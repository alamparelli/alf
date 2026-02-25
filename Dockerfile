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
    curl \
    openssh-server \
    sudo \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir /var/run/sshd

RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force

# Create alf user with SSH access
RUN useradd -m -s /bin/bash alf \
    && echo 'alf:alf2026' | chpasswd \
    && usermod -aG sudo alf \
    && echo 'alf ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

# SSH config
RUN sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config

EXPOSE 22

COPY --from=builder /alf-daemon /opt/alf/alf-daemon

RUN mkdir -p /home/alf/.claude && chown -R alf:alf /home/alf

WORKDIR /home/alf

# Start SSH, then run daemon as alf user
CMD /usr/sbin/sshd && su - alf -c "/opt/alf/alf-daemon"
