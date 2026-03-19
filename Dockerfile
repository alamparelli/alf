# Stage 1: Build Go binaries with CGO (sqlite-vec).
FROM golang:1.25-bookworm AS builder

ARG TARGETARCH
ARG BUILD_VERSION=dev

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ libc6-dev libsqlite3-dev && rm -rf /var/lib/apt/lists/*

# Copy vault-proxy source (needed as Go module dependency + standalone binaries).
WORKDIR /vault-proxy
COPY third_party/vault-proxy/ .

WORKDIR /src
COPY go.mod go.sum ./
# Point replace directive to the vendored vault-proxy.
RUN go mod edit -replace github.com/alessandrolamparelli/vault-proxy=/vault-proxy \
    && go mod download

COPY . .
# Re-apply replace after COPY overwrites go.mod with the local-path version.
RUN go mod edit -replace github.com/alessandrolamparelli/vault-proxy=/vault-proxy

# Build Go binaries.
RUN CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w -X main.version=${BUILD_VERSION}" -o /alf-daemon ./cmd/alf-daemon \
    && CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o /extract-video ./cmd/extract-video \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /recall-tools ./cmd/memory-tools \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /telegram-tools ./cmd/signal \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /schedule-tools ./cmd/schedule-tools

# Build vault-proxy binaries (secrets vault for AI agents).
WORKDIR /vault-proxy
RUN CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /vault-server ./cmd/vault-server \
    && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /vault-cli ./cmd/vault-cli

# Stage 2: Runtime.
FROM debian:bookworm-slim

ARG TARGETARCH

# Base packages.
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    trash-cli \
    poppler-utils \
    xz-utils \
    sqlite3 \
    htop \
    jq \
    imagemagick \
    pandoc \
    build-essential \
    unzip \
    zip \
    wget \
    tree \
    ripgrep \
    less \
    file \
    openssh-client \
    rsync \
    nano \
    tmux \
    dnsutils \
    net-tools \
    procps \
    iproute2 \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI.
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
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

# Pre-download ONNX embedding model (multilingual-e5-small: 100 languages, 384 dims).
RUN mkdir -p /opt/alf/models/multilingual-e5-small \
    && curl -fsSL -o /opt/alf/models/multilingual-e5-small/model.onnx \
       "https://huggingface.co/intfloat/multilingual-e5-small/resolve/main/onnx/model.onnx" \
    && curl -fsSL -o /opt/alf/models/multilingual-e5-small/tokenizer.json \
       "https://huggingface.co/intfloat/multilingual-e5-small/resolve/main/tokenizer.json"

# Claude Code native binary.
# Keep ~/.local/bin/claude so Claude Code recognises the native install.
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && cp "$(readlink -f /root/.local/bin/claude)" /usr/local/bin/claude \
    && rm -rf /root/.local/share/claude /root/.claude \
    && claude --version

ENV PATH="/opt/alf/tools.d:${PATH}"

COPY internal/controlcenter/defaults/tiers.json /opt/alf/defaults/tiers.json
COPY skills.d/ /opt/alf/defaults/skills.d/
COPY internal/cli/bundled_agents/ /opt/alf/defaults/teams/
COPY internal/cli/bundled_apps/ /opt/alf/defaults/apps/
COPY --from=builder /alf-daemon /opt/alf/alf-daemon
COPY --from=builder /extract-video /opt/alf/bin/extract-video
COPY --from=builder /recall-tools /opt/alf/bin/recall-tools
COPY --from=builder /telegram-tools /opt/alf/bin/telegram-tools
COPY --from=builder /schedule-tools /opt/alf/bin/schedule-tools
COPY --from=builder /vault-server /opt/alf/bin/vault-server
COPY --from=builder /vault-cli /opt/alf/bin/vault-cli

# Tool symlinks: clean names only, pointing to binaries in /opt/alf/bin/.
RUN mkdir -p /opt/alf/tools.d \
    && ln -s /opt/alf/bin/extract-video /opt/alf/tools.d/extract-video \
    && ln -s /opt/alf/bin/recall-tools /opt/alf/tools.d/recall \
    && ln -s /opt/alf/bin/recall-tools /opt/alf/tools.d/remember \
    && ln -s /opt/alf/bin/recall-tools /opt/alf/tools.d/forget \
    && ln -s /opt/alf/bin/telegram-tools /opt/alf/tools.d/react \
    && ln -s /opt/alf/bin/telegram-tools /opt/alf/tools.d/status \
    && ln -s /opt/alf/bin/schedule-tools /opt/alf/tools.d/schedule \
    && ln -s /opt/alf/bin/vault-cli /opt/alf/tools.d/vault \
    && ln -s /opt/alf/bin/vault-server /usr/local/bin/vault-server

# Tool schemas for API-tier agentic tool loop.
COPY tool-schemas/*.json /opt/alf/tools.d/

# Create alf user (uid 1000). The daemon drops from root to this user via setpriv
# in the entrypoint. No separate subprocess user — container boundary is the isolation.
RUN groupadd --gid 1000 alf \
    && useradd --uid 1000 --gid alf --shell /bin/bash --create-home alf

# Directory structure for volumes.
RUN mkdir -p /home/alf/data/logs /home/alf/data/sessions \
    && mkdir -p /home/alf/data/tools /home/alf/data/skills \
    && mkdir -p /home/alf/data/.claude \
    && mkdir -p /home/alf/data/config.d /home/alf/data/skills.d \
    && mkdir -p /opt/alf/config.d \
    && mkdir -p /opt/alf/vault-data \
    && mkdir -p /opt/alf/user-packages/bin /opt/alf/user-packages/lib \
    && chown -R root:alf /home/alf/data \
    && chmod -R g+ws /home/alf/data \
    && chown -R alf:alf /opt/alf/config.d \
    && chmod 750 /opt/alf/config.d \
    && chown alf:alf /opt/alf/vault-data \
    && chmod 700 /opt/alf/vault-data \
    && chmod -R 755 /opt/alf/tools.d \
    && chmod -R 755 /opt/alf/bin

# Git safe directory (data dir is a volume mount, may have different ownership).
RUN git config --system --add safe.directory /home/alf/data

WORKDIR /home/alf

ENV HOME=/home/alf

COPY scripts/entrypoint.sh /opt/alf/entrypoint.sh
RUN chmod +x /opt/alf/entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/opt/alf/entrypoint.sh"]
