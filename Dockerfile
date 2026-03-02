# Stage 1: Build Go daemon (debian for CGo — sqlite-vec requires CGO_ENABLED=1)
FROM --platform=$BUILDPLATFORM golang:1.24.13 AS builder
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH go build -tags fts5 -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon \
    && CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH go build -tags fts5 -ldflags="-s -w" -o /extract-video ./cmd/extract-video \
    && CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /memory-tools ./cmd/memory-tools

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
    poppler-utils \
    && rm -rf /var/lib/apt/lists/*

# Install faster-whisper for voice transcription.
# libgomp1 is required for OpenMP parallel CPU execution in ctranslate2.
RUN pip3 install --break-system-packages faster-whisper

# Install sentence-transformers for semantic memory embeddings.
RUN pip3 install --break-system-packages sentence-transformers

# Pre-download embedding model to avoid first-run delay.
RUN python3 -c "from sentence_transformers import SentenceTransformer; SentenceTransformer('all-MiniLM-L6-v2')"

# Audit Python dependencies for known CVEs (fails build on vulnerabilities).
RUN pip3 install --break-system-packages pip-audit \
    && pip-audit --desc --skip-editable

# Enable OpenMP threading for ctranslate2 (faster-whisper backend).
ENV OMP_NUM_THREADS=4

# Install Go for Claude agent to build CLI tools
RUN curl -fsSL "https://go.dev/dl/go1.24.13.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz
ENV PATH="/opt/alf/tools:/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH="/home/node/go"

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

COPY --from=builder /alf-daemon /opt/alf/alf-daemon
COPY --from=builder /extract-video /opt/alf/tools/extract-video
COPY --from=builder /memory-tools /opt/alf/tools/memory-tools
COPY scripts/transcribe.py /opt/alf/transcribe.py
COPY scripts/embed.py /opt/alf/embed.py

# Create memory tool symlinks (memory-search, memory-store → memory-tools).
RUN ln -s /opt/alf/tools/memory-tools /opt/alf/tools/memory-search \
    && ln -s /opt/alf/tools/memory-tools /opt/alf/tools/memory-store

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
