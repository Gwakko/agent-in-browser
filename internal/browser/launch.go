package browser

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LaunchOptions configures a new browser process.
type LaunchOptions struct {
	Browser     Found
	UserDataDir string
	Headless    bool
	Width       int
	Height      int
	ExtraArgs   []string
	// Detach keeps the browser alive after this process exits.
	Detach bool
}

// Launched describes a started browser.
type Launched struct {
	Endpoint string
	Protocol Protocol
	PID      int
	Process  *os.Process
}

const startTimeout = 30 * time.Second

// Launch starts a browser with remote automation enabled on an isolated
// profile and returns its WebSocket endpoint.
//
// A dedicated profile is mandatory: since Chrome 136 the remote debugging
// switches are ignored for the user's default profile.
func Launch(ctx context.Context, o LaunchOptions) (*Launched, error) {
	if err := os.MkdirAll(o.UserDataDir, 0o700); err != nil {
		return nil, err
	}
	if o.Width == 0 {
		o.Width, o.Height = 1280, 800
	}
	switch o.Browser.Kind.Engine {
	case Chromium:
		return launchChromium(ctx, o)
	case Gecko:
		return launchFirefox(ctx, o)
	default:
		return nil, fmt.Errorf("launching %s is not supported yet", o.Browser.Kind.Title)
	}
}

func launchChromium(ctx context.Context, o LaunchOptions) (*Launched, error) {
	portFile := filepath.Join(o.UserDataDir, "DevToolsActivePort")
	_ = os.Remove(portFile)
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + o.UserDataDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-search-engine-choice-screen",
		"--disable-features=Translate,OptimizationHints,MediaRouter",
		"--disable-background-networking",
		"--disable-sync",
		fmt.Sprintf("--window-size=%d,%d", o.Width, o.Height),
	}
	if o.Headless {
		args = append(args, "--headless=new", "--hide-scrollbars", "--mute-audio")
	}
	args = append(args, o.ExtraArgs...)
	args = append(args, "about:blank")

	proc, err := start(o, args)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 2 {
				return &Launched{
					Endpoint: "ws://127.0.0.1:" + strings.TrimSpace(lines[0]) + strings.TrimSpace(lines[1]),
					Protocol: CDP, PID: proc.Pid, Process: proc,
				}, nil
			}
		}
		if err := sleepCtx(ctx, 100*time.Millisecond); err != nil {
			_ = proc.Kill()
			return nil, err
		}
	}
	_ = proc.Kill()
	return nil, fmt.Errorf("%s did not expose DevToolsActivePort within %s (see %s)", o.Browser.Kind.Title, startTimeout, logPath(o))
}

const firefoxPrefs = `user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.aboutwelcome.enabled", false);
user_pref("trailhead.firstrun.didSeeAboutWelcome", true);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
user_pref("browser.tabs.warnOnClose", false);
user_pref("remote.active-protocols", 1);
`

func launchFirefox(ctx context.Context, o LaunchOptions) (*Launched, error) {
	if err := os.WriteFile(filepath.Join(o.UserDataDir, "user.js"), []byte(firefoxPrefs), 0o600); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := []string{
		"--remote-debugging-port", strconv.Itoa(port),
		"--profile", o.UserDataDir,
		"--no-remote", "--new-instance",
		"--width", strconv.Itoa(o.Width), "--height", strconv.Itoa(o.Height),
	}
	if o.Headless {
		args = append(args, "--headless")
	}
	args = append(args, o.ExtraArgs...)
	args = append(args, "about:blank")

	proc, err := start(o, args)
	if err != nil {
		return nil, err
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return &Launched{Endpoint: "ws://" + addr + "/session", Protocol: BiDi, PID: proc.Pid, Process: proc}, nil
		}
		if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
			_ = proc.Kill()
			return nil, err
		}
	}
	_ = proc.Kill()
	return nil, fmt.Errorf("%s did not open the BiDi port within %s (see %s)", o.Browser.Kind.Title, startTimeout, logPath(o))
}

func logPath(o LaunchOptions) string { return o.UserDataDir + ".log" }

func start(o LaunchOptions, args []string) (*os.Process, error) {
	logf, err := os.OpenFile(logPath(o), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	cmd := exec.Command(o.Browser.Path, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	if o.Detach {
		detach(cmd)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", o.Browser.Path, err)
	}
	go func() { _ = cmd.Wait() }() // reap
	return cmd.Process, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
