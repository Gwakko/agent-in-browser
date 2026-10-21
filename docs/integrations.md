# Agent integrations

The MCP server is just `agent-in-browser mcp` over stdio, so any MCP client works. `agent-in-browser setup <client>` prints the right snippet, and `--write` applies it. Add browser options after `mcp`: `["mcp", "--browser", "firefox", "--headless"]`.

Use the absolute binary path if the agent doesn't inherit your shell `PATH`, which is common for GUI apps. `setup` does this automatically.

## Claude Code

```bash
agent-in-browser setup claude-code --write
# = claude mcp add --scope user agent-in-browser -- /path/to/agent-in-browser mcp
agent-in-browser skill install --for claude      # ~/.claude/skills/agent-in-browser
```

It runs fine next to Claude in Chrome. Tools appear as `mcp__agent-in-browser__navigate` and so on.

## Claude Desktop

`~/Library/Application Support/Claude/claude_desktop_config.json` (macOS), `%APPDATA%\Claude\…` (Windows):

```json
{ "mcpServers": { "agent-in-browser": { "command": "/path/to/agent-in-browser", "args": ["mcp"] } } }
```

## Cursor / Windsurf / Gemini CLI

Same `mcpServers` shape:

- Cursor: `~/.cursor/mcp.json`, or `.cursor/mcp.json` per project
- Windsurf: `~/.codeium/windsurf/mcp_config.json`
- Gemini CLI: `~/.gemini/settings.json`

## OpenAI Codex CLI

`~/.codex/config.toml`:

```toml
[mcp_servers.agent-in-browser]
command = "/path/to/agent-in-browser"
args = ["mcp"]
```

Skill: `agent-in-browser skill install --for codex` (installs into `~/.codex/skills`).

## VS Code (GitHub Copilot agent mode)

`.vscode/mcp.json`:

```json
{ "servers": { "agent-in-browser": { "type": "stdio", "command": "agent-in-browser", "args": ["mcp"] } } }
```

## Zed

`settings.json`:

```json
{ "context_servers": { "agent-in-browser": { "command": "agent-in-browser", "args": ["mcp"] } } }
```

## opencode

`~/.config/opencode/opencode.json`:

```json
{ "mcp": { "agent-in-browser": { "type": "local", "command": ["agent-in-browser", "mcp"], "enabled": true } } }
```

## Agents without MCP

Any agent that can run shell commands can use `agent-in-browser call <tool> …`. Install the skill or paste `agent-in-browser skill print` into the agent's instructions (AGENTS.md, system prompt). Screenshots are saved as PNG files, and the path is printed so multimodal agents can open them.

## Custom agents / SDKs

Spawn `agent-in-browser mcp` as a stdio MCP server from your framework (the Claude Agent SDK, OpenAI Agents SDK, LangChain MCP adapters, …). You can also shell out to `agent-in-browser call --json <tool> '{…}'`, which prints the raw MCP result.

## Multiple agents at once

Give each agent its own browser with `--instance`:

```json
"args": ["mcp", "--instance", "cursor", "--browser", "edge"]
```
