// Package setup registers agent-in-browser with AI coding agents (MCP config)
// and installs the bundled agent skill.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const ServerName = "agent-in-browser"

// Client is an MCP-capable agent.
type Client struct {
	Name  string
	Title string
	// Path of the config file to merge into (empty: print-only).
	Path func() string
	// Key is the top-level JSON object holding servers.
	Key string
	// Entry builds the server entry.
	Entry func(bin string, args []string) any
	// Apply overrides file merging (e.g. CLI-based or TOML clients).
	Apply func(bin string, args []string) error
	// Snippet is shown when printing instructions.
	Snippet func(bin string, args []string) string
}

func home(p ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, p...)...)
}

func stdEntry(bin string, args []string) any {
	return map[string]any{"command": bin, "args": args}
}

func jsonSnippet(key string, entry any) string {
	b, _ := json.MarshalIndent(map[string]any{key: map[string]any{ServerName: entry}}, "", "  ")
	return string(b)
}

func claudeDesktopPath() string {
	switch runtime.GOOS {
	case "darwin":
		return home("Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")
	}
	return home(".config", "Claude", "claude_desktop_config.json")
}

// Clients returns supported agents.
func Clients() []Client {
	jsonClient := func(name, title, key string, path func() string, entry func(string, []string) any) Client {
		return Client{Name: name, Title: title, Path: path, Key: key, Entry: entry,
			Snippet: func(bin string, args []string) string { return jsonSnippet(key, entry(bin, args)) }}
	}
	return []Client{
		{
			Name: "claude-code", Title: "Claude Code",
			Apply: func(bin string, args []string) error {
				cmd := exec.Command("claude", append([]string{"mcp", "add", "--scope", "user", ServerName, "--", bin}, args...)...)
				cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
				return cmd.Run()
			},
			Snippet: func(bin string, args []string) string {
				return fmt.Sprintf("claude mcp add --scope user %s -- %s %s", ServerName, bin, strings.Join(args, " "))
			},
		},
		jsonClient("claude-desktop", "Claude Desktop", "mcpServers", claudeDesktopPath, stdEntry),
		jsonClient("cursor", "Cursor", "mcpServers", func() string { return home(".cursor", "mcp.json") }, stdEntry),
		jsonClient("windsurf", "Windsurf", "mcpServers", func() string { return home(".codeium", "windsurf", "mcp_config.json") }, stdEntry),
		jsonClient("gemini", "Gemini CLI", "mcpServers", func() string { return home(".gemini", "settings.json") }, stdEntry),
		jsonClient("opencode", "opencode", "mcp", func() string { return home(".config", "opencode", "opencode.json") },
			func(bin string, args []string) any {
				return map[string]any{"type": "local", "command": append([]string{bin}, args...), "enabled": true}
			}),
		{
			Name: "codex", Title: "OpenAI Codex CLI",
			Apply: func(bin string, args []string) error {
				return appendCodexTOML(home(".codex", "config.toml"), bin, args)
			},
			Snippet: func(bin string, args []string) string { return codexTOML(bin, args) },
		},
		{
			Name: "vscode", Title: "VS Code / GitHub Copilot (workspace .vscode/mcp.json)",
			Snippet: func(bin string, args []string) string {
				return jsonSnippet("servers", map[string]any{"type": "stdio", "command": bin, "args": args})
			},
		},
		{
			Name: "zed", Title: "Zed (settings.json)",
			Snippet: func(bin string, args []string) string {
				return jsonSnippet("context_servers", map[string]any{"command": bin, "args": args})
			},
		},
		{
			Name: "generic", Title: "Any MCP client (stdio)",
			Snippet: func(bin string, args []string) string { return jsonSnippet("mcpServers", stdEntry(bin, args)) },
		},
	}
}

// Find returns a client by name.
func Find(name string) (Client, error) {
	var names []string
	for _, c := range Clients() {
		if c.Name == name {
			return c, nil
		}
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return Client{}, fmt.Errorf("unknown client %q (known: %s)", name, strings.Join(names, ", "))
}

// Install writes the configuration. It returns a human-readable description.
func Install(c Client, bin string, args []string) (string, error) {
	if c.Apply != nil {
		if err := c.Apply(bin, args); err != nil {
			return "", err
		}
		return "configured " + c.Title, nil
	}
	if c.Path == nil {
		return "", fmt.Errorf("%s has no user-level config file; add this snippet manually:\n%s", c.Title, c.Snippet(bin, args))
	}
	p := c.Path()
	if err := mergeJSON(p, c.Key, c.Entry(bin, args)); err != nil {
		return "", err
	}
	return "updated " + p, nil
}

func mergeJSON(path, key string, entry any) error {
	cfg := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("%s is not plain JSON (comments?); add the snippet manually: %w", path, err)
			}
			if err := os.WriteFile(path+".bak", data, 0o600); err != nil {
				return err
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	default:
		return err
	}
	servers, _ := cfg[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[ServerName] = entry
	cfg[key] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func codexTOML(bin string, args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = fmt.Sprintf("%q", a)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [%s]\n", ServerName, bin, strings.Join(q, ", "))
}

func appendCodexTOML(path, bin string, args []string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if strings.Contains(string(data), "[mcp_servers."+ServerName+"]") {
		return fmt.Errorf("%s already has [mcp_servers.%s]; edit it manually", path, ServerName)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n" + codexTOML(bin, args))
	return err
}

// SkillDirs maps agent names to their user-level skills directories.
func SkillDirs() map[string]string {
	return map[string]string{
		"claude": home(".claude", "skills"),
		"codex":  home(".codex", "skills"),
		"agents": home(".agents", "skills"),
	}
}

// InstallSkill copies the embedded skill tree (rooted at skillName) into dir.
func InstallSkill(src fs.FS, skillName, dir string) (string, error) {
	dest := filepath.Join(dir, skillName)
	err := fs.WalkDir(src, skillName, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	return dest, err
}
