package hibana

import (
	"strings"
	"testing"
	"time"
)

func TestEventRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 25, 19, 11, 29, 0, time.UTC)
	cases := []Event{
		addEv(NewID(), now, "hello"),
		delEv(NewID(), now),
		addEvAt(NewID(), now.Add(-time.Hour), now, "with\nnewlines\tand other"),
	}
	for i, ev := range cases {
		line, err := ev.MarshalLine()
		if err != nil {
			t.Fatalf("[%d] marshal: %v", i, err)
		}
		if !strings.HasSuffix(string(line), "\n") {
			t.Fatalf("[%d] line missing newline", i)
		}
		got, err := ParseEvent(line)
		if err != nil {
			t.Fatalf("[%d] parse: %v", i, err)
		}
		if got.EventID != ev.EventID || got.NoteID != ev.NoteID || got.Op != ev.Op || got.Text != ev.Text {
			t.Errorf("[%d] mismatch: got=%+v want=%+v", i, got, ev)
		}
		if !got.TS.Equal(ev.TS) || !got.CreatedAt.Equal(ev.CreatedAt) {
			t.Errorf("[%d] time mismatch", i)
		}
	}
}

func TestParseEventRejectsBadLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"not json", "hello world\n"},
		{"missing eid", `{"nid":"01HXX1234567890ABCDEFGHJKM","op":"add","ts":"2026-04-25T19:11:29Z","text":"x"}`},
		{"missing nid", `{"eid":"01HXX1234567890ABCDEFGHJKM","op":"add","ts":"2026-04-25T19:11:29Z","text":"x"}`},
		{"bad op", `{"eid":"01HXX1234567890ABCDEFGHJKM","nid":"01HXX1234567890ABCDEFGHJKM","op":"reorder","ts":"2026-04-25T19:11:29Z"}`},
		{"short eid", `{"eid":"too-short","nid":"01HXX1234567890ABCDEFGHJKM","op":"add","ts":"2026-04-25T19:11:29Z"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseEvent([]byte(c.in)); err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
		})
	}
}

func TestParseEmptyLineSentinel(t *testing.T) {
	if _, err := ParseEvent([]byte("\n")); err == nil {
		t.Fatalf("expected sentinel error for blank line")
	}
}
