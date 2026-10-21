// Package mcp is a dependency-free Model Context Protocol server over stdio
// (newline-delimited JSON-RPC 2.0) exposing the tools package.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"slices"
	"sync"

	"github.com/gwakko/agent-in-browser/internal/session"
	"github.com/gwakko/agent-in-browser/internal/tools"
)

var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const instructions = `agent-in-browser controls a real browser (Chromium family via CDP, Firefox via WebDriver BiDi).
Workflow: navigate -> snapshot (get element refs) -> click/type/fill_form using ref -> snapshot again to verify.
Prefer snapshot/get_text over screenshot; use screenshot for visual checks. Refs go stale after navigation or big DOM changes: re-snapshot.
Use console_messages and network_requests to debug. Never enter credentials or payment data unless the user explicitly provided them for this task.`

// Server serves MCP over a reader/writer pair.
type Server struct {
	Name    string
	Version string
	Session *session.Session
	Logger  *log.Logger

	writeMu sync.Mutex
	out     io.Writer
	wg      sync.WaitGroup
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve blocks until in is exhausted or ctx is cancelled.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	calls := s.startWorker(ctx)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.reply(nil, nil, &rpcError{Code: -32700, Message: "parse error"})
			continue
		}
		if len(req.ID) == 0 { // notification
			continue
		}
		if req.Method == "tools/call" {
			calls <- req
			continue
		}
		result, rerr := s.handle(ctx, req)
		s.reply(req.ID, result, rerr)
	}
	close(calls)
	s.wg.Wait()
	return sc.Err()
}

// startWorker runs tool calls one at a time in arrival order (agents may
// pipeline navigate → click → read), off the read loop so ping stays live.
func (s *Server) startWorker(ctx context.Context) chan<- request {
	calls := make(chan request, 64)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for req := range calls {
			result, rerr := s.handle(ctx, req)
			s.reply(req.ID, result, rerr)
		}
	}()
	return calls
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := supportedVersions[0]
		if slices.Contains(supportedVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		s.logf("client %s %s (protocol %s)", p.ClientInfo.Name, p.ClientInfo.Version, v)
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0)
		for _, t := range tools.All() {
			list = append(list, map[string]any{
				"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema,
				"annotations": map[string]any{"readOnlyHint": t.ReadOnly, "openWorldHint": true},
			})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
		if _, ok := tools.Get(p.Name); !ok {
			return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", p.Name)}
		}
		res := tools.Call(ctx, s.Session, p.Name, p.Arguments)
		if res.IsError {
			s.logf("%s failed: %s", p.Name, res.Text())
		}
		return res, nil
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

func (s *Server) reply(id json.RawMessage, result any, rerr *rpcError) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if id == nil {
		msg["id"] = nil
	}
	if rerr != nil {
		msg["error"] = rerr
	} else {
		msg["result"] = result
	}
	data, err := json.Marshal(msg)
	if err != nil {
		s.logf("marshal reply: %v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
}

func (s *Server) logf(format string, a ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, a...)
	}
}
