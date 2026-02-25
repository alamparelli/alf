#!/bin/bash
# Sets up local dev secrets for docker compose build+up testing.
# Usage: ./scripts/dev-setup.sh

set -e

DIR="dev-secrets"
mkdir -p "$DIR"

# Check if secrets already exist
if [ -f "$DIR/telegram_bot_token" ] && [ -f "$DIR/telegram_chat_id" ] && [ -f "$DIR/cc_auth_token" ]; then
    echo "Dev secrets already exist in $DIR/"
    echo "To reset, delete the directory and run again."
    exit 0
fi

echo "Setting up local dev secrets..."

# Telegram bot token
if [ -f "$DIR/telegram_bot_token" ]; then
    echo "  telegram_bot_token: already set"
else
    read -p "  Telegram bot token: " token
    echo -n "$token" > "$DIR/telegram_bot_token"
    chmod 600 "$DIR/telegram_bot_token"
    echo "  telegram_bot_token: saved"
fi

# Telegram chat ID
if [ -f "$DIR/telegram_chat_id" ]; then
    echo "  telegram_chat_id: already set"
else
    read -p "  Telegram chat ID: " chatid
    echo -n "$chatid" > "$DIR/telegram_chat_id"
    chmod 600 "$DIR/telegram_chat_id"
    echo "  telegram_chat_id: saved"
fi

# CC auth token (auto-generated)
if [ -f "$DIR/cc_auth_token" ]; then
    echo "  cc_auth_token: already set"
else
    token=$(openssl rand -hex 32)
    echo -n "$token" > "$DIR/cc_auth_token"
    chmod 600 "$DIR/cc_auth_token"
    echo "  cc_auth_token: generated"
fi

echo ""
echo "Done. Now run:"
echo "  docker compose up --build"
echo ""
echo "Dashboard: http://localhost:8080?token=$(cat $DIR/cc_auth_token)"
