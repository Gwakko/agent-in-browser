#!/bin/sh
# Render the Homebrew formula for a release from goreleaser's checksums.txt.
#   packaging/homebrew/formula.sh 0.1.1 dist/checksums.txt > Formula/agent-in-browser.rb
set -eu
V="$1" SUMS="$2"
URL="https://github.com/Gwakko/agent-in-browser/releases/download/v$V"
sha() { grep " agent-in-browser_${V}_$1.tar.gz\$" "$SUMS" | awk '{print $1}'; }

cat <<EOF
class AgentInBrowser < Formula
  desc "Real browser for any AI agent, model or test runner (MCP server + CLI)"
  homepage "https://github.com/Gwakko/agent-in-browser"
  version "$V"
  license "MIT"

  on_macos do
    on_arm do
      url "$URL/agent-in-browser_${V}_darwin_arm64.tar.gz"
      sha256 "$(sha darwin_arm64)"
    end
    on_intel do
      url "$URL/agent-in-browser_${V}_darwin_amd64.tar.gz"
      sha256 "$(sha darwin_amd64)"
    end
  end

  on_linux do
    on_arm do
      url "$URL/agent-in-browser_${V}_linux_arm64.tar.gz"
      sha256 "$(sha linux_arm64)"
    end
    on_intel do
      url "$URL/agent-in-browser_${V}_linux_amd64.tar.gz"
      sha256 "$(sha linux_amd64)"
    end
  end

  def install
    bin.install "agent-in-browser"
    bin.install_symlink "agent-in-browser" => "aib"
    (share/"agent-in-browser").install "skills"
  end

  def caveats
    <<~EOS
      Register with your agent:
        agent-in-browser setup claude-code --write   # or cursor, codex, gemini, windsurf, ...
        agent-in-browser skill install --for claude
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/agent-in-browser version")
    assert_match "navigate", shell_output("#{bin}/agent-in-browser tools")
  end
end
EOF
