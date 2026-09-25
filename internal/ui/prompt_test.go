package ui

import (
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Widget refresh needs a running app, so give the package a headless one.
func TestMain(m *testing.M) {
	test.NewApp()
	os.Exit(m.Run())
}

// The duration buttons must send the values opensnitchd parses, not the
// friendlier labels shown on them.
func TestDurationOptionsUseWireValues(t *testing.T) {
	want := map[string]string{
		"30 sec":       "30s",
		"5 min":        "5m",
		"1 hour":       "1h",
		"Until reboot": "until restart",
		"Forever":      "always",
	}
	if len(durationOptions) != len(want) {
		t.Fatalf("got %d options, want %d", len(durationOptions), len(want))
	}
	for _, opt := range durationOptions {
		w, ok := want[opt.label]
		if !ok {
			t.Errorf("unexpected option %q", opt.label)
			continue
		}
		if opt.value != w {
			t.Errorf("%q sends %q, want %q", opt.label, opt.value, w)
		}
	}
}

func TestSegmentedSelectsOneAtATime(t *testing.T) {
	s := newSegmented(durationOptions, daemon.DurationOnce, nil)
	// "once" is not offered, so it should fall back to the first option.
	if s.Value() != daemon.Duration30s {
		t.Fatalf("default = %q, want the first option", s.Value())
	}

	s.selectIndex(4) // Forever
	if s.Value() != daemon.DurationAlways {
		t.Fatalf("value = %q, want always", s.Value())
	}

	// Exactly one button may be highlighted.
	var high int
	for _, b := range s.buttons {
		if b.Importance == widget.HighImportance {
			high++
		}
	}
	if high != 1 {
		t.Fatalf("%d buttons highlighted, want exactly 1", high)
	}
}

func TestSegmentedHonoursPreselection(t *testing.T) {
	s := newSegmented(durationOptions, daemon.DurationAlways, nil)
	if s.Value() != daemon.DurationAlways {
		t.Fatalf("value = %q, want always", s.Value())
	}
}

func TestToggleFlips(t *testing.T) {
	tg := newToggle("port 443", false)
	if tg.On() {
		t.Fatal("should start off")
	}
	tg.button.OnTapped()
	if !tg.On() {
		t.Fatal("should be on after tap")
	}
	tg.button.OnTapped()
	if tg.On() {
		t.Fatal("should be off after second tap")
	}
}

// Toggle labels must name the value they pin to, so the user can see what
// they are enabling without reading the details block.
func TestToggleLabelShowsItsValue(t *testing.T) {
	tg := newToggle("port 443", true)
	if got := tg.button.Text; got == "" || !contains(got, "443") {
		t.Fatalf("label %q should contain the port", got)
	}
}

// The keys that could activate a focused button must be inert. A prompt can
// appear while the user is typing, and a stray Enter reaching Allow would be a
// silent security failure.
func TestPromptSwallowsActivationKeys(t *testing.T) {
	var answered bool
	handler := promptKeyHandler(func() { answered = true })

	for _, k := range []fyne.KeyName{fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace} {
		handler(&fyne.KeyEvent{Name: k})
		if answered {
			t.Fatalf("%v answered the prompt; it must be inert", k)
		}
	}

	handler(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if !answered {
		t.Fatal("Escape should dismiss the prompt")
	}
}

func TestDestLabelPrefersHostname(t *testing.T) {
	c := &protocol.Connection{DstHost: "example.com", DstIp: "1.2.3.4"}
	if got := destLabel(c); got != "example.com" {
		t.Fatalf("got %q, want the hostname", got)
	}
	if got := destLabel(&protocol.Connection{DstIp: "1.2.3.4"}); got != "1.2.3.4" {
		t.Fatalf("got %q, want the IP fallback", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The countdown must say what will happen, not just tick, and must never show
// a negative or confusing value as it lands on zero.
func TestCountdownText(t *testing.T) {
	cases := []struct {
		left time.Duration
		want string
	}{
		{15 * time.Second, "No answer in 15s → allow / once (default)"},
		{1500 * time.Millisecond, "No answer in 2s → allow / once (default)"},
		{200 * time.Millisecond, "No answer in 1s → allow / once (default)"},
		{0, "No answer in 0s → allow / once (default)"},
		{-5 * time.Second, "No answer in 0s → allow / once (default)"},
	}
	for _, c := range cases {
		if got := countdownText(c.left, "allow", "once"); got != c.want {
			t.Errorf("countdownText(%v) = %q, want %q", c.left, got, c.want)
		}
	}
}

// The countdown names the action that will actually be applied.
func TestCountdownNamesConfiguredDefault(t *testing.T) {
	got := countdownText(10*time.Second, "deny", "once")
	if !contains(got, "deny") {
		t.Fatalf("countdown %q should name the deny default", got)
	}
}
