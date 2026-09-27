package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Connections whose text has historically decided the prompt's width.
var layoutCases = map[string]*protocol.Connection{
	"typical": {
		ProcessPath: "/usr/bin/curl", DstHost: "example.com",
		DstPort: 443, Protocol: "tcp", ProcessId: 1234, UserId: 1000,
	},
	"long command line": {
		ProcessPath: "/home/robert/.local/share/claude/versions/2.1.220/claude",
		DstHost:     "api.anthropic.com", DstIp: "160.79.104.10",
		DstPort: 443, Protocol: "tcp", ProcessId: 12345, UserId: 1000,
		ProcessArgs: []string{"/usr/bin/node", "--max-old-space-size=8192",
			"/home/robert/.local/share/claude/versions/2.1.220/cli.js", "--resume", "--verbose"},
	},
	"long everything": {
		ProcessPath: "/" + strings.Repeat("verylongdir/", 15) + "bin",
		DstHost:     strings.Repeat("sub.", 25) + "example.com",
		DstPort:     443, Protocol: "tcp", ProcessId: 9, UserId: 1000,
		ProcessArgs: []string{strings.Repeat("--flag=value ", 40)},
	},
	"no hostname or args": {
		ProcessPath: "/bin/sh", DstIp: "1.1.1.1", DstPort: 53, Protocol: "udp",
	},
}

// The prompt must fit the window it asks for.
//
// A window cannot be smaller than its content's MinSize, and an unwrapped
// label reports a MinSize as wide as its longest line. A long command line
// therefore made the window wider than the content drawn in it, leaving a
// strip of desktop showing down the right-hand side. Nothing in the prompt
// may let connection text decide the width.
func TestPromptContentFitsItsWindow(t *testing.T) {
	for name, conn := range layoutCases {
		t.Run(name, func(t *testing.T) {
			got := promptContent(conn, testButtons()).MinSize()
			if got.Width > promptWidth {
				t.Errorf("content wants %.0fpx, wider than the %.0fpx window",
					got.Width, float32(promptWidth))
			}
			if got.Height > promptHeight {
				t.Errorf("content wants %.0fpx tall, more than the %.0fpx window",
					got.Height, float32(promptHeight))
			}
		})
	}
}

// Whatever the connection text, the prompt should be about the same size:
// if content still drives the width, this spread grows with it.
func TestPromptWidthIsStableAcrossConnections(t *testing.T) {
	var min, max float32 = 1 << 20, 0
	for _, conn := range layoutCases {
		w := promptContent(conn, testButtons()).MinSize().Width
		if w < min {
			min = w
		}
		if w > max {
			max = w
		}
	}
	if spread := max - min; spread > 80 {
		t.Errorf("width varies by %.0fpx across connections (%.0f..%.0f); "+
			"connection text still drives the layout", spread, min, max)
	}
}

// The scope toggle shows the value it pins to, so it must not carry an
// unbounded hostname onto a button.
func TestScopeToggleLabelIsBounded(t *testing.T) {
	long := strings.Repeat("sub.", 25) + "example.com"
	tg := newToggle(long, false, nil)
	if n := len([]rune(tg.label)); n > maxToggleLabel {
		t.Errorf("toggle label is %d runes, want at most %d", n, maxToggleLabel)
	}
	if !strings.Contains(tg.label, "…") {
		t.Errorf("a truncated label should say so: %q", tg.label)
	}
	// A short label is left exactly as given.
	if tg := newToggle("port 443", false, nil); tg.label != "port 443" {
		t.Errorf("short label was altered: %q", tg.label)
	}
}

func TestElide(t *testing.T) {
	// Anything at or under the cap is returned untouched.
	for _, s := range []string{"", "short", "exactly-10"} {
		if got := elide(s, 10); got != s {
			t.Errorf("elide(%q, 10) = %q, want it unchanged", s, got)
		}
	}

	// Anything longer comes back at exactly the cap, cut in the middle so
	// both ends of a hostname survive.
	got := elide("aaaaaaaaaabbbbbbbbbbcccccccccc", 12)
	if n := len([]rune(got)); n != 12 {
		t.Errorf("elide to 12 gave %d runes: %q", n, got)
	}
	if !strings.HasPrefix(got, "aaa") || !strings.HasSuffix(got, "ccc") {
		t.Errorf("elide should keep both ends, got %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("elide should mark the cut, got %q", got)
	}
}

// testButtons supplies the interactive widgets promptContent needs. Their
// behaviour is buildPrompt's business; the layout only needs them to exist.
func testButtons() promptParts {
	return promptParts{
		duration: newSegmented(durationOptions, FallbackDuration, nil),
		dest:     newToggle("example.com", false, nil),
		port:     newToggle("port 443", false, nil),
		user:     newToggle("user 1000", false, nil),
		deny:     widget.NewButton("Deny", nil),
		allow:    widget.NewButton("Allow (60)", nil),
	}
}
