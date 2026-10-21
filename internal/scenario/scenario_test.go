package scenario

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadShorthandAndOptions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.yaml")
	_ = os.WriteFile(p, []byte(`
steps:
  - navigate: example.com
  - name: go
    click: More
    optional: true
  - assert: { url_contains: example }
  - sleep: 10
`), 0o644)
	sc, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Name != "s" || len(sc.Steps) != 4 {
		t.Fatalf("bad scenario: %+v", sc)
	}
	if sc.Steps[0].Args["url"] != "example.com" {
		t.Errorf("navigate shorthand: %+v", sc.Steps[0].Args)
	}
	if s := sc.Steps[1]; s.Name != "go" || !s.Optional || s.Args["match"] != "More" {
		t.Errorf("click step: %+v", s)
	}
}

func TestLoadRejectsUnknownAndAmbiguousSteps(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown.yaml": "steps:\n  - teleport: x\n",
		"two.yaml":     "steps:\n  - navigate: a\n    click: b\n",
	} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("AIB_T", "env")
	vars := map[string]string{"base": "http://x"}
	got := expand(map[string]any{"url": "${base}/a", "list": []any{"${env.AIB_T}", 1}, "keep": "${nope}"}, vars).(map[string]any)
	if got["url"] != "http://x/a" || got["list"].([]any)[0] != "env" || got["keep"] != "${nope}" {
		t.Fatalf("expand: %+v", got)
	}
}
