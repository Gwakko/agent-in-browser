#!/bin/sh
# agent-in-browser installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/gwakko/agent-in-browser/main/scripts/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version v0.1.0 --dir /usr/local/bin --setup claude-code
#
# Options (or env vars):
#   --version TAG   AIB_VERSION   release tag (default: latest)
#   --dir DIR       AIB_DIR       install directory (default: ~/.local/bin)
#   --setup CLIENT  AIB_SETUP     register the MCP server with an agent afterwards
#                                 (claude-code, cursor, codex, gemini, windsurf, claude-desktop, opencode)
#   --skill AGENT   AIB_SKILL     install the agent skill (claude, codex, agents)
#   --from-source                 build with `go install` instead of downloading
set -eu

REPO="gwakko/agent-in-browser"
BIN="agent-in-browser"
VERSION="${AIB_VERSION:-latest}"
DIR="${AIB_DIR:-$HOME/.local/bin}"
SETUP="${AIB_SETUP:-}"
SKILL="${AIB_SKILL:-}"
FROM_SOURCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --dir) DIR="$2"; shift 2 ;;
    --setup) SETUP="$2"; shift 2 ;;
    --skill) SKILL="$2"; shift 2 ;;
    --from-source) FROM_SOURCE=1; shift ;;
    -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else die "curl or wget is required"; fi
}

mkdir -p "$DIR"

if [ "$FROM_SOURCE" = 1 ]; then
  need go
  ref="latest"; [ "$VERSION" != latest ] && ref="$VERSION"
  say "building from source (go install ...@$ref)"
  GOBIN="$DIR" CGO_ENABLED=0 go install "github.com/$REPO/cmd/$BIN@$ref"
else
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in darwin|linux) ;; *) die "unsupported OS $os (use scripts/install.ps1 on Windows)";; esac
  arch=$(uname -m)
  case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) die "unsupported arch $arch" ;; esac

  if [ "$VERSION" = latest ]; then
    tmpj=$(mktemp)
    fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmpj"
    VERSION=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmpj" | head -1)
    rm -f "$tmpj"
    [ -n "$VERSION" ] || die "could not determine the latest release"
  fi
  num="${VERSION#v}"
  asset="${BIN}_${num}_${os}_${arch}.tar.gz"
  base="https://github.com/$REPO/releases/download/$VERSION"
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT

  say "downloading $asset ($VERSION)"
  fetch "$base/$asset" "$tmp/$asset"
  fetch "$base/checksums.txt" "$tmp/checksums.txt"

  expected=$(grep " $asset\$" "$tmp/checksums.txt" | awk '{print $1}')
  [ -n "$expected" ] || die "no checksum for $asset"
  if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
  else actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
  [ "$expected" = "$actual" ] || die "checksum mismatch for $asset"

  tar -xzf "$tmp/$asset" -C "$tmp"
  install -m 0755 "$tmp/$BIN" "$DIR/$BIN"
  # macOS: drop the quarantine flag so Gatekeeper doesn't block an unsigned CLI.
  [ "$os" = darwin ] && xattr -d com.apple.quarantine "$DIR/$BIN" 2>/dev/null || true
fi

ln -sf "$DIR/$BIN" "$DIR/aib"
say "installed $DIR/$BIN (alias: aib) — $("$DIR/$BIN" version)"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) say "add $DIR to your PATH, e.g.: echo 'export PATH=\"$DIR:\$PATH\"' >> ~/.zshrc" ;;
esac

"$DIR/$BIN" browsers || true

if [ -n "$SETUP" ]; then
  say "registering MCP server with $SETUP"
  "$DIR/$BIN" setup "$SETUP" --write
fi
if [ -n "$SKILL" ]; then
  "$DIR/$BIN" skill install --for "$SKILL"
fi

cat <<EOF

Next steps:
  $BIN setup claude-code --write      # or: cursor, codex, gemini, windsurf, claude-desktop, opencode
  $BIN skill install --for claude     # teach the agent how to use it (claude, codex, agents)
  $BIN call navigate url=example.com  # try it from the shell
EOF
