# Stage 1: Build Go daemon on target platform (CGo cross-compilation breaks sqlite-vec header resolution).
# Uses QEMU emulation when host != target — slower but reliable.
FROM golang:1.24.13 AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev libsqlite3-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon \
    && CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /extract-video ./cmd/extract-video \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /recall-tools ./cmd/memory-tools \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /telegram-tools ./cmd/signal

# Stage 2: Runtime — minimal Debian with Claude Code native binary (no Node.js).
FROM debian:bookworm-slim

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    trash-cli \
    python3 \
    python3-pip \
    libgomp1 \
    poppler-utils \
    xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Static ffmpeg+ffprobe (~80 MB unpacked vs ~400 MB Debian packages).
RUN if [ "${TARGETARCH}" = "arm64" ]; then FFARCH="arm64"; else FFARCH="amd64"; fi \
    && curl -fsSL "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${FFARCH}-static.tar.xz" \
       | tar xJ --strip-components=1 --wildcards -C /usr/local/bin '*/ffmpeg' '*/ffprobe' \
    && ffmpeg -version > /dev/null

# faster-whisper is installed on first voice message (auto-install in transcribe.py).
# libgomp1 is required for OpenMP parallel CPU execution in ctranslate2.

# Download ONNX Runtime shared library (CPU-only, ~20 MB).
# Embeddings run in Go via onnxruntime_go — no Python needed.
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

# Enable OpenMP threading for ctranslate2 (faster-whisper backend).
ENV OMP_NUM_THREADS=4

# Claude Code native binary (standalone, no Node.js required).
# Uses official installer which handles URL resolution and checksum verification.
# Installer puts binary at ~/.local/share/claude/versions/<ver> with symlink at ~/.local/bin/claude.
# We copy the actual binary to /usr/local/bin so it's accessible to all users.
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && cp "$(readlink -f /root/.local/bin/claude)" /usr/local/bin/claude \
    && rm -rf /root/.local/share/claude /root/.local/bin/claude /root/.claude \
    && claude --version

ENV PATH="/opt/alf/tools:${PATH}"

COPY --from=builder /alf-daemon /opt/alf/alf-daemon
COPY --from=builder /extract-video /opt/alf/tools/extract-video
COPY --from=builder /recall-tools /opt/alf/tools/recall-tools
COPY --from=builder /telegram-tools /opt/alf/tools/telegram-tools
COPY scripts/transcribe.py /opt/alf/transcribe.py

# Create memory tool symlinks (recall, remember, forget → recall-tools).
RUN ln -s /opt/alf/tools/recall-tools /opt/alf/tools/recall \
    && ln -s /opt/alf/tools/recall-tools /opt/alf/tools/remember \
    && ln -s /opt/alf/tools/recall-tools /opt/alf/tools/forget \
    && ln -s /opt/alf/tools/telegram-tools /opt/alf/tools/react \
    && ln -s /opt/alf/tools/telegram-tools /opt/alf/tools/status

# Create users for two-user privilege model.
# node=1000:1000 (legacy name, kept for volume compatibility), claude=1001:1000.
RUN groupadd --gid 1000 node \
    && useradd --uid 1000 --gid node --shell /bin/bash --create-home node \
    && useradd -u 1001 -g node -s /bin/bash -M claude \
    && mkdir -p /home/claude && chown claude:node /home/claude

# Two-user privilege model:
#   Daemon runs as root — starts CC, spawns Claude subprocesses.
#   Claude -p runs as 'claude' (uid 1001, gid node/1000).
#   /home/node/data — root:node, group-writable (claude writes via group).
#   /opt/alf/config — root:root, 755 (only daemon/CC can write).
RUN mkdir -p /home/node/data/logs /home/node/data/sessions \
    && mkdir -p /home/node/data/tools /home/node/data/skills \
    && mkdir -p /home/node/data/.claude \
    && mkdir -p /opt/alf/config \
    && chown -R root:node /home/node/data \
    && chmod -R g+ws /home/node/data \
    && chown -R root:root /opt/alf/config \
    && chmod 755 /opt/alf/config \
    && chmod -R 755 /opt/alf/tools

WORKDIR /home/node

EXPOSE 8080

CMD ["/opt/alf/alf-daemon"]
