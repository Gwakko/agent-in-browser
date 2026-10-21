package browser

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Key describes one key in protocol-neutral terms.
type Key struct {
	Name    string // DOM KeyboardEvent.key, e.g. "Enter", "a"
	Code    string // DOM KeyboardEvent.code, e.g. "Enter", "KeyA"
	VK      int    // Windows virtual key code (CDP)
	Text    string // text the key produces, if any
	WDValue string // WebDriver key value (BiDi / classic)
}

// Combo is a parsed key combination like "Control+Shift+a".
type Combo struct {
	Modifiers []Key
	Key       Key
}

var specialKeys = map[string]Key{
	"enter":      {Name: "Enter", Code: "Enter", VK: 13, Text: "\r", WDValue: ""},
	"tab":        {Name: "Tab", Code: "Tab", VK: 9, WDValue: ""},
	"escape":     {Name: "Escape", Code: "Escape", VK: 27, WDValue: ""},
	"backspace":  {Name: "Backspace", Code: "Backspace", VK: 8, WDValue: ""},
	"delete":     {Name: "Delete", Code: "Delete", VK: 46, WDValue: ""},
	"space":      {Name: " ", Code: "Space", VK: 32, Text: " ", WDValue: " "},
	"arrowup":    {Name: "ArrowUp", Code: "ArrowUp", VK: 38, WDValue: ""},
	"arrowdown":  {Name: "ArrowDown", Code: "ArrowDown", VK: 40, WDValue: ""},
	"arrowleft":  {Name: "ArrowLeft", Code: "ArrowLeft", VK: 37, WDValue: ""},
	"arrowright": {Name: "ArrowRight", Code: "ArrowRight", VK: 39, WDValue: ""},
	"home":       {Name: "Home", Code: "Home", VK: 36, WDValue: ""},
	"end":        {Name: "End", Code: "End", VK: 35, WDValue: ""},
	"pageup":     {Name: "PageUp", Code: "PageUp", VK: 33, WDValue: ""},
	"pagedown":   {Name: "PageDown", Code: "PageDown", VK: 34, WDValue: ""},
	"shift":      {Name: "Shift", Code: "ShiftLeft", VK: 16, WDValue: ""},
	"control":    {Name: "Control", Code: "ControlLeft", VK: 17, WDValue: ""},
	"alt":        {Name: "Alt", Code: "AltLeft", VK: 18, WDValue: ""},
	"meta":       {Name: "Meta", Code: "MetaLeft", VK: 91, WDValue: ""},
}

var keyAliases = map[string]string{
	"return": "enter", "esc": "escape", "del": "delete", "up": "arrowup", "down": "arrowdown",
	"left": "arrowleft", "right": "arrowright", "ctrl": "control", "cmd": "meta", "command": "meta",
	"super": "meta", "option": "alt", " ": "space",
}

// ModifierBit returns the CDP modifier bit for a modifier key (Alt=1, Ctrl=2, Meta=4, Shift=8).
func ModifierBit(k Key) int {
	switch k.Name {
	case "Alt":
		return 1
	case "Control":
		return 2
	case "Meta":
		return 4
	case "Shift":
		return 8
	}
	return 0
}

// LookupKey resolves a single key name or character.
func LookupKey(name string) (Key, error) {
	lower := strings.ToLower(name)
	if a, ok := keyAliases[lower]; ok {
		lower = a
	}
	if k, ok := specialKeys[lower]; ok {
		return k, nil
	}
	if len(lower) >= 2 && lower[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(lower, "f%d", &n); err == nil && n >= 1 && n <= 12 {
			return Key{Name: "F" + fmt.Sprint(n), Code: "F" + fmt.Sprint(n), VK: 111 + n, WDValue: string(rune(0xE031 + n - 1))}, nil
		}
	}
	if utf8.RuneCountInString(name) == 1 {
		return CharKey([]rune(name)[0]), nil
	}
	return Key{}, fmt.Errorf("unknown key %q", name)
}

// CharKey builds a Key for a printable character.
func CharKey(r rune) Key {
	k := Key{Name: string(r), Text: string(r), WDValue: string(r)}
	switch {
	case r >= 'a' && r <= 'z':
		k.Code, k.VK = "Key"+strings.ToUpper(string(r)), int(r-'a'+'A')
	case r >= 'A' && r <= 'Z':
		k.Code, k.VK = "Key"+string(r), int(r)
	case r >= '0' && r <= '9':
		k.Code, k.VK = "Digit"+string(r), int(r)
	}
	return k
}

// ParseCombo parses "Enter", "a", "Control+a", "Meta+Shift+ArrowLeft".
func ParseCombo(s string) (Combo, error) {
	s = strings.TrimSpace(s)
	if s == "+" {
		return Combo{Key: CharKey('+')}, nil
	}
	parts := strings.Split(s, "+")
	var c Combo
	for i, p := range parts {
		if p == "" {
			return c, fmt.Errorf("invalid key combination %q", s)
		}
		k, err := LookupKey(p)
		if err != nil {
			return c, err
		}
		if i < len(parts)-1 {
			if ModifierBit(k) == 0 {
				return c, fmt.Errorf("%q is not a modifier in %q", p, s)
			}
			c.Modifiers = append(c.Modifiers, k)
			continue
		}
		c.Key = k
	}
	return c, nil
}
