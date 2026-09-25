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
		"Once":         "once",
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
	if s.Value() != daemon.DurationOnce {
		t.Fatalf("default = %q, want once", s.Value())
	}

	s.selectIndex(len(durationOptions) - 1) // Forever
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

// The countdown rides on the button the timeout would press, matching the
// Python UI's "Allow (12)", and the other button stays plain so which one is
// the default is unambiguous.
func TestButtonTextCarriesCountdownOnDefaultOnly(t *testing.T) {
	if got := buttonText("Allow", daemon.ActionAllow, daemon.ActionAllow, 12); got != "Allow (12)" {
		t.Errorf("default button = %q, want \"Allow (12)\"", got)
	}
	if got := buttonText("Deny", daemon.ActionDeny, daemon.ActionAllow, 12); got != "Deny" {
		t.Errorf("non-default button = %q, want plain \"Deny\"", got)
	}
	// And the other way round when deny is the default.
	if got := buttonText("Deny", daemon.ActionDeny, daemon.ActionDeny, 5); got != "Deny (5)" {
		t.Errorf("default button = %q, want \"Deny (5)\"", got)
	}
	if got := buttonText("Allow", daemon.ActionAllow, daemon.ActionDeny, 5); got != "Allow" {
		t.Errorf("non-default button = %q, want plain \"Allow\"", got)
	}
}

func TestSecondsLeftRoundsUp(t *testing.T) {
	cases := []struct {
		left time.Duration
		want int
	}{
		{15 * time.Second, 15},
		{1500 * time.Millisecond, 2},
		{200 * time.Millisecond, 1},
		{0, 0},
		{-5 * time.Second, 0},
	}
	for _, c := range cases {
		if got := secondsLeft(c.left); got != c.want {
			t.Errorf("secondsLeft(%v) = %d, want %d", c.left, got, c.want)
		}
	}
}

// "Once" must be offered, and first: it is the least committal choice and the
// one an unanswered prompt applies.
func TestOnceIsTheFirstDurationOption(t *testing.T) {
	if durationOptions[0].value != daemon.DurationOnce {
		t.Fatalf("first option is %q, want once", durationOptions[0].value)
	}
	s := newSegmented(durationOptions, FallbackDuration, nil)
	if s.Value() != daemon.DurationOnce {
		t.Fatalf("prompt preselects %q, want once", s.Value())
	}
}

// The tray offers allow and deny only: reject is rare, and a third item turns
// a quick switch into something to read. It stays valid from the flag.
func TestTrayOffersAllowAndDenyOnly(t *testing.T) {
	if got := len(trayActions); got != 2 {
		t.Fatalf("tray offers %d actions, want 2", got)
	}
	if trayActions[0] != daemon.ActionAllow || trayActions[1] != daemon.ActionDeny {
		t.Fatalf("tray actions = %v, want [allow deny]", trayActions)
	}
	if !validAction(daemon.ActionReject) {
		t.Error("reject should still be accepted from -default-action")
	}
}
