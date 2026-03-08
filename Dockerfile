# Stage 1: Build Go binaries with CGO (sqlite-vec, whisper.cpp on arm64).
FROM golang:1.24-bookworm AS builder

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ libc6-dev libsqlite3-dev cmake make && rm -rf /var/lib/apt/lists/*

# Build whisper.cpp static library (arm64 only — faster-whisper doesn't work on ARM).
WORKDIR /whisper
RUN if [ "${TARGETARCH}" = "arm64" ]; then \
      git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git . \
      && cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
         -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF \
         -DGGML_CPU=ON -DGGML_NATIVE=OFF \
      && cmake --build build -j$(nproc) \
      && cmake --install build --prefix /usr/local; \
    fi

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build Go binaries.
# arm64: link whisper.cpp for native transcription.
# amd64: no whisper.cpp needed (uses faster-whisper Python subprocess).
RUN if [ "${TARGETARCH}" = "arm64" ]; then \
      export CGO_CFLAGS="-I/usr/local/include" && \
      export CGO_LDFLAGS="-L/usr/local/lib -lwhisper -lggml -lggml-base -lggml-cpu -lstdc++ -lm -lpthread" && \
      CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon && \
      CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /extract-video ./cmd/extract-video; \
    else \
      CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon && \
      CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /extract-video ./cmd/extract-video; \
    fi \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /recall-tools ./cmd/memory-tools \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /telegram-tools ./cmd/signal \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /schedule-tools ./cmd/schedule-tools

# Stage 2: Runtime.
FROM debian:bookworm-slim

ARG TARGETARCH

# Base packages. python3-pip only on amd64 (for faster-whisper).
# libgomp1 only on arm64 (for whisper.cpp/ggml OpenMP).
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    trash-cli \
    poppler-utils \
    xz-utils \
    && if [ "${TARGETARCH}" = "arm64" ]; then \
         apt-get install -y --no-install-recommends libgomp1; \
       else \
         apt-get install -y --no-install-recommends python3-pip; \
       fi \
    && rm -rf /var/lib/apt/lists/*

# Static ffmpeg+ffprobe (~80 MB unpacked vs ~400 MB Debian packages).
RUN if [ "${TARGETARCH}" = "arm64" ]; then FFARCH="arm64"; else FFARCH="amd64"; fi \
    && curl -fsSL "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${FFARCH}-static.tar.xz" \
       | tar xJ --strip-components=1 --wildcards -C /usr/local/bin '*/ffmpeg' '*/ffprobe' \
    && ffmpeg -version > /dev/null

# Download ONNX Runtime shared library (CPU-only, ~20 MB).
RUN if [ "${TARGETARCH}" = "amd64" ]; then ARCH="x64"; \
    elif [ "${TARGETARCH}" = "arm64" ]; then ARCH="aarch64"; \
    else ARCH="${TARGETARCH}"; fi \
    && curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v1.24.2/onnxruntime-linux-${ARCH}-1.24.2.tgz" \
       | tar xz --strip-components=2 -C /usr/local/lib \
         "onnxruntime-linux-${ARCH}-1.24.2/lib/libonnxruntime.so.1.24.2" \
         "onnxruntime-linux-${ARCH}-1.24.2/lib/libonnxruntime.so.1" \
         "onnxruntime-linux-${ARCH}-1.24.2/lib/libonnxruntime.so" \
    && ldconfig

# Pre-download ONNX embedding model to avoid first-run delay.
RUN mkdir -p /opt/alf/models/all-MiniLM-L6-v2 \
    && curl -fsSL -o /opt/alf/models/all-MiniLM-L6-v2/model.onnx \
       "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model.onnx" \
    && curl -fsSL -o /opt/alf/models/all-MiniLM-L6-v2/tokenizer.json \
       "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/tokenizer.json"

# Claude Code native binary.
# Keep ~/.local/bin/claude so Claude Code recognises the native install.
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && cp "$(readlink -f /root/.local/bin/claude)" /usr/local/bin/claude \
    && rm -rf /root/.local/share/claude /root/.claude \
    && claude --version

ENV PATH="/opt/alf/tools.d:${PATH}"

COPY internal/controlcenter/defaults/tiers.json /opt/alf/defaults/tiers.json
COPY --from=builder /alf-daemon /opt/alf/alf-daemon
COPY --from=builder /extract-video /opt/alf/bin/extract-video
COPY --from=builder /recall-tools /opt/alf/bin/recall-tools
COPY --from=builder /telegram-tools /opt/alf/bin/telegram-tools
COPY --from=builder /schedule-tools /opt/alf/bin/schedule-tools

# Transcription script (used on amd64 with faster-whisper, ignored on arm64).
COPY scripts/transcribe.py /opt/alf/transcribe.py

# Tool symlinks: clean names only, pointing to binaries in /opt/alf/bin/.
RUN mkdir -p /opt/alf/tools.d \
    && ln -s /opt/alf/bin/extract-video /opt/alf/tools.d/extract-video \
    && ln -s /opt/alf/bin/recall-tools /opt/alf/tools.d/recall \
    && ln -s /opt/alf/bin/recall-tools /opt/alf/tools.d/remember \
    && ln -s /opt/alf/bin/recall-tools /opt/alf/tools.d/forget \
    && ln -s /opt/alf/bin/telegram-tools /opt/alf/tools.d/react \
    && ln -s /opt/alf/bin/telegram-tools /opt/alf/tools.d/status \
    && ln -s /opt/alf/bin/schedule-tools /opt/alf/tools.d/schedule

# Create users for two-user privilege model.
RUN groupadd --gid 1000 alf \
    && useradd --uid 1000 --gid alf --shell /bin/bash --create-home alf \
    && useradd -u 1001 -g alf -s /bin/bash -M claude \
    && mkdir -p /home/claude && chown claude:alf /home/claude

# Directory structure for volumes.
RUN mkdir -p /home/alf/data/logs /home/alf/data/sessions \
    && mkdir -p /home/alf/data/tools /home/alf/data/skills \
    && mkdir -p /home/alf/data/pages \
    && mkdir -p /home/alf/data/.claude \
    && mkdir -p /home/alf/data/config.d /home/alf/data/skills.d \
    && mkdir -p /opt/alf/config.d \
    && mkdir -p /opt/alf/user-packages/bin /opt/alf/user-packages/lib \
    && chown -R root:alf /home/alf/data \
    && chmod -R g+ws /home/alf/data \
    && chown -R root:root /opt/alf/config.d \
    && chmod 755 /opt/alf/config.d \
    && chmod -R 755 /opt/alf/tools.d \
    && chmod -R 755 /opt/alf/bin

# Git safe directory for all users (data dir is root:alf, accessed by alf+claude).
RUN git config --system --add safe.directory /home/alf/data

WORKDIR /home/alf

ENV HOME=/home/alf

COPY scripts/entrypoint.sh /opt/alf/entrypoint.sh
RUN chmod +x /opt/alf/entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/opt/alf/entrypoint.sh"]
