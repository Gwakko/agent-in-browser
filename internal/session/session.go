// Package session owns the connection to one browser instance: it attaches to
// a running instance from saved state, launches one when needed, and tracks
// the current tab. It is shared by the MCP server, the CLI and the scenario
// runner.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gwakko/agent-in-browser/internal/bidi"
	"github.com/gwakko/agent-in-browser/internal/browser"
	"github.com/gwakko/agent-in-browser/internal/cdp"
	"github.com/gwakko/agent-in-browser/internal/page"
)

// Config selects and configures the browser.
type Config struct {
	Instance    string // saved-state name, default "default"
	Browser     string // auto | chrome | edge | brave | firefox | ...
	Exe         string // explicit browser binary
	Endpoint    string // attach to an already running browser instead of launching
	Protocol    string // protocol for Endpoint: cdp | bidi (guessed when empty)
	Headless    bool
	UserDataDir string
	Width       int
	Height      int
	ExtraArgs   []string
	// Ephemeral launches a fresh browser on a temporary profile that is
	// closed and deleted on Close (used by the scenario runner).
	Ephemeral bool
	// CloseOnExit closes a browser we launched when the session closes.
	CloseOnExit bool
}

// Session is safe for concurrent use; calls are serialized.
type Session struct {
	cfg Config

	mu       sync.Mutex
	drv      browser.Driver
	inst     *browser.Instance
	launched *browser.Launched
	current  string
	tmpDir   string
}

func New(cfg Config) *Session {
	if cfg.Instance == "" {
		cfg.Instance = "default"
	}
	if cfg.Width == 0 || cfg.Height == 0 {
		cfg.Width, cfg.Height = 1280, 800
	}
	return &Session{cfg: cfg}
}

func (s *Session) Config() Config { return s.cfg }

// Driver returns a connected driver, attaching or launching on first use.
func (s *Session) Driver(ctx context.Context) (browser.Driver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensure(ctx)
}

func (s *Session) ensure(ctx context.Context) (browser.Driver, error) {
	if s.drv != nil {
		return s.drv, nil
	}
	switch {
	case s.cfg.Endpoint != "":
		proto := browser.Protocol(s.cfg.Protocol)
		if proto == "" {
			proto = guessProtocol(s.cfg.Endpoint)
		}
		drv, err := connect(ctx, proto, s.cfg.Endpoint)
		if err != nil {
			return nil, err
		}
		s.drv = drv
		s.inst = &browser.Instance{Name: s.cfg.Instance, Browser: "external", Protocol: proto, Endpoint: s.cfg.Endpoint, StartedAt: time.Now()}
		return drv, nil
	case s.cfg.Ephemeral:
		dir, err := os.MkdirTemp("", "aib-profile-*")
		if err != nil {
			return nil, err
		}
		s.tmpDir = dir
		return s.launch(ctx, filepath.Join(dir, "profile"), false)
	}

	// Reuse the persisted instance if it is still alive and matches.
	if in, _ := browser.LoadInstance(s.cfg.Instance); in != nil {
		reachable := in.Reachable()
		switch {
		case reachable && s.matches(in):
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			drv, err := connect(cctx, in.Protocol, in.Endpoint)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("instance %q (%s) is running but not answering: %w; try: agent-in-browser stop --instance %s", s.cfg.Instance, in.Browser, err, s.cfg.Instance)
			}
			s.drv, s.inst, s.current = drv, in, in.CurrentTab
			return drv, nil
		case reachable:
			return nil, fmt.Errorf("instance %q is running %s; stop it first (agent-in-browser stop --instance %s) or use another --instance", s.cfg.Instance, in.Browser, s.cfg.Instance)
		default:
			_ = browser.DeleteInstance(s.cfg.Instance) // stale
		}
	}
	dir := s.cfg.UserDataDir
	if dir == "" {
		found, err := browser.Find(s.cfg.Browser, s.cfg.Exe)
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(browser.Home(), "profiles", s.cfg.Instance+"-"+found.Kind.Name)
	}
	return s.launch(ctx, dir, true)
}

func (s *Session) matches(in *browser.Instance) bool {
	b := strings.ToLower(s.cfg.Browser)
	return b == "" || b == "auto" || b == in.Browser
}

func (s *Session) launch(ctx context.Context, dir string, persist bool) (browser.Driver, error) {
	found, err := browser.Find(s.cfg.Browser, s.cfg.Exe)
	if err != nil {
		return nil, err
	}
	l, err := browser.Launch(ctx, browser.LaunchOptions{
		Browser: found, UserDataDir: dir, Headless: s.cfg.Headless,
		Width: s.cfg.Width, Height: s.cfg.Height, ExtraArgs: s.cfg.ExtraArgs,
		Detach: persist && !s.cfg.CloseOnExit,
	})
	if err != nil {
		return nil, err
	}
	var drv browser.Driver
	for attempt := 0; attempt < 20; attempt++ {
		drv, err = connect(ctx, l.Protocol, l.Endpoint)
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		_ = l.Process.Kill()
		return nil, err
	}
	s.drv, s.launched = drv, l
	s.inst = &browser.Instance{
		Name: s.cfg.Instance, Browser: found.Kind.Name, Protocol: l.Protocol, Endpoint: l.Endpoint,
		PID: l.PID, UserDataDir: dir, Headless: s.cfg.Headless, Owned: true, StartedAt: time.Now(),
	}
	if persist {
		if err := browser.SaveInstance(s.inst); err != nil {
			return nil, err
		}
	}
	return drv, nil
}

func connect(ctx context.Context, proto browser.Protocol, endpoint string) (browser.Driver, error) {
	switch proto {
	case browser.BiDi:
		return bidi.Connect(ctx, endpoint)
	case browser.CDP, "":
		return cdp.Connect(ctx, endpoint)
	}
	return nil, fmt.Errorf("protocol %q is not supported", proto)
}

func guessProtocol(endpoint string) browser.Protocol {
	if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/session") {
		return browser.BiDi
	}
	return browser.CDP
}

// Tab returns the tab to operate on: explicit if given, else the current tab,
// else the first open tab, else a new one.
func (s *Session) Tab(ctx context.Context, explicit string) (browser.Driver, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	drv, err := s.ensure(ctx)
	if err != nil {
		return nil, "", err
	}
	if explicit != "" {
		return drv, explicit, nil
	}
	tabs, err := drv.Tabs(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, t := range tabs {
		if t.ID == s.current {
			return drv, s.current, nil
		}
	}
	if len(tabs) > 0 {
		s.setCurrentLocked(tabs[0].ID)
		return drv, s.current, nil
	}
	id, err := drv.NewTab(ctx, "about:blank")
	if err != nil {
		return nil, "", err
	}
	s.setCurrentLocked(id)
	return drv, id, nil
}

// Current returns the current tab id without validating it.
func (s *Session) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// SetCurrent makes tab the default target for subsequent calls.
func (s *Session) SetCurrent(tab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCurrentLocked(tab)
}

func (s *Session) setCurrentLocked(tab string) {
	s.current = tab
	if s.inst != nil && !s.cfg.Ephemeral && s.cfg.Endpoint == "" {
		s.inst.CurrentTab = tab
		_ = browser.SaveInstance(s.inst)
	}
}

// Instance describes the connected browser (nil before first use).
func (s *Session) Instance() *browser.Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inst
}

// Eval runs page.Call/page.Wrap-produced code and decodes its JSON result.
func (s *Session) Eval(ctx context.Context, drv browser.Driver, tab, expr string, out any) error {
	raw, err := drv.Evaluate(ctx, tab, expr)
	if err != nil {
		return cleanJSError(err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode page result: %w (%.200s)", err, raw)
	}
	return nil
}

// PageCall invokes a function of the injected page library.
func (s *Session) PageCall(ctx context.Context, drv browser.Driver, tab string, out any, fn string, args ...any) error {
	return s.Eval(ctx, drv, tab, page.Call(fn, args...), out)
}

func cleanJSError(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "Error: "); i >= 0 && strings.Contains(msg, "javascript error") {
		msg = msg[i+len("Error: "):]
		if j := strings.Index(msg, "\n    at "); j >= 0 {
			msg = msg[:j]
		}
		return errors.New(msg)
	}
	return err
}

// WaitReady polls document.readyState until the page has loaded. Errors
// during navigation (destroyed execution context) are retried.
func (s *Session) WaitReady(ctx context.Context, drv browser.Driver, tab string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	time.Sleep(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		state, err := drv.Evaluate(cctx, tab, "document.readyState")
		cancel()
		if err == nil && state == "complete" {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Stop closes the browser (if owned or forced) and forgets the instance.
func (s *Session) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.inst
	if in == nil {
		var err error
		if in, err = browser.LoadInstance(s.cfg.Instance); err != nil || in == nil {
			return fmt.Errorf("instance %q is not running", s.cfg.Instance)
		}
	}
	drv := s.drv
	if drv == nil && in.Reachable() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		drv, _ = connect(cctx, in.Protocol, in.Endpoint)
		cancel()
	}
	if drv != nil {
		_ = drv.CloseBrowser(ctx)
		_ = drv.Close()
	}
	if in.Owned {
		deadline := time.Now().Add(5 * time.Second)
		for in.Reachable() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if p, err := os.FindProcess(in.PID); err == nil && in.PID > 0 && browser.Alive(in.PID) {
			_ = p.Kill()
		}
	}
	s.drv, s.inst, s.current = nil, nil, ""
	return browser.DeleteInstance(s.cfg.Instance)
}

// Close disconnects; ephemeral or close-on-exit browsers are shut down.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.launched != nil && (s.cfg.Ephemeral || s.cfg.CloseOnExit) {
		_ = s.drv.CloseBrowser(ctx)
		_ = s.drv.Close()
		deadline := time.Now().Add(3 * time.Second)
		for s.inst.Reachable() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if browser.Alive(s.launched.PID) {
			_ = s.launched.Process.Kill()
		}
		if !s.cfg.Ephemeral {
			_ = browser.DeleteInstance(s.cfg.Instance)
		}
	} else {
		_ = s.drv.Close()
	}
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
	s.drv = nil
	return nil
}
