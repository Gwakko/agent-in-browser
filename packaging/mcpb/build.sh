#!/bin/sh
# Build a cross-platform MCP Bundle (.mcpb) for Claude Desktop and the MCP Registry.
#
#   packaging/mcpb/build.sh VERSION DARWIN_UNIVERSAL_BIN LINUX_AMD64_BIN WINDOWS_AMD64_EXE OUT_DIR
#
# The bundle holds one binary per OS; manifest platform_overrides pick the right one.
set -eu
VERSION="$1" DARWIN="$2" LINUX="$3" WINDOWS="$4" OUT="$5"
HERE=$(cd "$(dirname "$0")" && pwd)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/server"
cp "$DARWIN" "$work/server/agent-in-browser"
cp "$LINUX" "$work/server/agent-in-browser-linux"
cp "$WINDOWS" "$work/server/agent-in-browser.exe"
chmod 755 "$work/server/agent-in-browser" "$work/server/agent-in-browser-linux"
cp "$HERE/icon.png" "$work/icon.png"

# Tool list comes from the binary itself, so the manifest never drifts.
case "$(uname -s)" in
  Darwin) self="$work/server/agent-in-browser" ;;
  *) self="$work/server/agent-in-browser-linux" ;;
esac
tools=$("$self" tools --json | jq '[.[] | {name, description: (.description | split(". ")[0] | rtrimstr("."))}]')

jq --arg v "$VERSION" --argjson tools "$tools" '.version = $v | .tools = $tools' "$HERE/manifest.json" > "$work/manifest.json"

mkdir -p "$OUT"
out="$(cd "$OUT" && pwd)/agent-in-browser-$VERSION.mcpb"
rm -f "$out"
(cd "$work" && zip -qr -X "$out" manifest.json icon.png server)
echo "$out"
