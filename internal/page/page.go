// Package page holds the JavaScript injected into pages and helpers to build
// calls into it.
package page

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed lib.js
var lib string

// Call returns an expression that installs the library (if needed) and calls
// window.__aib.<fn>(args...), JSON-encoding the result.
func Call(fn string, args ...any) string {
	enc := make([]string, len(args))
	for i, a := range args {
		b, _ := json.Marshal(a)
		enc[i] = string(b)
	}
	return Wrap(lib + "\nreturn await window.__aib." + fn + "(" + strings.Join(enc, ",") + ");")
}

// Wrap turns a function body into an expression that resolves to a JSON
// string of its return value, so every driver returns the same shape.
func Wrap(body string) string {
	return `(async () => {
const __aibRes = await (async () => {
` + body + `
})();
try { return JSON.stringify(__aibRes === undefined ? null : __aibRes); }
catch (e) { return JSON.stringify(String(__aibRes)); }
})()`
}

// UserCode wraps code from the evaluate tool: a bare expression is returned,
// code containing statements must use `return` explicitly.
func UserCode(code string) string {
	trimmed := strings.TrimSpace(code)
	if looksLikeBody(trimmed) {
		return Wrap(trimmed)
	}
	return Wrap("return (" + strings.TrimRight(trimmed, ";") + "\n);")
}

func looksLikeBody(code string) bool {
	if strings.Contains(code, "return ") || strings.Contains(code, "return;") {
		return true
	}
	for _, kw := range []string{"const ", "let ", "var ", "if (", "for (", "while (", "throw ", "try {", "function "} {
		if strings.HasPrefix(code, kw) {
			return true
		}
	}
	return false
}
