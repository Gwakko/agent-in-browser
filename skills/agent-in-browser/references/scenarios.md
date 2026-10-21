# Scenario files

A scenario is a YAML list of steps. Each step is **one tool call** (the same tools and arguments as MCP and `call`), or one of the special steps `assert` or `sleep`. Every scenario × browser combination runs in a fresh, isolated, headless browser with a temporary profile.

```yaml
name: checkout happy path
browsers: [chrome, firefox]      # default matrix (CLI --browser overrides)
viewport: { width: 1280, height: 800 }
timeout: 20s                     # per step (default 30s)
vars:
  base: http://localhost:3000    # use as ${base}; also ${env.NAME}, ${dir}, ${browser}

steps:
  - navigate: ${base}/cart                      # shorthand → { url: ... }
  - assert: { title_contains: "Cart" }
  - click: Checkout                             # shorthand → { match: "Checkout" }
  - fill_form:
      fields:
        - { selector: "#email", value: "${env.TEST_EMAIL}" }
        - { match: "Country", value: "Ukraine" }
        - { match: "Accept terms", value: true }
  - name: submit order
    click: { selector: "button[type=submit]" }
  - wait_for: { text: "Thank you", timeout_ms: 10000 }
  - assert:
      url_matches: "/orders/\\d+$"
      selector: ".order-total"
      text: "$42.00"
  - assert: { no_console_errors: true }
  - screenshot: artifacts/checkout-${browser}.png
  - click: { match: "Rate us" }
    optional: true                              # failure is reported but not fatal
```

## Shorthands

A scalar argument maps to the tool's main parameter:

| Step | Maps to |
|---|---|
| `navigate`, `tab_new` | `url` |
| `click`, `hover` | `match` |
| `press_key` | `keys` |
| `evaluate` | `code` |
| `wait_for` | `text` |
| `screenshot` | `path` |
| `find` | `query` |
| `sleep` | `ms` |

## Assertions

Several checks can be combined in one `assert`. All of them must pass.

| Key | Passes when |
|---|---|
| `url_contains` / `url_matches` | URL contains the substring / matches the regex |
| `title_contains` | the document title contains the substring |
| `text_contains` / `text_not_contains` | `document.body.innerText` does / doesn't contain the substring |
| `selector` (+ `text`, `count`) | the element exists (its text contains `text`; exactly `count` matches) |
| `js` | the expression or function body is truthy |
| `no_console_errors: true` | no console errors and no uncaught exceptions were captured |

## Running

```bash
agent-in-browser run tests/*.yaml                        # scenario's browsers, or auto
agent-in-browser run tests/*.yaml --browser chrome,firefox,edge
agent-in-browser run tests/*.yaml --junit report.xml --json report.json --artifacts out/
agent-in-browser run tests/login.yaml --headed           # watch it
```

The exit code is non-zero when any run fails. A failure screenshot goes to `--artifacts` (default `aib-artifacts/`).
