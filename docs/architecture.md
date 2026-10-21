# Architecture

## Layers

```
cmd/agent-in-browser        CLI: mcp | call | run | launch | status | stop | setup | skill | tools
internal/mcp                MCP server (JSON-RPC 2.0 over stdio, no SDK dependency)
internal/scenario           YAML scenario runner + JUnit/JSON reports
internal/tools              THE tool catalogue: name, JSON schema, handler
internal/session            attach-or-launch, current tab, persisted instance state
internal/page               injected JS library (snapshot, refs, find, fill, scroll, check)
internal/browser            Driver interface, discovery, launching, instance state, keys
internal/cdp                Driver over Chrome DevTools Protocol (flattened sessions)
internal/bidi               Driver over WebDriver BiDi
internal/rpc                JSON-over-WebSocket transport shared by CDP and BiDi
internal/setup              MCP client registration + skill installation
skills/                     agent skill, embedded into the binary
```

Dependencies: `github.com/coder/websocket` and `gopkg.in/yaml.v3`. Everything else uses the standard library. Builds use `CGO_ENABLED=0`, so the binary is static.

## One catalogue, three front ends

`internal/tools` registers every tool once. The MCP server lists them from it (`tools/list`). `agent-in-browser call` invokes them, turning `key=value` pairs or JSON into arguments. Scenario steps are the same tool calls (`- click: {ref: e3}`). A new tool therefore shows up for agents, the shell and tests at the same time, and they all behave identically.

## Driver contract

`browser.Driver` is intentionally small: tabs, navigate, evaluate, screenshot, trusted mouse/keyboard input, viewport, and console/network buffers. Higher-level behaviour lives in `internal/page/lib.js`, which runs through `Evaluate`:

- **snapshot**: walks the DOM, including open shadow roots and same-origin iframes. It emits landmarks, headings and visible interactive elements, and assigns persistent refs (`data-aib-ref="e12"`).
- **point**: resolves `ref | selector | match` to an element, scrolls it into view and returns viewport coordinates, including iframe offsets. The driver then clicks with real input events, so pages see `isTrusted === true`.
- **setValue**: framework-safe value setting (native setter + `input`/`change`) for inputs, selects (by value or label), checkboxes and radios.

A new backend (Safari/WebDriver classic, or a browser extension transport) only has to implement the primitives. Snapshot, refs and forms then work unchanged.

Every evaluate is wrapped so it resolves to a JSON string (`page.Wrap`). This hides the protocols' different value serialisations: CDP `returnByValue` vs BiDi `RemoteValue`.

## Sessions and instances

- **Persistent instance** (default; MCP and `call`): the session reads `~/.agent-in-browser/instances/<name>.json`. If the endpoint answers, it attaches. Otherwise it launches the browser on `~/.agent-in-browser/profiles/<name>-<browser>`, detached so it outlives the caller, and saves the endpoint. Liveness is checked by **endpoint reachability**, not PID, because Firefox re-execs itself on a fresh profile.
- **Ephemeral** (scenarios): a temporary profile that is killed and deleted at the end.
- **External** (`--endpoint`): attaches to a browser someone else started, and never closes it.

The current tab is part of the instance state, so consecutive CLI calls act on the same tab.

## MCP server

Requests are read line by line. `initialize`, `ping` and `tools/list` are answered inline. `tools/call` requests go to a single FIFO worker, so pipelined calls such as navigate → click → read run in order while pings stay responsive. Logs go to stderr only.

## Console and network capture

Drivers subscribe to protocol events and keep bounded per-tab buffers of the last 1000 entries:

- CDP: `Runtime.consoleAPICalled`, `Runtime.exceptionThrown`, `Log.entryAdded`, `Network.*`
- BiDi: `log.entryAdded`, `network.beforeRequestSent|responseCompleted|fetchError`

The buffers live in the process that is attached. A long-lived MCP server accumulates history, while a one-shot `call` only sees events from its own lifetime.
