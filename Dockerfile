# Stage 1: Build Go daemon
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETARCH

WORKDIR /src
COPY go.mod ./
# COPY go.sum ./ (uncomment when dependencies exist)
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon \
    && CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /extract-video ./cmd/extract-video

# Stage 2: Runtime with Claude Code CLI
FROM node:22-slim

ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    ffmpeg \
    git \
    trash-cli \
    python3 \
    python3-pip \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

# Install faster-whisper for voice transcription.
# libgomp1 is required for OpenMP parallel CPU execution in ctranslate2.
RUN pip3 install --break-system-packages faster-whisper

# Enable OpenMP threading for ctranslate2 (faster-whisper backend).
ENV OMP_NUM_THREADS=4

# Install Go for Claude agent to build CLI tools
RUN curl -fsSL "https://go.dev/dl/go1.24.1.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz
ENV PATH="/opt/alf/tools:/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH="/home/node/go"

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

COPY --from=builder /alf-daemon /opt/alf/alf-daemon
COPY --from=builder /extract-video /opt/alf/tools/extract-video
COPY scripts/transcribe.py /opt/alf/transcribe.py

# Create 'claude' user for subprocess isolation (same group as node).
# node=1000:1000, claude=1001:1000 — shares 'node' group for data access.
RUN useradd -u 1001 -g node -s /bin/bash -M claude \
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
