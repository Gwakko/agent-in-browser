package browser

import "testing"

func TestParseCombo(t *testing.T) {
	cases := []struct {
		in      string
		key     string
		mods    int
		wantErr bool
	}{
		{in: "Enter", key: "Enter"},
		{in: "a", key: "a"},
		{in: "Control+a", key: "a", mods: 2},
		{in: "ctrl+shift+K", key: "K", mods: 2 | 8},
		{in: "Meta+ArrowLeft", key: "ArrowLeft", mods: 4},
		{in: "esc", key: "Escape"},
		{in: "F5", key: "F5"},
		{in: "+", key: "+"},
		{in: "a+b", wantErr: true},
		{in: "Control+", wantErr: true},
		{in: "Bogus", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseCombo(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		mods := 0
		for _, m := range got.Modifiers {
			mods |= ModifierBit(m)
		}
		if got.Key.Name != c.key || mods != c.mods {
			t.Errorf("%q: got key %q mods %d, want %q %d", c.in, got.Key.Name, mods, c.key, c.mods)
		}
	}
}

func TestEnterHasTextAndWebDriverValue(t *testing.T) {
	k, _ := LookupKey("Enter")
	if k.Text != "\r" || k.WDValue != "" || k.VK != 13 {
		t.Fatalf("unexpected Enter: %+v", k)
	}
}
