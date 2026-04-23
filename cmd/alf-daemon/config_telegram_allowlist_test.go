package main

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"
)

// captureLogParse runs fn with log output captured, and returns it.
func captureLogParse(fn func()) string {
	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// Regression guard for #385-3: the Telegram allowlist must reject
// malformed entries (logged) and only return ints. Callers treat an
// empty return as "do not start Telegram".
func TestParseAllowedChatIDs(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    map[int64]bool
		wantLog string // substring expected in log output ("" = no log expected)
	}{
		{name: "empty", in: "", want: map[int64]bool{}},
		{name: "single", in: "12345", want: map[int64]bool{12345: true}},
		{name: "multi", in: "1,2,3", want: map[int64]bool{1: true, 2: true, 3: true}},
		{name: "whitespace trimmed", in: " 42 , 43 ", want: map[int64]bool{42: true, 43: true}},
		{name: "negative IDs (groups)", in: "-1001", want: map[int64]bool{-1001: true}},
		{name: "trailing comma ignored silently", in: "1,", want: map[int64]bool{1: true}},
		{name: "empty segments ignored silently", in: "1,,,2", want: map[int64]bool{1: true, 2: true}},
		{name: "all garbage -> empty + log", in: "abc,xyz", want: map[int64]bool{}, wantLog: "invalid chat ID"},
		{name: "partial garbage -> surviving + log", in: "1,abc,2", want: map[int64]bool{1: true, 2: true}, wantLog: "invalid chat ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[int64]bool
			logs := captureLogParse(func() { got = parseAllowedChatIDs(tc.in) })
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if tc.wantLog == "" && logs != "" {
				t.Fatalf("expected no log output, got %q", logs)
			}
			if tc.wantLog != "" && !strings.Contains(logs, tc.wantLog) {
				t.Fatalf("expected log to contain %q, got %q", tc.wantLog, logs)
			}
		})
	}
}
