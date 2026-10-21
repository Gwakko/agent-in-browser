# Browser support

`agent-in-browser browsers` lists what is installed and whether it can be driven.

## Chromium family (CDP)

Chrome (stable/beta/canary), Chromium, Edge, Brave, Vivaldi and Opera.

- Launched with `--remote-debugging-port=0 --user-data-dir=<dedicated profile>`. The real port is read from `DevToolsActivePort`.
- **Chrome 136+ ignores remote debugging on the default profile**, a security change against cookie theft. That's why a dedicated profile is always used. Logins you make inside the agent's window persist in that profile.
- Attach to a browser you started yourself:

  ```bash
  google-chrome --remote-debugging-port=9222 --user-data-dir=$HOME/.chrome-agent
  agent-in-browser call --endpoint http://127.0.0.1:9222 tabs_list
  ```
- Keyboard shortcuts like `Control+a`/`Meta+c` are mapped to editing commands, so they work on macOS too.

## Firefox (WebDriver BiDi)

Firefox, Developer Edition and Nightly. Firefox removed its experimental CDP in version 129. BiDi is the supported protocol.

- Launched with `--remote-debugging-port <port> --profile <dedicated profile> --no-remote`. `user.js` disables first-run screens.
- **Only one BiDi session per Firefox process.** While an MCP server is attached to an instance, a parallel `agent-in-browser call` against the same instance fails with "session not created". Use another `--instance` for parallel work. The scenario runner always uses its own instance.
- On a fresh profile, Firefox restarts itself once at first launch. This is handled automatically.

## Safari (planned)

Safari has no CDP/BiDi. Its automation goes through `safaridriver` (classic WebDriver over HTTP) and needs a one-time `safaridriver --enable` plus "Allow Remote Automation" in the Develop menu. It is detected today and planned as a `webdriver` driver, see the [roadmap](roadmap.md).

## Choosing a browser

| Flag / env | Effect |
|---|---|
| `--browser auto` (default) | first installed of: chrome, chrome-beta, chrome-canary, chromium, edge, brave, vivaldi, opera, firefox… |
| `--browser firefox` / `AIB_BROWSER=firefox` | a specific browser |
| `--exe /path/to/binary` | a custom build or portable install. Also set `--browser` to the engine family (e.g. `chromium`) |
| `--headless` / `AIB_HEADLESS=1` | no window. Scenarios are headless unless `--headed` |
| `--viewport 390x844` | window size on launch. The `resize` tool emulates a viewport later |
