package tui

import (
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{"simple words", "mv #101 done", []string{"mv", "#101", "done"}},
		{"double quoted string", `c "hello world"`, []string{"c", "hello world"}},
		{"single quoted string", `c 'hello world'`, []string{"c", "hello world"}},
		{"mixed quotes and tokens", `mv #101 "in progress"`, []string{"mv", "#101", "in progress"}},
		{"empty string", "", nil},
		{"only spaces", "   ", nil},
		{"tabs and spaces", " \t  ", nil},
		{"unmatched quote", `c "hello`, []string{"c", "hello"}},
		{"adjacent quoted tokens", `"ab" "cd"`, []string{"ab", "cd"}},
		{"empty quotes", `a "" b`, []string{"a", "b"}},
		{"single char", "q", []string{"q"}},
		{"leading trailing spaces", "  plan  ", []string{"plan"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenize(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("tokenize(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.expect, len(tt.expect))
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("tokenize(%q)[%d] = %q, want %q",
						tt.input, i, got[i], tt.expect[i])
				}
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantNil    bool
		wantAction string
		wantArgs   []string
		wantRaw    string
	}{
		{"empty", "", true, "", nil, ""},
		{"whitespace only", "   ", true, "", nil, ""},
		{"simple command", "plan", false, "plan", nil, "plan"},
		{"command with args", "mv #101 done", false, "mv", []string{"#101", "done"}, "mv #101 done"},
		{"case insensitive action", "MV #101 Done", false, "mv", []string{"#101", "Done"}, "MV #101 Done"},
		{"quoted args", `c "hello world"`, false, "c", []string{"hello world"}, `c "hello world"`},
		{"help", "h", false, "h", nil, "h"},
		{"line number", "2", false, "2", nil, "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCommand(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("parseCommand(%q) = %+v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseCommand(%q) = nil, want non-nil", tt.input)
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tt.wantAction)
			}
			if got.Raw != tt.wantRaw {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.wantRaw)
			}
			if len(got.Args) != len(tt.wantArgs) {
				t.Fatalf("Args = %v (len %d), want %v (len %d)",
					got.Args, len(got.Args), tt.wantArgs, len(tt.wantArgs))
			}
			for i := range got.Args {
				if got.Args[i] != tt.wantArgs[i] {
					t.Errorf("Args[%d] = %q, want %q", i, got.Args[i], tt.wantArgs[i])
				}
			}
		})
	}
}
