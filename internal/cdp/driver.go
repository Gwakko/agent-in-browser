// Package cdp drives Chromium-family browsers (Chrome, Edge, Brave, Chromium,
// Opera, Vivaldi) over the Chrome DevTools Protocol using flattened sessions.
package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gwakko/agent-in-browser/internal/browser"
	"github.com/gwakko/agent-in-browser/internal/rpc"
)

// Driver implements browser.Driver over CDP.
type Driver struct {
	conn *rpc.Conn
	buf  *browser.Buffers

	mu        sync.Mutex
	sessions  map[string]string // targetID → sessionID
	bySession map[string]string // sessionID → targetID
}

var _ browser.Driver = (*Driver)(nil)

// Connect attaches to a browser. endpoint is either the browser WebSocket URL
// (ws://host:port/devtools/browser/<id>) or an HTTP debugging address
// (http://127.0.0.1:9222), which is resolved via /json/version.
func Connect(ctx context.Context, endpoint string) (*Driver, error) {
	ws, err := resolveEndpoint(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	d := &Driver{
		buf:       browser.NewBuffers(),
		sessions:  map[string]string{},
		bySession: map[string]string{},
	}
	d.conn, err = rpc.Dial(ctx, ws, d.onEvent)
	if err != nil {
		return nil, err
	}
	// Learn about tabs closed behind our back so stale sessions are dropped.
	if _, err := d.conn.Call(ctx, "Target.setDiscoverTargets", map[string]any{"discover": true}, ""); err != nil {
		d.conn.Close()
		return nil, err
	}
	return d, nil
}

func resolveEndpoint(ctx context.Context, endpoint string) (string, error) {
	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
		return endpoint, nil
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve CDP endpoint: %w", err)
	}
	defer resp.Body.Close()
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.WS == "" {
		return "", fmt.Errorf("resolve CDP endpoint: no webSocketDebuggerUrl at %s/json/version", endpoint)
	}
	return v.WS, nil
}

func (d *Driver) Protocol() browser.Protocol { return browser.CDP }

// --- events ---------------------------------------------------------------

func (d *Driver) onEvent(e rpc.Event) {
	if e.Method == "Target.targetDestroyed" || e.Method == "Target.detachedFromTarget" {
		var p struct {
			TargetID  string `json:"targetId"`
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(e.Params, &p)
		d.mu.Lock()
		if p.TargetID == "" {
			p.TargetID = d.bySession[p.SessionID]
		}
		if sid, ok := d.sessions[p.TargetID]; ok {
			delete(d.bySession, sid)
			delete(d.sessions, p.TargetID)
		}
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	tab := d.bySession[e.SessionID]
	d.mu.Unlock()
	if tab == "" {
		return
	}
	now := time.Now()
	switch e.Method {
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string      `json:"type"`
			Args []remoteObj `json:"args"`
		}
		_ = json.Unmarshal(e.Params, &p)
		parts := make([]string, 0, len(p.Args))
		for _, a := range p.Args {
			parts = append(parts, a.String())
		}
		level := p.Type
		if level == "warning" {
			level = "warn"
		}
		d.buf.AddConsole(tab, browser.ConsoleEntry{Level: level, Text: strings.Join(parts, " "), Time: now})
	case "Runtime.exceptionThrown":
		var p struct {
			Details struct {
				Text      string    `json:"text"`
				URL       string    `json:"url"`
				Exception remoteObj `json:"exception"`
			} `json:"exceptionDetails"`
		}
		_ = json.Unmarshal(e.Params, &p)
		text := p.Details.Exception.String()
		if text == "" {
			text = p.Details.Text
		}
		d.buf.AddConsole(tab, browser.ConsoleEntry{Level: "error", Text: text, URL: p.Details.URL, Time: now})
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level, Text, URL, Source string
			} `json:"entry"`
		}
		_ = json.Unmarshal(e.Params, &p)
		if p.Entry.Source == "console-api" {
			return // already reported by Runtime.consoleAPICalled
		}
		level := p.Entry.Level
		if level == "warning" {
			level = "warn"
		}
		d.buf.AddConsole(tab, browser.ConsoleEntry{Level: level, Text: p.Entry.Text, URL: p.Entry.URL, Time: now})
	case "Network.requestWillBeSent":
		var p struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Request   struct {
				URL, Method string
			} `json:"request"`
		}
		_ = json.Unmarshal(e.Params, &p)
		d.buf.UpsertNetwork(tab, browser.NetworkEntry{ID: p.RequestID}, func(n *browser.NetworkEntry) {
			n.Method, n.URL, n.Type, n.Time = p.Request.Method, p.Request.URL, p.Type, now
		})
	case "Network.responseReceived":
		var p struct {
			RequestID string `json:"requestId"`
			Response  struct {
				Status   int    `json:"status"`
				MimeType string `json:"mimeType"`
				URL      string `json:"url"`
			} `json:"response"`
		}
		_ = json.Unmarshal(e.Params, &p)
		d.buf.UpsertNetwork(tab, browser.NetworkEntry{ID: p.RequestID, URL: p.Response.URL, Time: now}, func(n *browser.NetworkEntry) {
			n.Status, n.MimeType = p.Response.Status, p.Response.MimeType
		})
	case "Network.loadingFailed":
		var p struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
			Canceled  bool   `json:"canceled"`
		}
		_ = json.Unmarshal(e.Params, &p)
		d.buf.UpsertNetwork(tab, browser.NetworkEntry{ID: p.RequestID, Time: now}, func(n *browser.NetworkEntry) {
			n.Failed = p.ErrorText
			if p.Canceled {
				n.Failed = "canceled"
			}
		})
	}
}

type remoteObj struct {
	Type           string          `json:"type"`
	Value          json.RawMessage `json:"value"`
	Unserializable string          `json:"unserializableValue"`
	Description    string          `json:"description"`
}

func (o remoteObj) String() string {
	if len(o.Value) > 0 {
		var s string
		if json.Unmarshal(o.Value, &s) == nil {
			return s
		}
		return string(o.Value)
	}
	if o.Unserializable != "" {
		return o.Unserializable
	}
	if o.Description != "" {
		return o.Description
	}
	return o.Type
}

// --- sessions -------------------------------------------------------------

func (d *Driver) session(ctx context.Context, tab string) (string, error) {
	d.mu.Lock()
	sid, ok := d.sessions[tab]
	d.mu.Unlock()
	if ok {
		return sid, nil
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := d.conn.CallInto(ctx, "Target.attachToTarget", map[string]any{"targetId": tab, "flatten": true}, "", &res); err != nil {
		return "", fmt.Errorf("attach to tab %s: %w", tab, err)
	}
	d.mu.Lock()
	d.sessions[tab] = res.SessionID
	d.bySession[res.SessionID] = tab
	d.mu.Unlock()
	for _, m := range []string{"Runtime.enable", "Page.enable", "Network.enable", "Log.enable"} {
		if _, err := d.conn.Call(ctx, m, nil, res.SessionID); err != nil && m != "Log.enable" {
			return "", err
		}
	}
	return res.SessionID, nil
}

func (d *Driver) call(ctx context.Context, tab, method string, params any, out any) error {
	sid, err := d.session(ctx, tab)
	if err != nil {
		return err
	}
	return d.conn.CallInto(ctx, method, params, sid, out)
}

// --- tabs -----------------------------------------------------------------

func (d *Driver) Tabs(ctx context.Context) ([]browser.Tab, error) {
	var res struct {
		Infos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := d.conn.CallInto(ctx, "Target.getTargets", nil, "", &res); err != nil {
		return nil, err
	}
	var tabs []browser.Tab
	for _, t := range res.Infos {
		if t.Type != "page" || strings.HasPrefix(t.URL, "devtools://") {
			continue
		}
		tabs = append(tabs, browser.Tab{ID: t.TargetID, URL: t.URL, Title: t.Title})
	}
	return tabs, nil
}

func (d *Driver) NewTab(ctx context.Context, url string) (string, error) {
	if url == "" {
		url = "about:blank"
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := d.conn.CallInto(ctx, "Target.createTarget", map[string]any{"url": url}, "", &res); err != nil {
		return "", err
	}
	// Attach right away so console/network of the first load are captured.
	if _, err := d.session(ctx, res.TargetID); err != nil {
		return "", err
	}
	return res.TargetID, nil
}

func (d *Driver) CloseTab(ctx context.Context, tab string) error {
	_, err := d.conn.Call(ctx, "Target.closeTarget", map[string]any{"targetId": tab}, "")
	return err
}

func (d *Driver) ActivateTab(ctx context.Context, tab string) error {
	if _, err := d.session(ctx, tab); err != nil {
		return err
	}
	_, err := d.conn.Call(ctx, "Target.activateTarget", map[string]any{"targetId": tab}, "")
	return err
}

func (d *Driver) Navigate(ctx context.Context, tab, url string) error {
	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := d.call(ctx, tab, "Page.navigate", map[string]any{"url": url}, &res); err != nil {
		return err
	}
	if res.ErrorText != "" {
		return fmt.Errorf("navigate %s: %s", url, res.ErrorText)
	}
	return nil
}

// --- page -----------------------------------------------------------------

func (d *Driver) Evaluate(ctx context.Context, tab, expression string) (string, error) {
	var res struct {
		Result    remoteObj `json:"result"`
		Exception *struct {
			Text      string    `json:"text"`
			Exception remoteObj `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := d.call(ctx, tab, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
		"userGesture":   true,
	}, &res)
	if err != nil {
		return "", err
	}
	if res.Exception != nil {
		msg := res.Exception.Exception.Description
		if msg == "" {
			msg = res.Exception.Text
		}
		return "", fmt.Errorf("javascript error: %s", msg)
	}
	if res.Result.Type == "undefined" {
		return "", nil
	}
	return res.Result.String(), nil
}

func (d *Driver) Screenshot(ctx context.Context, tab string) ([]byte, error) {
	// Background tabs in a headed browser do not paint; bring it forward.
	_ = d.ActivateTab(ctx, tab)
	var res struct {
		Data string `json:"data"`
	}
	if err := d.call(ctx, tab, "Page.captureScreenshot", map[string]any{"format": "png"}, &res); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.Data)
}

func (d *Driver) Click(ctx context.Context, tab string, x, y float64, button string, count int) error {
	if button == "" {
		button = "left"
	}
	if count < 1 {
		count = 1
	}
	if err := d.Hover(ctx, tab, x, y); err != nil {
		return err
	}
	for i := 1; i <= count; i++ {
		for _, typ := range []string{"mousePressed", "mouseReleased"} {
			if err := d.call(ctx, tab, "Input.dispatchMouseEvent", map[string]any{
				"type": typ, "x": x, "y": y, "button": button, "clickCount": i,
			}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Driver) Hover(ctx context.Context, tab string, x, y float64) error {
	return d.call(ctx, tab, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y}, nil)
}

func (d *Driver) InsertText(ctx context.Context, tab, text string) error {
	return d.call(ctx, tab, "Input.insertText", map[string]any{"text": text}, nil)
}

// editing commands make clipboard/select-all shortcuts work on every OS,
// including macOS where Chromium ignores synthetic Cmd+key accelerators.
var editCommands = map[string]string{"a": "selectAll", "c": "copy", "x": "cut", "v": "paste", "z": "undo"}

func (d *Driver) Press(ctx context.Context, tab string, c browser.Combo) error {
	mods := 0
	for _, m := range c.Modifiers {
		mods |= browser.ModifierBit(m)
	}
	down := map[string]any{
		"type": "keyDown", "modifiers": mods, "key": c.Key.Name, "code": c.Key.Code,
		"windowsVirtualKeyCode": c.Key.VK,
	}
	if c.Key.Text != "" && mods&(2|4|1) == 0 {
		text := c.Key.Text
		if mods&8 != 0 {
			text = strings.ToUpper(text)
		}
		down["text"] = text
	} else {
		down["type"] = "rawKeyDown"
	}
	if mods&(2|4) != 0 {
		if cmd, ok := editCommands[strings.ToLower(c.Key.Name)]; ok {
			down["commands"] = []string{cmd}
		}
	}
	if err := d.call(ctx, tab, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return d.call(ctx, tab, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "modifiers": mods, "key": c.Key.Name, "code": c.Key.Code,
		"windowsVirtualKeyCode": c.Key.VK,
	}, nil)
}

func (d *Driver) SetViewport(ctx context.Context, tab string, width, height int) error {
	return d.call(ctx, tab, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 0, "mobile": false,
	}, nil)
}

func (d *Driver) Console(tab string) []browser.ConsoleEntry { return d.buf.Console(tab) }
func (d *Driver) ClearConsole(tab string)                   { d.buf.ClearConsole(tab) }
func (d *Driver) Network(tab string) []browser.NetworkEntry { return d.buf.Network(tab) }
func (d *Driver) ClearNetwork(tab string)                   { d.buf.ClearNetwork(tab) }

func (d *Driver) CloseBrowser(ctx context.Context) error {
	_, err := d.conn.Call(ctx, "Browser.close", nil, "")
	return err
}

func (d *Driver) Close() error { return d.conn.Close() }
