// Package browser defines the protocol-neutral Driver contract, browser
// discovery, launching and the persisted instance state.
package browser

import (
	"context"
	"sync"
	"time"
)

// Protocol identifies the automation protocol a driver speaks.
type Protocol string

const (
	CDP       Protocol = "cdp"       // Chrome DevTools Protocol: Chrome, Edge, Brave, Chromium, Opera, Vivaldi
	BiDi      Protocol = "bidi"      // WebDriver BiDi: Firefox (and Chromium via chromedriver, later)
	WebDriver Protocol = "webdriver" // classic WebDriver: Safari (planned)
)

// Tab is a top-level browsing context.
type Tab struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Active bool   `json:"active,omitempty"`
}

// ConsoleEntry is a captured console message or uncaught exception.
type ConsoleEntry struct {
	Level string    `json:"level"`
	Text  string    `json:"text"`
	URL   string    `json:"url,omitempty"`
	Time  time.Time `json:"time"`
}

// NetworkEntry is a captured request with its outcome.
type NetworkEntry struct {
	ID       string    `json:"-"`
	Method   string    `json:"method"`
	URL      string    `json:"url"`
	Type     string    `json:"type,omitempty"`
	Status   int       `json:"status,omitempty"`
	MimeType string    `json:"mimeType,omitempty"`
	Failed   string    `json:"failed,omitempty"`
	Time     time.Time `json:"time"`
}

// Driver is everything the tool layer needs from a browser. Most page-level
// behaviour (snapshot, element lookup, form filling) is implemented in injected
// JavaScript via Evaluate, so a new backend only has to provide these
// primitives — trusted input, screenshots, tabs and event capture.
type Driver interface {
	Protocol() Protocol
	Tabs(ctx context.Context) ([]Tab, error)
	NewTab(ctx context.Context, url string) (string, error)
	CloseTab(ctx context.Context, tab string) error
	ActivateTab(ctx context.Context, tab string) error
	Navigate(ctx context.Context, tab, url string) error
	// Evaluate runs a JS expression (promises are awaited) and returns its
	// value; string results are returned verbatim, others JSON-encoded.
	Evaluate(ctx context.Context, tab, expression string) (string, error)
	Screenshot(ctx context.Context, tab string) ([]byte, error)
	Click(ctx context.Context, tab string, x, y float64, button string, count int) error
	Hover(ctx context.Context, tab string, x, y float64) error
	InsertText(ctx context.Context, tab, text string) error
	Press(ctx context.Context, tab string, combo Combo) error
	SetViewport(ctx context.Context, tab string, width, height int) error
	Console(tab string) []ConsoleEntry
	ClearConsole(tab string)
	Network(tab string) []NetworkEntry
	ClearNetwork(tab string)
	// CloseBrowser asks the browser process to exit.
	CloseBrowser(ctx context.Context) error
	// Close disconnects; the browser keeps running.
	Close() error
}

// Buffers is a bounded per-tab store for console and network events, shared
// by driver implementations.
type Buffers struct {
	mu      sync.Mutex
	console map[string][]ConsoleEntry
	network map[string][]NetworkEntry
}

const maxBuffered = 1000

func NewBuffers() *Buffers {
	return &Buffers{console: map[string][]ConsoleEntry{}, network: map[string][]NetworkEntry{}}
}

func (b *Buffers) AddConsole(tab string, e ConsoleEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.console[tab] = trim(append(b.console[tab], e))
}

// UpsertNetwork adds an entry or updates the one with the same ID.
func (b *Buffers) UpsertNetwork(tab string, e NetworkEntry, update func(*NetworkEntry)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.network[tab]
	for i := len(list) - 1; i >= 0 && e.ID != ""; i-- {
		if list[i].ID == e.ID {
			update(&list[i])
			return
		}
	}
	update(&e)
	b.network[tab] = trim(append(list, e))
}

func (b *Buffers) Console(tab string) []ConsoleEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ConsoleEntry(nil), b.console[tab]...)
}

func (b *Buffers) Network(tab string) []NetworkEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]NetworkEntry(nil), b.network[tab]...)
}

func (b *Buffers) ClearConsole(tab string) { b.mu.Lock(); delete(b.console, tab); b.mu.Unlock() }
func (b *Buffers) ClearNetwork(tab string) { b.mu.Lock(); delete(b.network, tab); b.mu.Unlock() }

func trim[T any](s []T) []T {
	if len(s) > maxBuffered {
		return s[len(s)-maxBuffered:]
	}
	return s
}
