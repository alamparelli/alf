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
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o /recall-tools ./cmd/memory-tools

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

# Install ONNX Runtime + tokenizers for semantic memory embeddings.
# Replaces PyTorch/sentence-transformers (~1 GB) with ONNX (~180 MB).
RUN pip3 install --break-system-packages onnxruntime tokenizers numpy \
    && rm -rf /root/.cache/pip

# Pre-download ONNX embedding model to avoid first-run delay.
RUN mkdir -p /opt/alf/models/all-MiniLM-L6-v2 \
    && curl -fsSL -o /opt/alf/models/all-MiniLM-L6-v2/model.onnx \
       "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model.onnx" \
    && curl -fsSL -o /opt/alf/models/all-MiniLM-L6-v2/tokenizer.json \
       "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/tokenizer.json"

# Python CVE audit runs in go test (internal/vulncheck/) with scoped deps.
# Removed from Dockerfile — pip-audit lacks package exclusion, and base image
# pip/setuptools CVEs are false positives that block builds.

# Enable OpenMP threading for ctranslate2 (faster-whisper backend).
ENV OMP_NUM_THREADS=4

# Install Go for Claude agent to build CLI tools
RUN curl -fsSL "https://go.dev/dl/go1.24.13.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz
ENV PATH="/opt/alf/tools:/usr/local/go/bin:/home/node/go/bin:${PATH}"
ENV GOPATH="/home/node/go"

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

COPY --from=builder /alf-daemon /opt/alf/alf-daemon
COPY --from=builder /extract-video /opt/alf/tools/extract-video
COPY --from=builder /recall-tools /opt/alf/tools/recall-tools
COPY scripts/transcribe.py /opt/alf/transcribe.py
COPY scripts/embed.py /opt/alf/embed.py

# Create memory tool symlinks (recall, remember, forget → recall-tools).
RUN ln -s /opt/alf/tools/recall-tools /opt/alf/tools/recall \
    && ln -s /opt/alf/tools/recall-tools /opt/alf/tools/remember \
    && ln -s /opt/alf/tools/recall-tools /opt/alf/tools/forget

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
