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

# Create claude user + shared alf group for permission model.
RUN useradd -u 1001 -s /bin/bash claude \
    && groupadd -g 1002 alf \
    && usermod -aG alf node \
    && usermod -aG alf claude

# Directories with proper ownership.
RUN mkdir -p /home/node/data/logs /home/node/data/sessions \
    && mkdir -p /home/node/data/config /home/node/data/tools /home/node/data/skills \
    && mkdir -p /home/node/data/.claude \
    && chown -R node:node /home/node \
    && chown claude:alf /home/node/data/config /home/node/data/tools /home/node/data/skills \
    && chmod 775 /home/node/data/config /home/node/data/tools /home/node/data/skills \
    && chown -R claude:claude /home/node/data/.claude \
    && chmod 700 /home/node/data/.claude

WORKDIR /home/node

EXPOSE 8080

CMD ["/opt/alf/alf-daemon"]
