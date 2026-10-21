// Package scenario runs declarative browser test scenarios (YAML/JSON). Each
// step is a tool call from the tools package or an assertion, so anything an
// agent can do interactively can be replayed as an automated test.
package scenario

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gwakko/agent-in-browser/internal/page"
	"github.com/gwakko/agent-in-browser/internal/session"
	"github.com/gwakko/agent-in-browser/internal/tools"
)

// Scenario is a test file.
type Scenario struct {
	Name     string   `yaml:"name"`
	Browsers []string `yaml:"browsers"` // default matrix; CLI --browser overrides
	Headless *bool    `yaml:"headless"`
	Viewport *struct {
		Width  int `yaml:"width"`
		Height int `yaml:"height"`
	} `yaml:"viewport"`
	Vars    map[string]string `yaml:"vars"`
	Timeout string            `yaml:"timeout"` // per step, default 30s
	Steps   []Step            `yaml:"steps"`
	File    string            `yaml:"-"`
}

// Step is one action: {<tool|assert|sleep>: args, name?: "...", optional?: bool}.
type Step struct {
	Name     string
	Action   string
	Args     map[string]any
	Optional bool
}

func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	var raw map[string]any
	if err := n.Decode(&raw); err != nil {
		return err
	}
	if v, ok := raw["name"].(string); ok {
		s.Name = v
		delete(raw, "name")
	}
	if v, ok := raw["optional"].(bool); ok {
		s.Optional = v
		delete(raw, "optional")
	}
	if len(raw) != 1 {
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("line %d: a step needs exactly one action, got %v", n.Line, keys)
	}
	for k, v := range raw {
		s.Action = k
		switch a := v.(type) {
		case map[string]any:
			s.Args = a
		case nil:
			s.Args = map[string]any{}
		case string: // shorthand: `- navigate: https://example.com`
			s.Args = map[string]any{shorthandKey(k): a}
		default:
			s.Args = map[string]any{shorthandKey(k): a}
		}
	}
	if s.Action != "assert" && s.Action != "sleep" {
		if _, ok := tools.Get(s.Action); !ok {
			return fmt.Errorf("line %d: unknown action %q", n.Line, s.Action)
		}
	}
	return nil
}

func shorthandKey(action string) string {
	switch action {
	case "navigate", "tab_new":
		return "url"
	case "click", "hover":
		return "match"
	case "press_key":
		return "keys"
	case "evaluate":
		return "code"
	case "wait_for":
		return "text"
	case "sleep":
		return "ms"
	case "screenshot":
		return "path"
	case "find":
		return "query"
	}
	return "value"
}

// Load parses a scenario file.
func Load(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(sc.Steps) == 0 {
		return nil, fmt.Errorf("%s: no steps", path)
	}
	if sc.Name == "" {
		sc.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	sc.File = path
	return &sc, nil
}

// StepResult is the outcome of one step.
type StepResult struct {
	Index    int           `json:"index"`
	Name     string        `json:"name"`
	Action   string        `json:"action"`
	OK       bool          `json:"ok"`
	Skipped  bool          `json:"skipped,omitempty"`
	Error    string        `json:"error,omitempty"`
	Output   string        `json:"output,omitempty"`
	Duration time.Duration `json:"durationNs"`
}

// Report is the outcome of one scenario on one browser.
type Report struct {
	Scenario   string        `json:"scenario"`
	File       string        `json:"file"`
	Browser    string        `json:"browser"`
	OK         bool          `json:"ok"`
	Error      string        `json:"error,omitempty"`
	Steps      []StepResult  `json:"steps"`
	Duration   time.Duration `json:"durationNs"`
	Screenshot string        `json:"failureScreenshot,omitempty"`
}

// Options control a run.
type Options struct {
	Browser   string
	Exe       string
	Headless  bool
	Artifacts string // directory for failure screenshots
	Verbose   func(string)
}

// Run executes a scenario in a fresh, isolated browser.
func Run(ctx context.Context, sc *Scenario, o Options) *Report {
	start := time.Now()
	rep := &Report{Scenario: sc.Name, File: sc.File, Browser: o.Browser}
	cfg := session.Config{Browser: o.Browser, Exe: o.Exe, Headless: o.Headless, Ephemeral: true}
	if sc.Headless != nil {
		cfg.Headless = cfg.Headless || *sc.Headless
	}
	if sc.Viewport != nil {
		cfg.Width, cfg.Height = sc.Viewport.Width, sc.Viewport.Height
	}
	sess := session.New(cfg)
	defer sess.Close()

	stepTimeout := 30 * time.Second
	if d, err := time.ParseDuration(sc.Timeout); err == nil && d > 0 {
		stepTimeout = d
	}
	vars := map[string]string{"browser": o.Browser}
	if abs, err := filepath.Abs(filepath.Dir(sc.File)); err == nil {
		vars["dir"] = filepath.ToSlash(abs)
	}
	for k, v := range sc.Vars {
		vars[k] = v
	}
	for range 3 { // vars may reference other vars and ${env.X}
		for k, v := range vars {
			vars[k] = expand(v, vars).(string)
		}
	}

	rep.OK = true
	for i, st := range sc.Steps {
		res := StepResult{Index: i + 1, Name: st.Name, Action: st.Action}
		if !rep.OK {
			res.Skipped = true
			rep.Steps = append(rep.Steps, res)
			continue
		}
		args := expand(st.Args, vars).(map[string]any)
		t0 := time.Now()
		sctx, cancel := context.WithTimeout(ctx, stepTimeout)
		out, err := runStep(sctx, sess, st.Action, args)
		cancel()
		res.Duration = time.Since(t0)
		res.Output = out
		if err != nil {
			res.Error = err.Error()
			if st.Optional {
				res.OK = true
			} else {
				rep.OK = false
				rep.Error = fmt.Sprintf("step %d (%s): %s", i+1, label(st), err)
				rep.Screenshot = failureShot(ctx, sess, o.Artifacts, sc.Name, o.Browser)
			}
		} else {
			res.OK = true
		}
		if o.Verbose != nil {
			o.Verbose(formatStep(res, st))
		}
		rep.Steps = append(rep.Steps, res)
	}
	rep.Duration = time.Since(start)
	return rep
}

func runStep(ctx context.Context, s *session.Session, action string, args map[string]any) (string, error) {
	switch action {
	case "sleep":
		ms := tools.Args(args).Int("ms", 1000)
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	case "assert":
		return "", assert(ctx, s, tools.Args(args))
	}
	res := tools.Call(ctx, s, action, args)
	text := res.Text()
	if res.IsError {
		return "", fmt.Errorf("%s", strings.TrimPrefix(text, "Error: "))
	}
	if len(text) > 2000 {
		text = text[:2000] + "…"
	}
	return text, nil
}

// assert supports: url_contains, url_matches, title_contains, text_contains,
// text_not_contains, selector (+ text, count), js, no_console_errors.
func assert(ctx context.Context, s *session.Session, a tools.Args) error {
	drv, tab, err := s.Tab(ctx, a.Str("tab"))
	if err != nil {
		return err
	}
	var st struct {
		URL, Title, Text string
	}
	if err := s.Eval(ctx, drv, tab, page.Wrap("return {url: location.href, title: document.title, text: document.body ? document.body.innerText : ''};"), &st); err != nil {
		return err
	}
	checked := 0
	if v := a.Str("url_contains"); v != "" {
		checked++
		if !strings.Contains(st.URL, v) {
			return fmt.Errorf("url %q does not contain %q", st.URL, v)
		}
	}
	if v := a.Str("url_matches"); v != "" {
		checked++
		re, err := regexp.Compile(v)
		if err != nil {
			return err
		}
		if !re.MatchString(st.URL) {
			return fmt.Errorf("url %q does not match /%s/", st.URL, v)
		}
	}
	if v := a.Str("title_contains"); v != "" {
		checked++
		if !strings.Contains(st.Title, v) {
			return fmt.Errorf("title %q does not contain %q", st.Title, v)
		}
	}
	if v := a.Str("text_contains"); v != "" {
		checked++
		if !strings.Contains(st.Text, v) {
			return fmt.Errorf("page text does not contain %q", v)
		}
	}
	if v := a.Str("text_not_contains"); v != "" {
		checked++
		if strings.Contains(st.Text, v) {
			return fmt.Errorf("page text unexpectedly contains %q", v)
		}
	}
	if sel := a.Str("selector"); sel != "" {
		checked++
		var r struct {
			Count int
			Text  string
		}
		js := fmt.Sprintf("const els = document.querySelectorAll(%q); return {count: els.length, text: els[0] ? els[0].innerText || els[0].value || '' : ''};", sel)
		if err := s.Eval(ctx, drv, tab, page.Wrap(js), &r); err != nil {
			return err
		}
		if want := a.Int("count", -1); want >= 0 {
			if r.Count != want {
				return fmt.Errorf("selector %s: %d matches, want %d", sel, r.Count, want)
			}
		} else if r.Count == 0 {
			return fmt.Errorf("selector %s: no match", sel)
		}
		if t := a.Str("text"); t != "" && !strings.Contains(r.Text, t) {
			return fmt.Errorf("selector %s: text %q does not contain %q", sel, r.Text, t)
		}
	}
	if js := a.Str("js"); js != "" {
		checked++
		var ok any
		if err := s.Eval(ctx, drv, tab, page.UserCode(js), &ok); err != nil {
			return err
		}
		if !truthy(ok) {
			return fmt.Errorf("js assertion is falsy: %s (got %v)", js, ok)
		}
	}
	if a.Bool("no_console_errors", false) {
		checked++
		for _, e := range drv.Console(tab) {
			if e.Level == "error" {
				return fmt.Errorf("console error: %s", e.Text)
			}
		}
	}
	if checked == 0 {
		return fmt.Errorf("assert has no known checks (url_contains, url_matches, title_contains, text_contains, text_not_contains, selector, js, no_console_errors)")
	}
	return nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z0-9_.]+)\}`)

func expand(v any, vars map[string]string) any {
	switch x := v.(type) {
	case string:
		return varRe.ReplaceAllStringFunc(x, func(m string) string {
			k := varRe.FindStringSubmatch(m)[1]
			if strings.HasPrefix(k, "env.") {
				return os.Getenv(strings.TrimPrefix(k, "env."))
			}
			if val, ok := vars[k]; ok {
				return val
			}
			return m
		})
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = expand(val, vars)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = expand(val, vars)
		}
		return out
	}
	return v
}

func failureShot(ctx context.Context, s *session.Session, dir, name, browserName string) string {
	if dir == "" {
		return ""
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	slug := regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(name, "-")
	p := filepath.Join(dir, fmt.Sprintf("%s-%s-failure.png", strings.Trim(slug, "-"), browserName))
	res := tools.Call(sctx, s, "screenshot", map[string]any{"path": p})
	if res.IsError {
		return ""
	}
	return p
}

func label(st Step) string {
	if st.Name != "" {
		return st.Name
	}
	return st.Action
}

func formatStep(r StepResult, st Step) string {
	mark := "✓"
	if !r.OK {
		mark = "✗"
	} else if r.Error != "" {
		mark = "~"
	}
	line := fmt.Sprintf("  %s %2d %-40s %6dms", mark, r.Index, label(st), r.Duration.Milliseconds())
	if r.Error != "" {
		line += "\n      " + r.Error
	}
	return line
}
