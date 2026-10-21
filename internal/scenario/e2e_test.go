//go:build integration

package scenario

import (
	"context"
	"testing"

	"github.com/gwakko/agent-in-browser/internal/browser"
)

// Runs the example signup scenario on every supported installed browser.
// go test -tags integration ./internal/scenario/
func TestSignupScenarioOnInstalledBrowsers(t *testing.T) {
	sc, err := Load("../../examples/scenarios/signup.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, f := range browser.Installed() {
		if !f.Kind.Supported {
			continue
		}
		ran++
		t.Run(f.Kind.Name, func(t *testing.T) {
			rep := Run(context.Background(), sc, Options{Browser: f.Kind.Name, Headless: true})
			if !rep.OK {
				t.Fatalf("%s", rep.Error)
			}
		})
	}
	if ran == 0 {
		t.Skip("no supported browsers installed")
	}
}
