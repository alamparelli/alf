# ALF Multi-Tenant Deployment

Deploy multiple isolated ALF instances on a single VPS with shared Traefik (TLS) and Whisper (speech-to-text).

## Architecture

```
                    Internet
                       │
                   ┌───┴───┐
                   │Traefik │  :80 / :443
                   │  TLS   │  Let's Encrypt
                   └───┬───┘
          ┌────────────┼────────────┐
          │            │            │
    ┌─────┴─────┐ ┌───┴─────┐ ┌───┴─────┐
    │alf-alice  │ │alf-bob  │ │alf-...  │
    │:8080      │ │:8080    │ │:8080    │
    └─────┬─────┘ └───┬─────┘ └───┬─────┘
          │            │            │
          └────────────┼────────────┘
                  ┌────┴────┐
                  │ Whisper │  10.99.0.10
                  │ (shared)│  internal network
                  └─────────┘
```

Each tenant gets:
- Its own ALF container with isolated data, config, secrets, and cache
- A unique subdomain with automatic TLS (e.g. `alice.alf.example.com`)
- Independent Telegram bot, API keys, vault

Shared across tenants:
- Traefik reverse proxy (TLS termination, routing)
- Whisper service (speech-to-text, single model in RAM)
- Let's Encrypt certificates

## Prerequisites

- A VPS with Docker + Docker Compose v2 installed
- A domain with wildcard DNS: `*.alf.example.com → VPS_IP`
- `jq` and `openssl` available on the host

## Quick Start

### 1. Clone and prepare

```bash
# On the VPS
git clone <repo> /opt/alf-src
cd /opt/alf-src/scripts/multi
chmod +x *.sh
```

### 2. Initialize infrastructure

```bash
export ACME_EMAIL=you@example.com

# Optional overrides:
# export ALF_MULTI_DIR=/opt/alf-multi    # default
# export ALF_IMAGE=ghcr.io/alamparelli/alf:0.6.82
# export WHISPER_MODEL=small             # tiny|base|small|medium|large

./init.sh
```

This creates:
```
/opt/alf-multi/
├── docker-compose.yml
├── tenants.json        # empty registry
├── .env                # saved configuration
├── letsencrypt/
└── shared/
    ├── whisper_shared_secret
    └── models/
```

### 3. Provision a tenant

```bash
./provision.sh \
  --user alice \
  --domain alice.alf.example.com \
  --timezone Europe/Rome
```

Optional flags:
- `--image ghcr.io/alamparelli/alf:0.6.82` — pin a specific ALF version

The script will:
1. Create isolated directories under `tenants/alice/`
2. Generate `cc_auth_token` and copy the shared whisper secret
3. Register the tenant in `tenants.json`
4. Regenerate `docker-compose.yml`
5. Start the container
6. Print a magic link for first login

### 4. Configure tenant secrets

```bash
# Set API keys for a tenant
./secret.sh --user alice set openrouter_api_key sk-or-v1-xxx
./secret.sh --user alice set telegram_bot_token 123456:ABC-xxx
./secret.sh --user alice set telegram_chat_id 987654321

# List secret status
./secret.sh --user alice list

# Apply changes
docker compose -f /opt/alf-multi/docker-compose.yml restart alf-alice
```

Available secrets:

| Secret | Description |
|--------|-------------|
| `telegram_bot_token` | Telegram bot token from @BotFather |
| `telegram_chat_id` | Telegram chat ID |
| `cc_auth_token` | Control Center auth (auto-generated) |
| `openrouter_api_key` | OpenRouter API key |
| `openai_api_key` | OpenAI API key |
| `claude_oauth_token` | Claude Code OAuth token |
| `vault_master_password` | Vault master password |
| `whisper_shared_secret` | Whisper auth (auto-copied from shared) |

## Management Commands

### List tenants

```bash
./list.sh
```

```
  USER            DOMAIN                              TIMEZONE        STATUS     CREATED
  ────            ──────                              ────────        ──────     ───────
  alice           alice.alf.example.com               Europe/Rome     running    2026-03-16T14:30:00Z
  bob             bob.alf.example.com                 UTC             exited     2026-03-16T15:00:00Z
```

### Generate magic link

```bash
./magic-link.sh --user alice
```

### Teardown a tenant

```bash
# Remove tenant and delete all data
./teardown.sh --user alice

# Remove tenant but keep data on disk
./teardown.sh --user alice --keep-data
```

### Regenerate compose

After manually editing `tenants.json` or changing environment variables:

```bash
export ACME_EMAIL=you@example.com  # required
./generate-compose.sh

# Apply
cd /opt/alf-multi && docker compose up -d
```

## Operations

### Restart a single tenant

```bash
cd /opt/alf-multi
docker compose restart alf-alice
```

### View logs

```bash
cd /opt/alf-multi
docker compose logs -f alf-alice        # single tenant
docker compose logs -f whisper          # shared whisper
docker compose logs -f traefik          # reverse proxy
```

### Update ALF image for a tenant

Edit `tenants.json` manually, then:

```bash
export ACME_EMAIL=you@example.com
./generate-compose.sh
cd /opt/alf-multi && docker compose pull alf-alice && docker compose up -d alf-alice
```

### Update ALF image for all tenants

```bash
cd /opt/alf-multi && docker compose pull && docker compose up -d
```

### Backup a tenant

```bash
tar czf alice-backup.tar.gz -C /opt/alf-multi/tenants alice/
```

### Restore a tenant

```bash
tar xzf alice-backup.tar.gz -C /opt/alf-multi/tenants/
# Then re-provision or regenerate compose
```

## Directory Structure

```
/opt/alf-multi/
├── docker-compose.yml          # auto-generated, do not edit
├── tenants.json                # tenant registry
├── .env                        # runtime config
├── letsencrypt/                # TLS certificates (shared)
├── shared/
│   ├── whisper_shared_secret   # auth token for whisper
│   └── models/                 # whisper model cache (~1GB for small)
└── tenants/
    └── <user>/
        ├── data/               # ALF data (conversations, logs, agents)
        ├── config.d/           # configuration (tiers.json, packages.txt)
        ├── skills.d/           # custom skills
        ├── secrets/            # per-tenant secrets
        ├── cache/              # expendable (claude, npm, local, cache)
        ├── vault-data/         # encrypted vault storage
        ├── local/              # user-installed packages
        └── resolv.conf         # DNS resolver
```

## Resource Usage

Per-tenant limits (configurable in `lib.sh`):
- **RAM**: 2 GB per ALF instance
- **CPU**: 2 cores per ALF instance

Shared services:
- **Whisper**: 2 GB RAM, 2 cores (model loaded once, shared by all)
- **Traefik**: minimal (~50 MB RAM)

**Estimate for 5 tenants**: ~12 GB RAM, 12 cores recommended.

## Troubleshooting

### Tenant not accessible

1. Check DNS resolves: `dig alice.alf.example.com`
2. Check container is running: `docker ps | grep alf-alice`
3. Check Traefik logs: `docker compose logs traefik | grep alice`
4. Check TLS cert issued: `curl -v https://alice.alf.example.com 2>&1 | grep subject`

### Whisper not working

1. Check whisper is running: `docker compose logs whisper`
2. Check shared secret matches: `diff shared/whisper_shared_secret tenants/alice/secrets/whisper_shared_secret`
3. Check network connectivity: `docker exec alf-alice curl -sf http://whisper:8000/health`

### Container keeps restarting

```bash
docker compose logs --tail 50 alf-alice
```

Common causes:
- Missing or empty required secret files (Docker Compose needs them to exist)
- Permission issues on data directories (should be owned by uid 1000)
