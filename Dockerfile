# Stage 1: Build Go daemon
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod ./
# COPY go.sum ./ (uncomment when dependencies exist)
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /alf-daemon ./cmd/alf-daemon

# Stage 2: Runtime with Claude Code CLI
FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

COPY --from=builder /alf-daemon /opt/alf/alf-daemon

# Create claude user for sandboxed subprocess (UID 1001).
RUN useradd -m -u 1001 -s /bin/bash claude

# Directories with proper ownership.
RUN mkdir -p /home/node/data/logs /home/node/data/sessions \
    && mkdir -p /home/claude/.claude \
    && chown -R node:node /home/node \
    && chown -R claude:claude /home/claude

WORKDIR /home/node
USER node

EXPOSE 8080

CMD ["/opt/alf/alf-daemon"]
