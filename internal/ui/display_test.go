package ui

import (
	"strings"
	"testing"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

func TestSafeTextStripsControls(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"/usr/bin/curl", "/usr/bin/curl"},
		{"a\nb", "a�b"},
		{"a\rb", "a�b"},
		{"a\tb", "a�b"},
		{"a\x00b", "a�b"},
		{"a\x1b[31mb", "a�[31mb"}, // ANSI escape defanged
		{"ab‮cd", "ab�cd"},        // bidi override
		{"héllo→", "héllo→"},      // ordinary non-ASCII survives
	}
	for _, c := range cases {
		if got := safeText(c.in); got != c.want {
			t.Errorf("safeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSafeTextIsAlwaysOneLine(t *testing.T) {
	for _, s := range []string{"a\nb\nc", "\n\n\n", "x\r\ny"} {
		if got := safeText(s); strings.ContainsAny(got, "\n\r") {
			t.Errorf("safeText(%q) = %q, still multi-line", s, got)
		}
	}
}

func TestSafeTextCaps(t *testing.T) {
	got := safeText(strings.Repeat("a", maxDisplayLen*3))
	if n := len([]rune(got)); n != maxDisplayLen+1 { // +1 for the ellipsis
		t.Errorf("length %d runes, want %d", n, maxDisplayLen+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncation is not marked")
	}
	// A short string must not be marked as truncated.
	if strings.HasSuffix(safeText("short"), "…") {
		t.Error("short string marked truncated")
	}
}

// The prompt is a block of aligned "Field: value" lines, so a newline in any
// attacker-controlled value would forge the lines below it and misattribute
// the connection to a binary that is not making it.
func TestPromptCannotBeForged(t *testing.T) {
	evil := &protocol.Connection{
		ProcessPath: "/usr/bin/curl",
		DstHost:     "safe.example.com\n\nPath:        /usr/bin/totally-trusted",
		DstPort:     443,
		Protocol:    "tcp",
		ProcessArgs: []string{"--x\nUser: 0"},
	}

	detail := promptDetail(evil)
	// One line per real field: Path, Destination, PID, Command. The injected
	// text may still appear as characters inside a line - that is unavoidable
	// and harmless - but it must not be able to start a line of its own.
	lines := strings.Split(detail, "\n")
	if len(lines) != 4 {
		t.Errorf("detail has %d lines, want 4:\n%s", len(lines), detail)
	}
	for i, l := range lines {
		want := []string{"Path:", "Destination:", "PID:", "Command:"}[i]
		if !strings.HasPrefix(l, want) {
			t.Errorf("line %d = %q, want it to start with %q", i, l, want)
		}
	}

	if h := promptHeadline(evil); strings.ContainsAny(h, "\n\r") {
		t.Errorf("headline spans lines: %q", h)
	}
}

// filepath.Base can return a name that is nothing but a newline, which used to
// produce a headline beginning with a blank line and the attacker's text.
func TestProcessNameFallsBackOnBlankName(t *testing.T) {
	c := &protocol.Connection{ProcessPath: "/tmp/x/\nAllow forever?", ProcessId: 42}
	name := processName(c)
	if strings.ContainsAny(name, "\n\r") {
		t.Errorf("processName = %q, still multi-line", name)
	}
	c2 := &protocol.Connection{ProcessPath: "/tmp/x/\n", ProcessId: 42}
	if got := processName(c2); got != "process 42" {
		t.Errorf("processName = %q, want the pid fallback", got)
	}
}

// Sanitising is display-only; the rule must still pin to the real host.
func TestSanitisingDoesNotWeakenRules(t *testing.T) {
	c := &protocol.Connection{ProcessPath: "/usr/bin/curl", DstHost: "evil\n.example.com"}
	if got := destLabel(c); strings.ContainsAny(got, "\n\r") {
		t.Errorf("destLabel = %q", got)
	}
	if c.GetDstHost() != "evil\n.example.com" {
		t.Error("the connection itself was mutated")
	}
}
