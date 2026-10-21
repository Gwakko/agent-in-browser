package browser

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Home is the data directory: $AIB_HOME or ~/.agent-in-browser.
func Home() string {
	if h := os.Getenv("AIB_HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".agent-in-browser")
	}
	return filepath.Join(os.TempDir(), "agent-in-browser")
}

// Instance is a browser started (or attached to) by agent-in-browser,
// persisted so that separate CLI invocations and MCP servers share it.
type Instance struct {
	Name        string    `json:"name"`
	Browser     string    `json:"browser"`
	Protocol    Protocol  `json:"protocol"`
	Endpoint    string    `json:"endpoint"`
	PID         int       `json:"pid,omitempty"`
	UserDataDir string    `json:"userDataDir,omitempty"`
	Headless    bool      `json:"headless,omitempty"`
	CurrentTab  string    `json:"currentTab,omitempty"`
	Owned       bool      `json:"owned"` // launched by us (stop may kill it)
	StartedAt   time.Time `json:"startedAt"`
}

func statePath(name string) string {
	return filepath.Join(Home(), "instances", name+".json")
}

// LoadInstance returns the saved instance or nil.
func LoadInstance(name string) (*Instance, error) {
	data, err := os.ReadFile(statePath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var in Instance
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, nil // corrupt state is treated as absent
	}
	return &in, nil
}

// SaveInstance atomically persists the instance.
func SaveInstance(in *Instance) error {
	p := statePath(in.Name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// DeleteInstance removes the saved state.
func DeleteInstance(name string) error {
	err := os.Remove(statePath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Reachable reports whether the instance's endpoint accepts TCP connections.
// PIDs are not reliable: Firefox re-execs itself on a fresh profile.
func (in *Instance) Reachable() bool {
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Host == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", u.Host, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
