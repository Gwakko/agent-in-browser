package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestParseToolArgs(t *testing.T) {
	got, err := parseToolArgs([]string{"url=example.com", "n=3", "ok=true", "text=007", "ids=[1,2]"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"url": "example.com", "n": float64(3), "ok": true, "text": "007", "ids": []any{float64(1), float64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
	got, _ = parseToolArgs([]string{`{"fields":[{"ref":"e1","value":"x"}]}`})
	if _, ok := got["fields"].([]any); !ok {
		t.Errorf("json form: %#v", got)
	}
	if _, err := parseToolArgs([]string{"novalue"}); err == nil {
		t.Error("want error for bare word")
	}
}

func TestParseInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	h := fs.Bool("headless", false, "")
	pos := parseInterleaved(fs, []string{"snapshot", "--headless", "mode=all"})
	if !*h || !reflect.DeepEqual(pos, []string{"snapshot", "mode=all"}) {
		t.Errorf("headless=%v pos=%v", *h, pos)
	}
}
