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
    openssh-server \
    sudo \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir /var/run/sshd

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

# Configure existing node user (uid 1000) for SSH access
RUN usermod -s /bin/bash node \
    && echo 'node:alf2026' | chpasswd \
    && usermod -aG sudo node \
    && echo 'node ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

# SSH config
RUN sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config

EXPOSE 22 8080

COPY --from=builder /alf-daemon /opt/alf/alf-daemon

RUN mkdir -p /home/node/.claude /home/node/data/logs && chown -R node:node /home/node

WORKDIR /home/node

# Start SSH, then run daemon as node user (no dash to preserve env vars)
CMD /usr/sbin/sshd && su node -c "/opt/alf/alf-daemon"
