package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gwakko/agent-in-browser/internal/session"
)

func TestHandshakeListAndErrors(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"missing"}}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	s := &Server{Name: "t", Version: "0", Session: session.New(session.Config{})}
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q", line)
		}
		byID[string(mustJSON(m["id"]))] = m
	}
	if v := byID["1"]["result"].(map[string]any)["protocolVersion"]; v != "2025-06-18" {
		t.Errorf("negotiated %v", v)
	}
	if n := len(byID["2"]["result"].(map[string]any)["tools"].([]any)); n < 15 {
		t.Errorf("only %d tools", n)
	}
	if byID["3"]["error"] == nil || byID["4"]["error"] == nil || byID["null"]["error"] == nil {
		t.Errorf("expected errors for unknown method, unknown tool and parse error: %v", byID)
	}
	if len(byID) != 5 {
		t.Errorf("notification must not be answered; got %d replies", len(byID))
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
