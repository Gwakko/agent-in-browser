package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Engine is the rendering engine family; it decides how a browser is launched.
type Engine string

const (
	Chromium Engine = "chromium"
	Gecko    Engine = "gecko"
	WebKit   Engine = "webkit"
)

// Kind is a browser product agent-in-browser knows how to find.
type Kind struct {
	Name     string
	Title    string
	Engine   Engine
	Protocol Protocol
	// Supported is false for browsers we can detect but not drive yet.
	Supported bool
	paths     map[string][]string // GOOS → candidate paths (env vars expanded)
	bins      []string            // names looked up in PATH
}

// Found is an installed browser.
type Found struct {
	Kind Kind
	Path string
}

// Kinds returns known browsers in auto-selection preference order.
func Kinds() []Kind {
	mac := func(app, bin string) string { return "/Applications/" + app + ".app/Contents/MacOS/" + bin }
	return []Kind{
		{Name: "chrome", Title: "Google Chrome", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Google Chrome", "Google Chrome")},
				"windows": {`${ProgramFiles}\Google\Chrome\Application\chrome.exe`, `${ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe`, `${LocalAppData}\Google\Chrome\Application\chrome.exe`},
			},
			bins: []string{"google-chrome", "google-chrome-stable"}},
		{Name: "chrome-beta", Title: "Google Chrome Beta", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{"darwin": {mac("Google Chrome Beta", "Google Chrome Beta")}},
			bins:  []string{"google-chrome-beta"}},
		{Name: "chrome-canary", Title: "Google Chrome Canary", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Google Chrome Canary", "Google Chrome Canary")},
				"windows": {`${LocalAppData}\Google\Chrome SxS\Application\chrome.exe`},
			},
			bins: []string{"google-chrome-unstable"}},
		{Name: "chromium", Title: "Chromium", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{"darwin": {mac("Chromium", "Chromium")}},
			bins:  []string{"chromium", "chromium-browser"}},
		{Name: "edge", Title: "Microsoft Edge", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Microsoft Edge", "Microsoft Edge")},
				"windows": {`${ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe`, `${ProgramFiles}\Microsoft\Edge\Application\msedge.exe`},
			},
			bins: []string{"microsoft-edge", "microsoft-edge-stable"}},
		{Name: "brave", Title: "Brave", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Brave Browser", "Brave Browser")},
				"windows": {`${ProgramFiles}\BraveSoftware\Brave-Browser\Application\brave.exe`, `${LocalAppData}\BraveSoftware\Brave-Browser\Application\brave.exe`},
			},
			bins: []string{"brave-browser", "brave"}},
		{Name: "vivaldi", Title: "Vivaldi", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Vivaldi", "Vivaldi")},
				"windows": {`${LocalAppData}\Vivaldi\Application\vivaldi.exe`},
			},
			bins: []string{"vivaldi", "vivaldi-stable"}},
		{Name: "opera", Title: "Opera", Engine: Chromium, Protocol: CDP, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Opera", "Opera")},
				"windows": {`${LocalAppData}\Programs\Opera\opera.exe`},
			},
			bins: []string{"opera"}},
		{Name: "firefox", Title: "Mozilla Firefox", Engine: Gecko, Protocol: BiDi, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Firefox", "firefox")},
				"windows": {`${ProgramFiles}\Mozilla Firefox\firefox.exe`, `${ProgramFiles(x86)}\Mozilla Firefox\firefox.exe`},
			},
			bins: []string{"firefox", "firefox-esr"}},
		{Name: "firefox-dev", Title: "Firefox Developer Edition", Engine: Gecko, Protocol: BiDi, Supported: true,
			paths: map[string][]string{
				"darwin":  {mac("Firefox Developer Edition", "firefox")},
				"windows": {`${ProgramFiles}\Firefox Developer Edition\firefox.exe`},
			},
			bins: []string{"firefox-developer-edition"}},
		{Name: "firefox-nightly", Title: "Firefox Nightly", Engine: Gecko, Protocol: BiDi, Supported: true,
			paths: map[string][]string{"darwin": {mac("Firefox Nightly", "firefox")}},
			bins:  []string{"firefox-nightly"}},
		{Name: "safari", Title: "Safari (via safaridriver)", Engine: WebKit, Protocol: WebDriver, Supported: false,
			paths: map[string][]string{"darwin": {"/usr/bin/safaridriver"}}},
	}
}

// Installed lists browsers present on this machine.
func Installed() []Found {
	var out []Found
	for _, k := range Kinds() {
		if p := locate(k); p != "" {
			out = append(out, Found{Kind: k, Path: p})
		}
	}
	return out
}

// Find resolves a browser by name ("" or "auto" picks the first supported
// installed browser). If exe is set it overrides discovery.
func Find(name, exe string) (Found, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "auto" {
		for _, f := range Installed() {
			if f.Kind.Supported {
				if exe != "" {
					f.Path = exe
				}
				return f, nil
			}
		}
		return Found{}, fmt.Errorf("no supported browser found; install Chrome/Chromium/Edge/Brave/Firefox or pass --exe")
	}
	for _, k := range Kinds() {
		if k.Name != name {
			continue
		}
		if !k.Supported {
			return Found{}, fmt.Errorf("%s is detected but not drivable yet (protocol %s is on the roadmap)", k.Title, k.Protocol)
		}
		p := exe
		if p == "" {
			p = locate(k)
		}
		if p == "" {
			return Found{}, fmt.Errorf("%s not found; install it or pass --exe /path/to/binary", k.Title)
		}
		return Found{Kind: k, Path: p}, nil
	}
	return Found{}, fmt.Errorf("unknown browser %q (known: %s)", name, strings.Join(Names(), ", "))
}

// Names lists known browser names.
func Names() []string {
	var n []string
	for _, k := range Kinds() {
		n = append(n, k.Name)
	}
	return n
}

func locate(k Kind) string {
	for _, p := range k.paths[runtime.GOOS] {
		p = expandWinEnv(p)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, b := range k.bins {
		if p, err := exec.LookPath(b); err == nil {
			return p
		}
	}
	return ""
}

func expandWinEnv(p string) string {
	if !strings.Contains(p, "${") {
		return p
	}
	return filepath.Clean(os.Expand(p, func(v string) string {
		if val := os.Getenv(v); val != "" {
			return val
		}
		return os.Getenv(strings.ToUpper(v))
	}))
}
