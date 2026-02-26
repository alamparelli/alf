#!/bin/sh
set -e

# ALF CLI installer
# Usage: curl -fsSL https://raw.githubusercontent.com/alamparelli/alf/main/scripts/install.sh | sh

REPO="alamparelli/alf"
INSTALL_DIR="${HOME}/.local/bin"

main() {
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

    # Get latest release tag
    tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
    if [ -z "$tag" ]; then
        echo "Failed to fetch latest release"
        exit 1
    fi

    filename="alf-${os}-${arch}"
    url="https://github.com/${REPO}/releases/download/${tag}/${filename}"

    echo "Downloading alf ${tag} for ${os}/${arch}..."
    tmpdir=$(mktemp -d)
    curl -fsSL -o "${tmpdir}/alf" "$url"
    chmod +x "${tmpdir}/alf"

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

    # Add to PATH if needed
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            # Detect shell profile
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
            echo "Added ${INSTALL_DIR} to PATH in ${profile}"
            echo ""
            echo "To start using alf, first reload your shell:"
            echo ""
            echo "  source ${profile}"
            echo ""
            echo "Then run 'alf init' to get started."
            return
            ;;
    esac

    echo ""
    echo "Run 'alf init' to get started."
}

main
