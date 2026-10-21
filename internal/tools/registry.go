// Package tools is the single catalogue of browser tools. The same definitions
// are served over MCP, exposed by `agent-in-browser call` and used as steps in
// scenarios, so behaviour is identical for agents, shell users and tests.
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gwakko/agent-in-browser/internal/session"
)

// Content is an MCP content block.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Result is a tool outcome.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Text returns all text blocks joined.
func (r *Result) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Images returns decoded image blocks.
func (r *Result) Images() [][]byte {
	var out [][]byte
	for _, c := range r.Content {
		if c.Type == "image" {
			if b, err := base64.StdEncoding.DecodeString(c.Data); err == nil {
				out = append(out, b)
			}
		}
	}
	return out
}

func textResult(format string, a ...any) *Result {
	return &Result{Content: []Content{{Type: "text", Text: fmt.Sprintf(format, a...)}}}
}

func errorResult(err error) *Result {
	return &Result{IsError: true, Content: []Content{{Type: "text", Text: "Error: " + err.Error()}}}
}

// Tool is one callable capability.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	ReadOnly    bool           `json:"-"`
	run         func(ctx context.Context, s *session.Session, a Args) (*Result, error)
}

var registry = map[string]Tool{}

func register(t Tool) {
	if _, dup := registry[t.Name]; dup {
		panic("duplicate tool " + t.Name)
	}
	registry[t.Name] = t
}

// All returns tools sorted by name.
func All() []Tool {
	out := make([]Tool, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get looks a tool up by name.
func Get(name string) (Tool, bool) {
	t, ok := registry[name]
	return t, ok
}

// Call runs a tool. Failures are reported as error results (never Go errors)
// so agents can read and react to them.
func Call(ctx context.Context, s *session.Session, name string, args map[string]any) *Result {
	t, ok := registry[name]
	if !ok {
		return errorResult(fmt.Errorf("unknown tool %q", name))
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := t.run(ctx, s, Args(args))
	if err != nil {
		return errorResult(err)
	}
	return res
}

// Args are decoded JSON arguments with typed accessors.
type Args map[string]any

func (a Args) Str(k string) string {
	switch v := a[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func (a Args) Int(k string, def int) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		var n int
		if _, err := fmt.Sscan(v, &n); err == nil {
			return n
		}
	}
	return def
}

func (a Args) Float(k string) (float64, bool) {
	switch v := a[k].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		var f float64
		if _, err := fmt.Sscan(v, &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func (a Args) Bool(k string, def bool) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	}
	return def
}

// target extracts the element locator fields.
func (a Args) target() map[string]any {
	t := map[string]any{}
	for _, k := range []string{"ref", "selector", "match"} {
		if v := a.Str(k); v != "" {
			t[k] = v
		}
	}
	return t
}

// --- JSON Schema helpers ----------------------------------------------------

type props map[string]any

func object(p props, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": p, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func intg(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func enum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}

func with(p props, extra props) props {
	out := props{}
	for k, v := range p {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

var tabProp = props{"tab": str("Tab id from tabs_list. Defaults to the current tab.")}

var targetProps = props{
	"ref":      str("Element ref from snapshot/find, e.g. \"e12\". Preferred."),
	"selector": str("CSS selector (first match)."),
	"match":    str("Visible text / accessible name to match (case-insensitive substring)."),
}
