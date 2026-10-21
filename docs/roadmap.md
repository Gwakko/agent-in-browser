# Roadmap

## Now (v0.1)

- [x] CDP driver: Chrome, Chromium, Edge, Brave, Vivaldi, Opera
- [x] WebDriver BiDi driver: Firefox
- [x] MCP stdio server, 23 tools
- [x] Shell CLI with a persistent shared browser, named instances, `--endpoint` attach
- [x] Injected page library: snapshot with refs, shadow DOM, same-origin iframes, framework-safe form filling
- [x] Scenario runner: YAML, browser matrix, assertions, JUnit/JSON reports, failure screenshots
- [x] `setup` for 9 MCP clients, embedded agent skill, install scripts, goreleaser, CI

## Next

- [ ] **Safari**: `webdriver` driver over safaridriver (classic WebDriver HTTP). The Driver contract already fits.
- [ ] **Your own logged-in browser**: a companion WebExtension (Chrome + Firefox) that relays the Driver primitives over native messaging, like Claude in Chrome. Builds on the existing `webprobe` work.
- [ ] Streamable HTTP transport for MCP (`agent-in-browser mcp --http :7777`) so remote agents and containers can connect.
- [ ] `daemon` mode: keep console/network buffers across one-shot `call`s.
- [ ] Tools: `upload_file`, `handle_dialog`, `pdf`, `cookies`, `storage`, `full_page` screenshots, element screenshots, `select_tab_by_url`, `downloads`.
- [ ] Record mode: capture an agent's tool calls into a scenario file (`--record session.yaml`).
- [ ] Scenario features: `include`, `before/after` hooks, data-driven runs (`matrix.vars`), parallel workers, retries, HTML report.
- [ ] Device emulation presets (`resize device="iPhone 15"`), geolocation, network throttling (CDP).
- [ ] Chromium via BiDi (chromedriver) so one protocol covers everything where possible.
- [x] Packaging: Homebrew tap, MCP bundle (.mcpb) for Claude Desktop, MCP Registry listing
- [ ] Packaging: Scoop bucket, npm wrapper (`npx agent-in-browser`), Docker image with Chromium + Firefox, Smithery
