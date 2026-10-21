package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gwakko/agent-in-browser/internal/browser"
	"github.com/gwakko/agent-in-browser/internal/page"
	"github.com/gwakko/agent-in-browser/internal/session"
)

func init() {
	register(Tool{
		Name:        "browser_status",
		Description: "Show the connected browser (launching it if needed), protocol, current tab and open tabs.",
		InputSchema: object(props{}),
		ReadOnly:    true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, "")
			if err != nil {
				return nil, err
			}
			in := s.Instance()
			var b strings.Builder
			fmt.Fprintf(&b, "Browser: %s (%s)\nEndpoint: %s\n", in.Browser, drv.Protocol(), in.Endpoint)
			if in.PID > 0 {
				fmt.Fprintf(&b, "PID: %d, headless: %v, profile: %s\n", in.PID, in.Headless, in.UserDataDir)
			}
			b.WriteString("\n")
			b.WriteString(renderTabs(ctx, drv, tab))
			return textResult("%s", b.String()), nil
		},
	})

	register(Tool{
		Name:        "browsers_installed",
		Description: "List browsers detected on this machine and whether they can be driven.",
		InputSchema: object(props{}),
		ReadOnly:    true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			var b strings.Builder
			for _, f := range browser.Installed() {
				status := "supported"
				if !f.Kind.Supported {
					status = "detected, not yet supported"
				}
				fmt.Fprintf(&b, "%-16s %-28s %-9s %s  [%s]\n", f.Kind.Name, f.Kind.Title, f.Kind.Protocol, f.Path, status)
			}
			if b.Len() == 0 {
				b.WriteString("No known browsers found.")
			}
			return textResult("%s", b.String()), nil
		},
	})

	register(Tool{
		Name:        "tabs_list",
		Description: "List open tabs. The current tab (default target of page tools) is marked with *.",
		InputSchema: object(props{}),
		ReadOnly:    true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, "")
			if err != nil {
				return nil, err
			}
			return textResult("%s", renderTabs(ctx, drv, tab)), nil
		},
	})

	register(Tool{
		Name:        "tab_new",
		Description: "Open a new tab (optionally at a URL) and make it current.",
		InputSchema: object(props{"url": str("URL to open; scheme defaults to https://")}),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, err := s.Driver(ctx)
			if err != nil {
				return nil, err
			}
			url := normalizeURL(a.Str("url"))
			id, err := drv.NewTab(ctx, url)
			if err != nil {
				return nil, err
			}
			s.SetCurrent(id)
			_ = drv.ActivateTab(ctx, id)
			if url != "" && url != "about:blank" {
				s.WaitReady(ctx, drv, id, 30*time.Second)
			}
			return pageInfo(ctx, s, drv, id, "Opened tab "+id)
		},
	})

	register(Tool{
		Name:        "tab_select",
		Description: "Make a tab current (and bring it to front).",
		InputSchema: object(props{"tab": str("Tab id from tabs_list.")}, "tab"),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, err := s.Driver(ctx)
			if err != nil {
				return nil, err
			}
			id := a.Str("tab")
			if err := drv.ActivateTab(ctx, id); err != nil {
				return nil, err
			}
			s.SetCurrent(id)
			return pageInfo(ctx, s, drv, id, "Selected tab "+id)
		},
	})

	register(Tool{
		Name:        "tab_close",
		Description: "Close a tab (default: current). Another tab becomes current.",
		InputSchema: object(tabProp),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			if err := drv.CloseTab(ctx, tab); err != nil {
				return nil, err
			}
			time.Sleep(100 * time.Millisecond)
			_, cur, err := s.Tab(ctx, "")
			if err != nil {
				return nil, err
			}
			return textResult("Closed tab %s; current tab is now %s", tab, cur), nil
		},
	})

	register(Tool{
		Name:        "navigate",
		Description: "Navigate the tab to a URL, or go back/forward/reload. Waits for the page to load and returns its title and URL.",
		InputSchema: object(with(tabProp, props{
			"url":        str("Destination URL; scheme defaults to https://"),
			"action":     enum("History action instead of url.", "back", "forward", "reload"),
			"timeout_ms": intg("Load timeout (default 30000)."),
		})),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			switch a.Str("action") {
			case "back":
				_, err = drv.Evaluate(ctx, tab, "history.back()")
			case "forward":
				_, err = drv.Evaluate(ctx, tab, "history.forward()")
			case "reload":
				_, err = drv.Evaluate(ctx, tab, "location.reload()")
			case "":
				url := normalizeURL(a.Str("url"))
				if url == "" {
					return nil, errors.New("url or action is required")
				}
				drv.ClearNetwork(tab)
				err = drv.Navigate(ctx, tab, url)
			default:
				return nil, fmt.Errorf("unknown action %q", a.Str("action"))
			}
			if err != nil {
				return nil, err
			}
			note := ""
			if !s.WaitReady(ctx, drv, tab, time.Duration(a.Int("timeout_ms", 30000))*time.Millisecond) {
				note = " (still loading)"
			}
			return pageInfo(ctx, s, drv, tab, "Navigated"+note)
		},
	})

	register(Tool{
		Name: "snapshot",
		Description: "Return a compact accessibility-style outline of the page: headings, landmarks and every visible " +
			"interactive element with a ref (e.g. [ref=e12]) to use with click/type/fill_form. Prefer this over screenshots " +
			"for understanding page structure. mode=all also includes text content.",
		InputSchema: object(with(tabProp, props{
			"mode":      enum("interactive (default) or all (adds text blocks).", "interactive", "all"),
			"selector":  str("Limit the snapshot to the subtree matching this CSS selector."),
			"max_chars": intg("Output budget (default 20000)."),
		})),
		ReadOnly: true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			var out string
			err = s.PageCall(ctx, drv, tab, &out, "snapshot", map[string]any{
				"mode": a.Str("mode"), "selector": a.Str("selector"), "maxChars": a.Int("max_chars", 20000),
			})
			if err != nil {
				return nil, err
			}
			return textResult("%s", out), nil
		},
	})

	register(Tool{
		Name:        "find",
		Description: "Find visible elements whose text, label, placeholder, id or name contains the query. Returns refs.",
		InputSchema: object(with(tabProp, props{
			"query": str("Text to look for."),
			"limit": intg("Max results (default 20)."),
		}), "query"),
		ReadOnly: true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			var out string
			if err := s.PageCall(ctx, drv, tab, &out, "find", a.Str("query"), a.Int("limit", 20)); err != nil {
				return nil, err
			}
			return textResult("%s", out), nil
		},
	})

	register(Tool{
		Name: "click",
		Description: "Click an element (by ref, selector or visible text) or page coordinates with real mouse events. " +
			"The element is scrolled into view first.",
		InputSchema: object(with(with(tabProp, targetProps), props{
			"x":      num("Viewport x coordinate (instead of an element)."),
			"y":      num("Viewport y coordinate."),
			"button": enum("Mouse button (default left).", "left", "right", "middle"),
			"double": boolean("Double-click."),
		})),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			x, y, desc, err := locate(ctx, s, drv, tab, a)
			if err != nil {
				return nil, err
			}
			count := 1
			if a.Bool("double", false) {
				count = 2
			}
			before, _ := drv.Evaluate(ctx, tab, "location.href")
			if err := drv.Click(ctx, tab, x, y, a.Str("button"), count); err != nil {
				return nil, err
			}
			msg := "Clicked " + desc
			time.Sleep(150 * time.Millisecond)
			if after, err := drv.Evaluate(ctx, tab, "location.href"); err != nil || after != before {
				s.WaitReady(ctx, drv, tab, 15*time.Second)
				msg += navigatedSuffix(ctx, drv, tab)
			}
			return textResult("%s", msg), nil
		},
	})

	register(Tool{
		Name:        "hover",
		Description: "Move the mouse over an element or coordinates (reveals menus/tooltips).",
		InputSchema: object(with(with(tabProp, targetProps), props{"x": num("Viewport x."), "y": num("Viewport y.")})),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			x, y, desc, err := locate(ctx, s, drv, tab, a)
			if err != nil {
				return nil, err
			}
			if err := drv.Hover(ctx, tab, x, y); err != nil {
				return nil, err
			}
			return textResult("Hovering %s", desc), nil
		},
	})

	register(Tool{
		Name: "type",
		Description: "Type text with real keyboard input. If an element is given it is focused (and optionally cleared) first; " +
			"otherwise text goes to the focused element. submit=true presses Enter afterwards.",
		InputSchema: object(with(with(tabProp, targetProps), props{
			"text":   str("Text to type."),
			"clear":  boolean("Clear the field first (default true when an element is given)."),
			"submit": boolean("Press Enter after typing."),
		}), "text"),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			desc := "focused element"
			if t := a.target(); len(t) > 0 {
				fn := "focus"
				if a.Bool("clear", true) {
					fn = "clear"
				}
				if err := s.PageCall(ctx, drv, tab, &desc, fn, t); err != nil {
					return nil, err
				}
			}
			if err := drv.InsertText(ctx, tab, a.Str("text")); err != nil {
				return nil, err
			}
			msg := fmt.Sprintf("Typed %d chars into %s", len([]rune(a.Str("text"))), desc)
			if a.Bool("submit", false) {
				c, _ := browser.ParseCombo("Enter")
				if err := drv.Press(ctx, tab, c); err != nil {
					return nil, err
				}
				time.Sleep(200 * time.Millisecond)
				s.WaitReady(ctx, drv, tab, 15*time.Second)
				msg += " and pressed Enter" + navigatedSuffix(ctx, drv, tab)
			}
			return textResult("%s", msg), nil
		},
	})

	register(Tool{
		Name:        "press_key",
		Description: "Press a key or combination on the focused element, e.g. Enter, Escape, Tab, ArrowDown, Control+a, Meta+Shift+k.",
		InputSchema: object(with(tabProp, props{
			"keys":   str("Key or combo; modifiers: Control, Shift, Alt, Meta."),
			"repeat": intg("Times to press (default 1)."),
		}), "keys"),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			c, err := browser.ParseCombo(a.Str("keys"))
			if err != nil {
				return nil, err
			}
			n := a.Int("repeat", 1)
			for i := 0; i < n; i++ {
				if err := drv.Press(ctx, tab, c); err != nil {
					return nil, err
				}
			}
			return textResult("Pressed %s x%d", a.Str("keys"), n), nil
		},
	})

	register(Tool{
		Name: "fill_form",
		Description: "Set values of several fields at once (inputs, textareas, selects by value or label, checkboxes/radios with true/false). " +
			"Fires input/change events so frameworks (React/Vue/Angular) notice.",
		InputSchema: object(with(tabProp, props{
			"fields": map[string]any{
				"type":        "array",
				"description": "Fields to set.",
				"items": object(with(targetProps, props{
					"value": map[string]any{"description": "Value: string, number or boolean.", "type": []string{"string", "number", "boolean"}},
				}), "value"),
			},
		}), "fields"),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			fields, _ := a["fields"].([]any)
			if len(fields) == 0 {
				return nil, errors.New("fields must be a non-empty array")
			}
			var lines []string
			for i, f := range fields {
				m, ok := f.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("fields[%d] must be an object", i)
				}
				fa := Args(m)
				var desc string
				if err := s.PageCall(ctx, drv, tab, &desc, "setValue", fa.target(), m["value"]); err != nil {
					return nil, fmt.Errorf("fields[%d]: %w", i, err)
				}
				lines = append(lines, desc)
			}
			return textResult("Set %d fields:\n%s", len(lines), strings.Join(lines, "\n")), nil
		},
	})

	register(Tool{
		Name:        "scroll",
		Description: "Scroll the page (or a scrollable element) by direction/amount, or scroll an element into view.",
		InputSchema: object(with(with(tabProp, targetProps), props{
			"direction": enum("Direction (default down).", "up", "down", "left", "right"),
			"amount":    intg("Pixels (default 600)."),
			"within":    str("CSS selector of a scroll container instead of the window."),
		})),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			opts := a.target()
			amt := a.Int("amount", 600)
			switch a.Str("direction") {
			case "up":
				opts["dy"] = -amt
			case "left":
				opts["dx"] = -amt
			case "right":
				opts["dx"] = amt
			default:
				opts["dy"] = amt
			}
			if w := a.Str("within"); w != "" {
				opts["within"] = w
			}
			var out string
			if err := s.PageCall(ctx, drv, tab, &out, "scroll", opts); err != nil {
				return nil, err
			}
			return textResult("%s", out), nil
		},
	})

	register(Tool{
		Name:        "wait_for",
		Description: "Wait until text appears, a CSS selector is visible, or the URL contains a string (gone=true waits for the opposite). With only ms, just sleeps.",
		InputSchema: object(with(tabProp, props{
			"text":       str("Text that should appear in the page."),
			"selector":   str("CSS selector that should be visible."),
			"url":        str("Substring the URL should contain."),
			"gone":       boolean("Wait for the condition to become false instead."),
			"ms":         intg("Plain sleep in milliseconds (max 60000)."),
			"timeout_ms": intg("Timeout (default 10000)."),
		})),
		ReadOnly: true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			cond := map[string]any{"text": a.Str("text"), "selector": a.Str("selector"), "url": a.Str("url"), "gone": a.Bool("gone", false)}
			if a.Str("text") == "" && a.Str("selector") == "" && a.Str("url") == "" {
				ms := min(a.Int("ms", 1000), 60000)
				time.Sleep(time.Duration(ms) * time.Millisecond)
				return textResult("Waited %dms", ms), nil
			}
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			timeout := time.Duration(a.Int("timeout_ms", 10000)) * time.Millisecond
			start := time.Now()
			for {
				var ok bool
				if err := s.PageCall(ctx, drv, tab, &ok, "check", cond); err == nil && ok {
					return textResult("Condition met after %dms", time.Since(start).Milliseconds()), nil
				}
				if time.Since(start) > timeout {
					return nil, fmt.Errorf("timed out after %s waiting for %v", timeout, describeCond(cond))
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
				}
			}
		},
	})

	register(Tool{
		Name:        "get_text",
		Description: "Return the visible text of the page (or of a CSS selector) — good for reading articles, results and messages.",
		InputSchema: object(with(tabProp, props{
			"selector":  str("CSS selector to read (default body)."),
			"max_chars": intg("Max characters (default 30000)."),
		})),
		ReadOnly: true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			var out string
			if err := s.PageCall(ctx, drv, tab, &out, "text", a.Str("selector"), a.Int("max_chars", 30000)); err != nil {
				return nil, err
			}
			return textResult("%s", out), nil
		},
	})

	register(Tool{
		Name:        "screenshot",
		Description: "Capture the visible viewport as PNG. Returned as an image; set path to also save it to disk.",
		InputSchema: object(with(tabProp, props{"path": str("File path to save the PNG to.")})),
		ReadOnly:    true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			png, err := drv.Screenshot(ctx, tab)
			if err != nil {
				return nil, err
			}
			res := &Result{Content: []Content{{Type: "image", MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(png)}}}
			if p := a.Str("path"); p != "" {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return nil, err
				}
				if err := os.WriteFile(p, png, 0o644); err != nil {
					return nil, err
				}
				res.Content = append(res.Content, Content{Type: "text", Text: "Saved screenshot to " + p})
			}
			return res, nil
		},
	})

	register(Tool{
		Name: "evaluate",
		Description: "Run JavaScript in the page and return the JSON result. A bare expression is returned as is " +
			"(e.g. document.title); multi-statement code must use `return`. Promises are awaited.",
		InputSchema: object(with(tabProp, props{"code": str("JavaScript expression or function body.")}), "code"),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			out, err := drv.Evaluate(ctx, tab, page.UserCode(a.Str("code")))
			if err != nil {
				return nil, err
			}
			if len(out) > 50000 {
				out = out[:50000] + "… [truncated]"
			}
			return textResult("%s", out), nil
		},
	})

	register(Tool{
		Name: "console_messages",
		Description: "Console messages and uncaught errors captured since this server attached to the tab. " +
			"Filter by level or regex pattern.",
		InputSchema: object(with(tabProp, props{
			"level":   enum("Only this level (error includes uncaught exceptions).", "error", "warn", "info", "log", "debug"),
			"pattern": str("Regex the text must match."),
			"limit":   intg("Last N entries (default 100)."),
			"clear":   boolean("Clear the buffer after reading."),
		})),
		ReadOnly: true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			re, err := compileOpt(a.Str("pattern"))
			if err != nil {
				return nil, err
			}
			var lines []string
			for _, e := range drv.Console(tab) {
				if lv := a.Str("level"); lv != "" && e.Level != lv {
					continue
				}
				if re != nil && !re.MatchString(e.Text) {
					continue
				}
				lines = append(lines, fmt.Sprintf("[%s] %s %s", e.Time.Format("15:04:05.000"), strings.ToUpper(e.Level), e.Text))
			}
			lines = lastN(lines, a.Int("limit", 100))
			if a.Bool("clear", false) {
				drv.ClearConsole(tab)
			}
			if len(lines) == 0 {
				return textResult("No console messages captured (capture starts when agent-in-browser attaches to the tab; reload to catch load-time messages)."), nil
			}
			return textResult("%s", strings.Join(lines, "\n")), nil
		},
	})

	register(Tool{
		Name:        "network_requests",
		Description: "Network requests captured since attaching to the tab, with status. Filter by URL regex or failures only.",
		InputSchema: object(with(tabProp, props{
			"pattern":     str("Regex the URL must match."),
			"failed_only": boolean("Only failed requests and HTTP status >= 400."),
			"limit":       intg("Last N entries (default 100)."),
			"clear":       boolean("Clear the buffer after reading."),
		})),
		ReadOnly: true,
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			re, err := compileOpt(a.Str("pattern"))
			if err != nil {
				return nil, err
			}
			var lines []string
			for _, e := range drv.Network(tab) {
				if re != nil && !re.MatchString(e.URL) {
					continue
				}
				if a.Bool("failed_only", false) && e.Failed == "" && e.Status < 400 {
					continue
				}
				status := fmt.Sprint(e.Status)
				switch {
				case e.Failed != "":
					status = "FAILED " + e.Failed
				case e.Status == 0:
					status = "pending"
				}
				lines = append(lines, fmt.Sprintf("%-6s %-24s %s %s", e.Method, status, e.Type, e.URL))
			}
			lines = lastN(lines, a.Int("limit", 100))
			if a.Bool("clear", false) {
				drv.ClearNetwork(tab)
			}
			if len(lines) == 0 {
				return textResult("No requests captured (capture starts when agent-in-browser attaches to the tab)."), nil
			}
			return textResult("%s", strings.Join(lines, "\n")), nil
		},
	})

	register(Tool{
		Name:        "resize",
		Description: "Emulate a viewport size (e.g. 390x844 for a phone, 1440x900 for desktop).",
		InputSchema: object(with(tabProp, props{"width": intg("Width in CSS px."), "height": intg("Height in CSS px.")}), "width", "height"),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			drv, tab, err := s.Tab(ctx, a.Str("tab"))
			if err != nil {
				return nil, err
			}
			w, h := a.Int("width", 0), a.Int("height", 0)
			if w <= 0 || h <= 0 {
				return nil, errors.New("width and height must be positive")
			}
			if err := drv.SetViewport(ctx, tab, w, h); err != nil {
				return nil, err
			}
			return textResult("Viewport set to %dx%d", w, h), nil
		},
	})

	register(Tool{
		Name:        "browser_close",
		Description: "Close the browser launched by agent-in-browser. The next tool call starts a fresh one.",
		InputSchema: object(props{}),
		run: func(ctx context.Context, s *session.Session, a Args) (*Result, error) {
			if err := s.Stop(ctx); err != nil {
				return nil, err
			}
			return textResult("Browser closed."), nil
		},
	})
}

// --- helpers ------------------------------------------------------------------

func locate(ctx context.Context, s *session.Session, drv browser.Driver, tab string, a Args) (x, y float64, desc string, err error) {
	if t := a.target(); len(t) > 0 {
		var p struct {
			X, Y          float64
			Width, Height float64
			Desc, Ref     string
			Covered       string
		}
		if err := s.PageCall(ctx, drv, tab, &p, "point", t); err != nil {
			return 0, 0, "", err
		}
		if p.Width == 0 && p.Height == 0 {
			return 0, 0, "", fmt.Errorf("%s has no size (hidden?)", p.Desc)
		}
		desc = fmt.Sprintf("%s [ref=%s]", p.Desc, p.Ref)
		if p.Covered != "" {
			desc += " (note: covered by " + p.Covered + " — the click may hit it)"
		}
		return p.X, p.Y, desc, nil
	}
	px, okx := a.Float("x")
	py, oky := a.Float("y")
	if !okx || !oky {
		return 0, 0, "", errors.New("give ref, selector, match, or x and y")
	}
	return px, py, fmt.Sprintf("at (%.0f, %.0f)", px, py), nil
}

func pageInfo(ctx context.Context, s *session.Session, drv browser.Driver, tab, prefix string) (*Result, error) {
	var info struct{ Title, URL string }
	err := s.Eval(ctx, drv, tab, page.Wrap("return {title: document.title, url: location.href};"), &info)
	if err != nil {
		return textResult("%s", prefix), nil
	}
	return textResult("%s\nTitle: %s\nURL: %s\nTab: %s", prefix, info.Title, info.URL, tab), nil
}

func navigatedSuffix(ctx context.Context, drv browser.Driver, tab string) string {
	url, err := drv.Evaluate(ctx, tab, "location.href")
	if err != nil {
		return ""
	}
	title, _ := drv.Evaluate(ctx, tab, "document.title")
	return fmt.Sprintf("; page is now %q (%s)", title, url)
}

func renderTabs(ctx context.Context, drv browser.Driver, current string) string {
	tabs, err := drv.Tabs(ctx)
	if err != nil {
		return "tabs: " + err.Error()
	}
	var b strings.Builder
	for _, t := range tabs {
		mark := " "
		if t.ID == current {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s %s  %q  %s\n", mark, t.ID, t.Title, t.URL)
	}
	return b.String()
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" || strings.Contains(u, "://") || strings.HasPrefix(u, "about:") || strings.HasPrefix(u, "data:") ||
		strings.HasPrefix(u, "file:") || strings.HasPrefix(u, "javascript:") || strings.HasPrefix(u, "chrome:") {
		return u
	}
	if strings.HasPrefix(u, "localhost") || strings.HasPrefix(u, "127.0.0.1") || strings.HasPrefix(u, "0.0.0.0") {
		return "http://" + u
	}
	return "https://" + u
}

func compileOpt(p string) (*regexp.Regexp, error) {
	if p == "" {
		return nil, nil
	}
	re, err := regexp.Compile("(?i)" + p)
	if err != nil {
		return nil, fmt.Errorf("bad pattern: %w", err)
	}
	return re, nil
}

func lastN(s []string, n int) []string {
	if n > 0 && len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func describeCond(c map[string]any) string {
	var parts []string
	for _, k := range []string{"text", "selector", "url"} {
		if v, _ := c[k].(string); v != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", k, v))
		}
	}
	if g, _ := c["gone"].(bool); g {
		parts = append(parts, "gone")
	}
	return strings.Join(parts, " ")
}
