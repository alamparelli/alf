#!/bin/sh
set -e

# ALF Alpha installer (private distribution)
# Usage: curl -fsSL https://cc.lamparelli.eu/alpha/install.sh | ALF_TOKEN=<password> sh

BASE_URL="https://cc.lamparelli.eu/alpha"
INSTALL_DIR="${HOME}/.local/bin"

main() {
    if [ -z "$ALF_TOKEN" ]; then
        printf "ALF_TOKEN required. Usage:\n\n"
        printf "  curl -fsSL %s/install.sh | ALF_TOKEN=<password> sh\n\n" "$BASE_URL"
        exit 1
    fi

    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) echo "Unsupported architecture: $arch" && exit 1 ;;
    esac

    case "$os" in
        linux|darwin) ;;
        *) echo "Unsupported OS: $os" && exit 1 ;;
    esac

    filename="alf-${os}-${arch}"
    url="${BASE_URL}/${filename}"

    echo "Downloading alf (alpha) for ${os}/${arch}..."
    tmpdir=$(mktemp -d)

    http_code=$(curl -fsSL -o "${tmpdir}/alf" -w '%{http_code}' -u "alpha:${ALF_TOKEN}" "$url" 2>/dev/null) || true

    if [ ! -s "${tmpdir}/alf" ] || [ "$http_code" = "401" ] || [ "$http_code" = "403" ]; then
        echo "Authentication failed. Check your ALF_TOKEN."
        rm -rf "$tmpdir"
        exit 1
    fi

    chmod +x "${tmpdir}/alf"

    # Verify it's a real binary (check ELF/Mach-O magic bytes, no dependency on `file`)
    head_bytes=$(od -A n -t x1 -N 4 "${tmpdir}/alf" 2>/dev/null | tr -d ' ')
    case "$head_bytes" in
        7f454c46) ;; # ELF
        cafebabe|feedface|feedfacf|cffaedfe|cefaedfe) ;; # Mach-O
        *)
            echo "Download failed — invalid binary (got HTML or bad response)."
            rm -rf "$tmpdir"
            exit 1
            ;;
    esac

    # Install
    if [ -w "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR" 2>/dev/null; then
        mv "${tmpdir}/alf" "${INSTALL_DIR}/alf"
        echo "Installed to ${INSTALL_DIR}/alf"
    elif [ -w "/usr/local/bin" ]; then
        mv "${tmpdir}/alf" "/usr/local/bin/alf"
        INSTALL_DIR="/usr/local/bin"
        echo "Installed to /usr/local/bin/alf"
    else
        sudo mkdir -p /usr/local/bin
        sudo mv "${tmpdir}/alf" "/usr/local/bin/alf"
        INSTALL_DIR="/usr/local/bin"
        echo "Installed to /usr/local/bin/alf (with sudo)"
    fi

    rm -rf "$tmpdir"

    # Save alpha token for auto-updates via alf upgrade
    echo "$ALF_TOKEN" > "${HOME}/.alf_alpha_token"
    chmod 600 "${HOME}/.alf_alpha_token"

    # Add to PATH if needed
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            if [ -f "$HOME/.zshrc" ]; then
                profile="$HOME/.zshrc"
            elif [ -f "$HOME/.bashrc" ]; then
                profile="$HOME/.bashrc"
            elif [ -f "$HOME/.profile" ]; then
                profile="$HOME/.profile"
            else
                profile="$HOME/.profile"
            fi

            echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "$profile"
            export PATH="${INSTALL_DIR}:$PATH"
            echo "Added ${INSTALL_DIR} to PATH in ${profile}"
            ;;
    esac

    echo ""
    echo "Run 'alf init' to get started."
}

main
