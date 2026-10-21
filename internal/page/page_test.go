package page

import (
	"strings"
	"testing"
)

func TestUserCode(t *testing.T) {
	expr := UserCode("document.title;")
	if !strings.Contains(expr, "return (document.title\n);") {
		t.Errorf("expression not wrapped as return: %s", expr)
	}
	body := UserCode("const a = 1; return a + 1")
	if strings.Contains(body, "return (const") {
		t.Errorf("statement body wrapped as expression: %s", body)
	}
}

func TestCallEmbedsLibrary(t *testing.T) {
	c := Call("snapshot", map[string]any{"mode": "all"})
	if !strings.Contains(c, "window.__aib.snapshot({\"mode\":\"all\"})") || !strings.Contains(c, "const VERSION") {
		t.Fatal("call does not embed library and invocation")
	}
}
