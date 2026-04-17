package model

import (
	"reflect"
	"testing"
)

func TestConfig_TeamLogins(t *testing.T) {
	cfg := Config{
		Team: []TeamMember{
			{Login: "alice"},
			{Login: "bob"},
		},
	}

	got := cfg.TeamLogins()
	want := []string{"alice", "bob"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TeamLogins() = %v, want %v", got, want)
	}
}

func TestConfig_FocusSet(t *testing.T) {
	cfg := Config{
		Team: []TeamMember{
			{Login: "alice", Focus: []int{101, 202}},
			{Login: "bob"},
		},
	}

	got := cfg.FocusSet("alice")
	if got == nil {
		t.Fatal("FocusSet(alice) returned nil")
	}
	if !got[101] || !got[202] {
		t.Fatalf("FocusSet(alice) = %v, want keys 101 and 202", got)
	}
	if got[999] {
		t.Fatalf("FocusSet(alice) unexpectedly contains 999: %v", got)
	}
	if cfg.FocusSet("bob") != nil {
		t.Fatal("FocusSet(bob) should be nil when no personal focus exists")
	}
	if cfg.FocusSet("nobody") != nil {
		t.Fatal("FocusSet(nobody) should be nil for unknown user")
	}
}

func TestConfig_DisplayName(t *testing.T) {
	cfg := Config{
		Team: []TeamMember{
			{Login: "alice", Name: "Alice Smith"},
			{Login: "bob"},
		},
	}

	if got := cfg.DisplayName("alice"); got != "Alice Smith" {
		t.Fatalf("DisplayName(alice) = %q, want %q", got, "Alice Smith")
	}
	if got := cfg.DisplayName("bob"); got != "bob" {
		t.Fatalf("DisplayName(bob) = %q, want %q", got, "bob")
	}
	if got := cfg.DisplayName("nobody"); got != "nobody" {
		t.Fatalf("DisplayName(nobody) = %q, want %q", got, "nobody")
	}
}
