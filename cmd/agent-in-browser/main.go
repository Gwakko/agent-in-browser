// Command agent-in-browser gives AI agents (and tests) a real browser: an MCP
// server, a one-shot CLI and a scenario runner in one dependency-free binary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	agentinbrowser "github.com/gwakko/agent-in-browser"
	"github.com/gwakko/agent-in-browser/internal/browser"
	"github.com/gwakko/agent-in-browser/internal/mcp"
	"github.com/gwakko/agent-in-browser/internal/scenario"
	"github.com/gwakko/agent-in-browser/internal/session"
	"github.com/gwakko/agent-in-browser/internal/setup"
	"github.com/gwakko/agent-in-browser/internal/tools"
)

var version = "dev"

func init() {
	// `go install …@vX` builds carry the module version instead of ldflags.
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

const usage = `agent-in-browser — a real browser for any AI agent, model or test runner.

Usage:
  agent-in-browser <command> [flags]

Agent integration:
  mcp                 Run the MCP server on stdio (configure this in your agent)
  setup <client>      Register the MCP server with an agent: claude-code, claude-desktop,
                      cursor, windsurf, gemini, opencode, codex, vscode, zed, generic
  skill install       Install the agent skill (SKILL.md) for claude / codex / agents / --dir

Shell usage (for agents without MCP, scripts and humans):
  launch              Start (or reuse) the browser instance and print its status
  call <tool> [args]  Run one tool: args as JSON or key=value pairs
  tools               List tools (--json for schemas, --markdown for docs)
  status              Show the running instance
  stop                Close the browser instance
  browsers            List browsers found on this machine

Testing:
  run <file.yaml>...  Run scenario files in fresh isolated browsers (matrix with --browser a,b)

Other:
  version             Print version

Browser flags (mcp, launch, call, status, stop):
  --browser NAME      auto (default), chrome, edge, brave, chromium, vivaldi, opera, firefox, ...
  --headless          Run without a window
  --instance NAME     Named browser instance (default "default"); separate instances run side by side
  --endpoint URL      Attach to a running browser: http://127.0.0.1:9222 (CDP) or ws://.../session (BiDi)
  --exe PATH          Browser binary to use instead of auto-discovery
  --profile DIR       Profile (user data) directory
  --viewport WxH      Window/viewport size (default 1280x800)

Environment: AIB_BROWSER, AIB_HEADLESS=1, AIB_INSTANCE, AIB_ENDPOINT, AIB_HOME (data dir).
Docs: https://github.com/Gwakko/agent-in-browser
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "mcp", "serve":
		err = cmdMCP(ctx, args)
	case "launch", "start":
		err = cmdLaunch(ctx, args)
	case "call":
		err = cmdCall(ctx, args)
	case "tools":
		err = cmdTools(args)
	case "status":
		err = cmdStatus(ctx, args)
	case "stop":
		err = cmdStop(ctx, args)
	case "browsers":
		res := tools.Call(ctx, nil, "browsers_installed", nil)
		fmt.Print(res.Text())
	case "run", "test":
		err = cmdRun(ctx, args)
	case "setup":
		err = cmdSetup(args)
	case "skill":
		err = cmdSkill(args)
	case "version", "--version", "-v":
		fmt.Println("agent-in-browser", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		var exit exitCode
		if errors.As(err, &exit) {
			os.Exit(int(exit))
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// --- flags --------------------------------------------------------------------

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func sessionFlags(flags *flag.FlagSet) func() (session.Config, error) {
	var cfg session.Config
	var viewport string
	flags.StringVar(&cfg.Browser, "browser", envOr("AIB_BROWSER", "auto"), "browser to use")
	flags.BoolVar(&cfg.Headless, "headless", os.Getenv("AIB_HEADLESS") == "1" || os.Getenv("AIB_HEADLESS") == "true", "run headless")
	flags.StringVar(&cfg.Instance, "instance", envOr("AIB_INSTANCE", "default"), "named browser instance")
	flags.StringVar(&cfg.Endpoint, "endpoint", os.Getenv("AIB_ENDPOINT"), "attach to a running browser")
	flags.StringVar(&cfg.Protocol, "protocol", "", "protocol for --endpoint: cdp or bidi (guessed)")
	flags.StringVar(&cfg.Exe, "exe", os.Getenv("AIB_EXE"), "browser executable")
	flags.StringVar(&cfg.UserDataDir, "profile", "", "profile directory")
	flags.StringVar(&viewport, "viewport", "1280x800", "viewport WxH")
	return func() (session.Config, error) {
		if _, err := fmt.Sscanf(viewport, "%dx%d", &cfg.Width, &cfg.Height); err != nil {
			return cfg, fmt.Errorf("bad --viewport %q, want WxH", viewport)
		}
		return cfg, nil
	}
}

// --- commands -------------------------------------------------------------------

func cmdMCP(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("mcp", flag.ExitOnError)
	get := sessionFlags(flags)
	closeOnExit := flags.Bool("close-on-exit", false, "close the browser when the MCP client disconnects")
	_ = flags.Parse(args)
	cfg, err := get()
	if err != nil {
		return err
	}
	cfg.CloseOnExit = *closeOnExit
	sess := session.New(cfg)
	defer sess.Close()
	logger := log.New(os.Stderr, "[agent-in-browser] ", log.LstdFlags)
	logger.Printf("MCP server %s ready on stdio (browser=%s instance=%s)", version, cfg.Browser, cfg.Instance)
	srv := &mcp.Server{Name: "agent-in-browser", Version: version, Session: sess, Logger: logger}
	return srv.Serve(ctx, os.Stdin, os.Stdout)
}

func cmdLaunch(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("launch", flag.ExitOnError)
	get := sessionFlags(flags)
	url := flags.String("url", "", "open this URL")
	_ = flags.Parse(args)
	cfg, err := get()
	if err != nil {
		return err
	}
	sess := session.New(cfg)
	defer sess.Close()
	if *url != "" {
		if res := tools.Call(ctx, sess, "navigate", map[string]any{"url": *url}); res.IsError {
			return errors.New(res.Text())
		}
	}
	return printResult(tools.Call(ctx, sess, "browser_status", nil), "")
}

func cmdStatus(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("status", flag.ExitOnError)
	get := sessionFlags(flags)
	_ = flags.Parse(args)
	cfg, _ := get()
	in, err := browser.LoadInstance(cfg.Instance)
	if err != nil {
		return err
	}
	if in == nil || !in.Reachable() {
		fmt.Printf("instance %q is not running\n", cfg.Instance)
		return exitCode(3)
	}
	data, _ := json.MarshalIndent(in, "", "  ")
	fmt.Println(string(data))
	return nil
}

func cmdStop(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("stop", flag.ExitOnError)
	get := sessionFlags(flags)
	_ = flags.Parse(args)
	cfg, _ := get()
	if err := session.New(cfg).Stop(ctx); err != nil {
		return err
	}
	fmt.Printf("instance %q stopped\n", cfg.Instance)
	return nil
}

// parseInterleaved lets flags appear after positional arguments
// (agent-in-browser call snapshot --headless).
func parseInterleaved(flags *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		_ = flags.Parse(args)
		args = flags.Args()
		if len(args) == 0 {
			return pos
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func cmdCall(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("call", flag.ExitOnError)
	get := sessionFlags(flags)
	out := flags.String("out", "", "where to save images (default: ~/.agent-in-browser/screenshots/<time>.png)")
	asJSON := flags.Bool("json", false, "print the raw MCP result as JSON")
	timeout := flags.Duration("timeout", 60*time.Second, "overall timeout")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: agent-in-browser call [flags] <tool> [JSON | key=value ...]\n\nexamples:\n  agent-in-browser call navigate url=example.com\n  agent-in-browser call snapshot\n  agent-in-browser call click ref=e3\n  agent-in-browser call fill_form '{\"fields\":[{\"ref\":\"e4\",\"value\":\"hi\"}]}'\n\nflags:")
		flags.PrintDefaults()
	}
	pos := parseInterleaved(flags, args)
	if len(pos) < 1 {
		flags.Usage()
		return exitCode(2)
	}
	cfg, err := get()
	if err != nil {
		return err
	}
	name := pos[0]
	targs, err := parseToolArgs(pos[1:])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	sess := session.New(cfg)
	defer sess.Close()
	res := tools.Call(ctx, sess, name, targs)
	if *asJSON {
		data, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(data))
	} else if err := printResult(res, *out); err != nil {
		return err
	}
	if res.IsError {
		return exitCode(1)
	}
	return nil
}

func parseToolArgs(args []string) (map[string]any, error) {
	out := map[string]any{}
	if len(args) == 1 && strings.HasPrefix(strings.TrimSpace(args[0]), "{") {
		if err := json.Unmarshal([]byte(args[0]), &out); err != nil {
			return nil, fmt.Errorf("arguments JSON: %w", err)
		}
		return out, nil
	}
	for _, kv := range args {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("argument %q: want key=value or a JSON object", kv)
		}
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			switch parsed.(type) {
			case float64, bool, []any:
				out[k] = parsed
				continue
			}
		}
		out[k] = v
	}
	return out, nil
}

func printResult(res *tools.Result, out string) error {
	if t := res.Text(); t != "" {
		fmt.Println(t)
	}
	for i, img := range res.Images() {
		p := out
		if p == "" {
			p = filepath.Join(browser.Home(), "screenshots", time.Now().Format("20060102-150405.000")+".png")
		} else if i > 0 {
			p = strings.TrimSuffix(p, ".png") + fmt.Sprintf("-%d.png", i)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, img, 0o644); err != nil {
			return err
		}
		fmt.Println("image:", p)
	}
	return nil
}

func cmdTools(args []string) error {
	flags := flag.NewFlagSet("tools", flag.ExitOnError)
	asJSON := flags.Bool("json", false, "print tool definitions as JSON")
	md := flags.Bool("markdown", false, "print Markdown reference")
	_ = flags.Parse(args)
	all := tools.All()
	switch {
	case *asJSON:
		data, _ := json.MarshalIndent(all, "", "  ")
		fmt.Println(string(data))
	case *md:
		fmt.Print(toolsMarkdown(all))
	default:
		for _, t := range all {
			desc := t.Description
			if i := strings.Index(desc, ". "); i > 0 {
				desc = desc[:i+1]
			}
			fmt.Printf("%-18s %s\n", t.Name, desc)
		}
	}
	return nil
}

func toolsMarkdown(all []tools.Tool) string {
	var b strings.Builder
	b.WriteString("# Tool reference\n\n<!-- Generated by `agent-in-browser tools --markdown` (task docs:tools). Do not edit by hand. -->\n\n")
	b.WriteString("Every tool is available over MCP, via `agent-in-browser call <tool>`, and as a scenario step.\n\n")
	for _, t := range all {
		fmt.Fprintf(&b, "## `%s`\n\n%s\n\n", t.Name, t.Description)
		propsMap, _ := t.InputSchema["properties"].(map[string]any)
		if len(propsMap) == 0 {
			b.WriteString("_No arguments._\n\n")
			continue
		}
		req := map[string]bool{}
		if r, ok := t.InputSchema["required"].([]string); ok {
			for _, n := range r {
				req[n] = true
			}
		}
		b.WriteString("| Argument | Type | Required | Description |\n|---|---|---|---|\n")
		names := make([]string, 0, len(propsMap))
		for n := range propsMap {
			names = append(names, n)
		}
		sortStrings(names)
		for _, n := range names {
			p, _ := propsMap[n].(map[string]any)
			typ := fmt.Sprint(p["type"])
			if e, ok := p["enum"].([]string); ok {
				typ = strings.Join(e, " \\| ")
			}
			r := ""
			if req[n] {
				r = "yes"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", n, typ, r, p["description"])
		}
		b.WriteString("\n")
	}
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func cmdRun(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	browsers := flags.String("browser", os.Getenv("AIB_BROWSER"), "browser(s), comma-separated, e.g. chrome,firefox (default: scenario's browsers, else auto)")
	exe := flags.String("exe", "", "browser executable")
	headed := flags.Bool("headed", false, "show the browser window (default headless)")
	junit := flags.String("junit", "", "write JUnit XML report")
	jsonOut := flags.String("json", "", "write JSON report")
	artifacts := flags.String("artifacts", "aib-artifacts", "directory for failure screenshots ('' to disable)")
	files := parseInterleaved(flags, args)
	if len(files) == 0 {
		return errors.New("usage: agent-in-browser run [--browser chrome,firefox] [--junit out.xml] scenario.yaml...")
	}
	var reports []*scenario.Report
	failed := 0
	for _, file := range files {
		sc, err := scenario.Load(file)
		if err != nil {
			return err
		}
		matrix := splitList(*browsers)
		if len(matrix) == 0 {
			matrix = sc.Browsers
		}
		if len(matrix) == 0 {
			matrix = []string{"auto"}
		}
		for _, b := range matrix {
			fmt.Printf("▶ %s [%s]\n", sc.Name, b)
			rep := scenario.Run(ctx, sc, scenario.Options{
				Browser: b, Exe: *exe, Headless: !*headed, Artifacts: *artifacts,
				Verbose: func(line string) { fmt.Println(line) },
			})
			status := "PASS"
			if !rep.OK {
				status = "FAIL"
				failed++
			}
			fmt.Printf("%s %s [%s] in %s\n", status, sc.Name, b, rep.Duration.Round(time.Millisecond))
			if rep.Screenshot != "" {
				fmt.Println("  screenshot:", rep.Screenshot)
			}
			fmt.Println()
			reports = append(reports, rep)
		}
	}
	if *junit != "" {
		if err := scenario.WriteJUnit(*junit, reports); err != nil {
			return err
		}
	}
	if *jsonOut != "" {
		if err := scenario.WriteJSON(*jsonOut, reports); err != nil {
			return err
		}
	}
	fmt.Printf("%d run(s), %d failed\n", len(reports), failed)
	if failed > 0 {
		return exitCode(1)
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "agent-in-browser"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func cmdSetup(args []string) error {
	flags := flag.NewFlagSet("setup", flag.ExitOnError)
	write := flags.Bool("write", false, "write the config (default: print what to add)")
	bin := flags.String("bin", selfPath(), "binary path to register")
	extra := flags.String("args", "", "extra mcp flags, e.g. \"--browser firefox --headless\"")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: agent-in-browser setup <client> [--write] [--args \"--browser firefox\"]\n\nclients:")
		for _, c := range setup.Clients() {
			fmt.Fprintf(os.Stderr, "  %-15s %s\n", c.Name, c.Title)
		}
		flags.PrintDefaults()
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		flags.Usage()
		return exitCode(2)
	}
	name := args[0]
	_ = flags.Parse(args[1:])
	c, err := setup.Find(name)
	if err != nil {
		return err
	}
	mcpArgs := append([]string{"mcp"}, strings.Fields(*extra)...)
	if !*write {
		fmt.Printf("# %s — add this (or re-run with --write):\n\n%s\n", c.Title, c.Snippet(*bin, mcpArgs))
		return nil
	}
	msg, err := setup.Install(c, *bin, mcpArgs)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func cmdSkill(args []string) error {
	flags := flag.NewFlagSet("skill", flag.ExitOnError)
	dir := flags.String("dir", "", "install into this skills directory")
	target := flags.String("for", "claude", "agent: claude, codex, agents (cross-agent ~/.agents/skills)")
	if len(args) == 0 || (args[0] != "install" && args[0] != "print") {
		return errors.New("usage: agent-in-browser skill install [--for claude|codex|agents] [--dir DIR] | skill print")
	}
	_ = flags.Parse(args[1:])
	sub, err := fs.Sub(agentinbrowser.Skills, "skills")
	if err != nil {
		return err
	}
	if args[0] == "print" {
		data, err := fs.ReadFile(sub, "agent-in-browser/SKILL.md")
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil
	}
	d := *dir
	if d == "" {
		var ok bool
		if d, ok = setup.SkillDirs()[*target]; !ok {
			return fmt.Errorf("unknown --for %q", *target)
		}
	}
	dest, err := setup.InstallSkill(sub, "agent-in-browser", d)
	if err != nil {
		return err
	}
	fmt.Println("installed skill to", dest)
	return nil
}
