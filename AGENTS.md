# Working on agent-in-browser

Go 1.24+, single static binary (`CGO_ENABLED=0`). Internal commands go through Task (`task --list`).

- Build/test: `task build`, `task test`, `task lint`. Real browsers: `task test:e2e` / `task smoke`.
- Architecture: [docs/architecture.md](docs/architecture.md). Keep the Driver interface small. Put page-level behaviour in `internal/page/lib.js` so every engine gets it.
- Adding a tool: register it in `internal/tools`. It then shows up automatically in MCP, `call` and scenarios. Run `task docs:tools` afterwards and mention it in `skills/agent-in-browser/SKILL.md` if agents should know about it.
- Bump `VERSION` in `lib.js` whenever you change it, so already-injected pages reload the library.
- Never write to stdout from the `mcp` code path except protocol messages. Logs go to stderr.
- Use an isolated data dir for manual testing: `AIB_HOME=$(mktemp -d) ./bin/agent-in-browser call …`.
- Fixture page for manual and e2e checks: `examples/fixtures/form.html`.
