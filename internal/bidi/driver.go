// Package bidi drives browsers over W3C WebDriver BiDi. Firefox exposes it
// natively via --remote-debugging-port (CDP was removed from Firefox in 129).
package bidi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gwakko/agent-in-browser/internal/browser"
	"github.com/gwakko/agent-in-browser/internal/rpc"
)

// Driver implements browser.Driver over WebDriver BiDi.
type Driver struct {
	conn *rpc.Conn
	buf  *browser.Buffers
}

var _ browser.Driver = (*Driver)(nil)

// Connect opens a BiDi session at endpoint (ws://host:port/session).
// Firefox allows only one BiDi session at a time.
func Connect(ctx context.Context, endpoint string) (*Driver, error) {
	d := &Driver{buf: browser.NewBuffers()}
	var err error
	d.conn, err = rpc.Dial(ctx, endpoint, d.onEvent)
	if err != nil {
		return nil, err
	}
	if _, err := d.conn.Call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}, ""); err != nil {
		d.conn.Close()
		if strings.Contains(err.Error(), "session not created") {
			return nil, fmt.Errorf("%w (Firefox allows a single BiDi client: another agent-in-browser process is probably attached)", err)
		}
		return nil, err
	}
	_, err = d.conn.Call(ctx, "session.subscribe", map[string]any{
		"events": []string{"log.entryAdded", "network.beforeRequestSent", "network.responseCompleted", "network.fetchError"},
	}, "")
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Driver) Protocol() browser.Protocol { return browser.BiDi }

type remoteValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

func (v remoteValue) String() string {
	switch v.Type {
	case "string":
		var s string
		_ = json.Unmarshal(v.Value, &s)
		return s
	case "undefined", "null":
		return v.Type
	}
	if len(v.Value) > 0 {
		return string(v.Value)
	}
	return v.Type
}

func (d *Driver) onEvent(e rpc.Event) {
	now := time.Now()
	switch e.Method {
	case "log.entryAdded":
		var p struct {
			Level  string        `json:"level"`
			Text   string        `json:"text"`
			Method string        `json:"method"`
			Args   []remoteValue `json:"args"`
			Source struct {
				Context string `json:"context"`
			} `json:"source"`
		}
		_ = json.Unmarshal(e.Params, &p)
		text := p.Text
		if text == "" && len(p.Args) > 0 {
			parts := make([]string, len(p.Args))
			for i, a := range p.Args {
				parts[i] = a.String()
			}
			text = strings.Join(parts, " ")
		}
		level := p.Level
		if p.Method != "" && p.Method != "log" {
			level = p.Method
		}
		if level == "warning" {
			level = "warn"
		}
		d.buf.AddConsole(p.Source.Context, browser.ConsoleEntry{Level: level, Text: text, Time: now})
	case "network.beforeRequestSent", "network.responseCompleted", "network.fetchError":
		var p struct {
			Context string `json:"context"`
			Request struct {
				Request string `json:"request"`
				URL     string `json:"url"`
				Method  string `json:"method"`
			} `json:"request"`
			Response struct {
				Status   int    `json:"status"`
				MimeType string `json:"mimeType"`
			} `json:"response"`
			ErrorText string `json:"errorText"`
		}
		_ = json.Unmarshal(e.Params, &p)
		if p.Context == "" {
			return
		}
		d.buf.UpsertNetwork(p.Context, browser.NetworkEntry{ID: p.Request.Request, Time: now}, func(n *browser.NetworkEntry) {
			n.URL, n.Method = p.Request.URL, p.Request.Method
			switch e.Method {
			case "network.responseCompleted":
				n.Status, n.MimeType = p.Response.Status, p.Response.MimeType
			case "network.fetchError":
				n.Failed = p.ErrorText
			}
		})
	}
}

func (d *Driver) Tabs(ctx context.Context) ([]browser.Tab, error) {
	var res struct {
		Contexts []struct {
			Context string `json:"context"`
			URL     string `json:"url"`
		} `json:"contexts"`
	}
	if err := d.conn.CallInto(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0}, "", &res); err != nil {
		return nil, err
	}
	tabs := make([]browser.Tab, 0, len(res.Contexts))
	for _, c := range res.Contexts {
		title, _ := d.Evaluate(ctx, c.Context, "document.title")
		tabs = append(tabs, browser.Tab{ID: c.Context, URL: c.URL, Title: title})
	}
	return tabs, nil
}

func (d *Driver) NewTab(ctx context.Context, url string) (string, error) {
	var res struct {
		Context string `json:"context"`
	}
	if err := d.conn.CallInto(ctx, "browsingContext.create", map[string]any{"type": "tab"}, "", &res); err != nil {
		return "", err
	}
	if url != "" && url != "about:blank" {
		if err := d.Navigate(ctx, res.Context, url); err != nil {
			return res.Context, err
		}
	}
	return res.Context, nil
}

func (d *Driver) CloseTab(ctx context.Context, tab string) error {
	_, err := d.conn.Call(ctx, "browsingContext.close", map[string]any{"context": tab}, "")
	return err
}

func (d *Driver) ActivateTab(ctx context.Context, tab string) error {
	_, err := d.conn.Call(ctx, "browsingContext.activate", map[string]any{"context": tab}, "")
	return err
}

func (d *Driver) Navigate(ctx context.Context, tab, url string) error {
	_, err := d.conn.Call(ctx, "browsingContext.navigate", map[string]any{"context": tab, "url": url, "wait": "none"}, "")
	return err
}

func (d *Driver) Evaluate(ctx context.Context, tab, expression string) (string, error) {
	var res struct {
		Type      string      `json:"type"`
		Result    remoteValue `json:"result"`
		Exception struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	err := d.conn.CallInto(ctx, "script.evaluate", map[string]any{
		"expression":      expression,
		"target":          map[string]any{"context": tab},
		"awaitPromise":    true,
		"resultOwnership": "none",
		"userActivation":  true,
	}, "", &res)
	if err != nil {
		return "", err
	}
	if res.Type == "exception" {
		return "", fmt.Errorf("javascript error: %s", res.Exception.Text)
	}
	if res.Result.Type == "undefined" {
		return "", nil
	}
	return res.Result.String(), nil
}

func (d *Driver) Screenshot(ctx context.Context, tab string) ([]byte, error) {
	_ = d.ActivateTab(ctx, tab)
	var res struct {
		Data string `json:"data"`
	}
	if err := d.conn.CallInto(ctx, "browsingContext.captureScreenshot", map[string]any{"context": tab}, "", &res); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.Data)
}

func (d *Driver) perform(ctx context.Context, tab string, sources ...map[string]any) error {
	_, err := d.conn.Call(ctx, "input.performActions", map[string]any{"context": tab, "actions": sources}, "")
	return err
}

func mouse(actions ...map[string]any) map[string]any {
	return map[string]any{"type": "pointer", "id": "aib-mouse", "parameters": map[string]any{"pointerType": "mouse"}, "actions": actions}
}

func keys(actions ...map[string]any) map[string]any {
	return map[string]any{"type": "key", "id": "aib-keyboard", "actions": actions}
}

func move(x, y float64) map[string]any {
	return map[string]any{"type": "pointerMove", "x": int(math.Round(x)), "y": int(math.Round(y))}
}

func (d *Driver) Click(ctx context.Context, tab string, x, y float64, button string, count int) error {
	btn := map[string]int{"left": 0, "middle": 1, "right": 2}[button]
	if count < 1 {
		count = 1
	}
	acts := []map[string]any{move(x, y)}
	for i := 0; i < count; i++ {
		acts = append(acts, map[string]any{"type": "pointerDown", "button": btn}, map[string]any{"type": "pointerUp", "button": btn})
	}
	return d.perform(ctx, tab, mouse(acts...))
}

func (d *Driver) Hover(ctx context.Context, tab string, x, y float64) error {
	return d.perform(ctx, tab, mouse(move(x, y)))
}

func (d *Driver) InsertText(ctx context.Context, tab, text string) error {
	acts := make([]map[string]any, 0, 2*len(text))
	for _, r := range text {
		v := string(r)
		if r == '\n' {
			v = ""
		}
		acts = append(acts, map[string]any{"type": "keyDown", "value": v}, map[string]any{"type": "keyUp", "value": v})
	}
	if len(acts) == 0 {
		return nil
	}
	return d.perform(ctx, tab, keys(acts...))
}

func (d *Driver) Press(ctx context.Context, tab string, c browser.Combo) error {
	var acts []map[string]any
	for _, m := range c.Modifiers {
		acts = append(acts, map[string]any{"type": "keyDown", "value": m.WDValue})
	}
	acts = append(acts, map[string]any{"type": "keyDown", "value": c.Key.WDValue}, map[string]any{"type": "keyUp", "value": c.Key.WDValue})
	for i := len(c.Modifiers) - 1; i >= 0; i-- {
		acts = append(acts, map[string]any{"type": "keyUp", "value": c.Modifiers[i].WDValue})
	}
	return d.perform(ctx, tab, keys(acts...))
}

func (d *Driver) SetViewport(ctx context.Context, tab string, width, height int) error {
	_, err := d.conn.Call(ctx, "browsingContext.setViewport", map[string]any{
		"context": tab, "viewport": map[string]any{"width": width, "height": height},
	}, "")
	return err
}

func (d *Driver) Console(tab string) []browser.ConsoleEntry { return d.buf.Console(tab) }
func (d *Driver) ClearConsole(tab string)                   { d.buf.ClearConsole(tab) }
func (d *Driver) Network(tab string) []browser.NetworkEntry { return d.buf.Network(tab) }
func (d *Driver) ClearNetwork(tab string)                   { d.buf.ClearNetwork(tab) }

func (d *Driver) CloseBrowser(ctx context.Context) error {
	_, err := d.conn.Call(ctx, "browser.close", nil, "")
	return err
}

// Close ends the BiDi session (freeing the single slot) and disconnects.
func (d *Driver) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = d.conn.Call(ctx, "session.end", nil, "")
	return d.conn.Close()
}
