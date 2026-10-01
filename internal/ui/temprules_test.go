package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
)

func TestExpiryTextCountdown(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		left time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m 30s"},
		{5 * time.Minute, "5m 0s"},
		{90 * time.Minute, "1h 30m"},
	}
	for _, c := range cases {
		r := daemon.TempRule{ExpiresAt: now.Add(c.left)}
		if got := expiryText(r, now); got != c.want {
			t.Errorf("left %v: got %q, want %q", c.left, got, c.want)
		}
	}
}

// A blank or zero time here would read as "never expires", which is the
// opposite of what "until restart" means.
func TestExpiryTextUntilRestart(t *testing.T) {
	r := daemon.TempRule{Rule: daemon.Rule{Duration: daemon.DurationUntilRestart}}
	if got := expiryText(r, time.Now()); got != "on restart" {
		t.Errorf("got %q", got)
	}
}

func TestExpiryTextDue(t *testing.T) {
	now := time.Now()
	r := daemon.TempRule{ExpiresAt: now.Add(-time.Second)}
	if got := expiryText(r, now); got != "now" {
		t.Errorf("got %q", got)
	}
}

// Same threat model as the rules table: the process path came from the
// connection being judged, so a newline in it could forge a row.
func TestTempRuleCellTextSanitises(t *testing.T) {
	r := daemon.TempRule{Rule: daemon.Rule{
		Action:  "allow\ndeny",
		Process: "/usr/bin/ev\nil",
	}}
	for _, col := range []int{1, 3} {
		got := tempRuleCellText(r, col, time.Now())
		if strings.ContainsAny(got, "\n\r\t") {
			t.Errorf("col %d = %q still has control characters", col, got)
		}
	}
}

func TestTempRuleCellTextEmptyProcess(t *testing.T) {
	r := daemon.TempRule{}
	if got := tempRuleCellText(r, 3, time.Now()); got != "—" {
		t.Errorf("got %q, want an em dash", got)
	}
}

func TestTempRuleCellTextShowsBasename(t *testing.T) {
	r := daemon.TempRule{Rule: daemon.Rule{Process: "/usr/bin/curl"}}
	if got := tempRuleCellText(r, 3, time.Now()); got != "curl" {
		t.Errorf("got %q, want curl", got)
	}
}

func TestTempRuleCount(t *testing.T) {
	if got := tempRuleCount(1); got != "1 temporary rule" {
		t.Errorf("got %q", got)
	}
	if got := tempRuleCount(3); got != "3 temporary rules" {
		t.Errorf("got %q", got)
	}
}

// Every column must have a width, or the table renders one at Fyne's default
// and the layout drifts from the header.
func TestTempRuleColumnsHaveWidths(t *testing.T) {
	for _, c := range tempRuleColumns {
		if c.title == "" {
			t.Error("column with no title")
		}
		if c.width <= 0 {
			t.Errorf("%s has width %v", c.title, c.width)
		}
	}
}

// newTestTempView builds the view over a real store, the way App does.
func newTestTempView(t *testing.T) (*tempRulesView, *daemon.TempStore) {
	t.Helper()
	store := daemon.NewTempStore(0)
	v := newTempRulesView(&App{}, store)
	v.content() // creates the Delete button the view refers to
	return v, store
}

// The table must have a header row plus one row per rule, or the body callback
// indexes past the slice.
func TestTempViewRowCount(t *testing.T) {
	v, store := newTestTempView(t)
	if rows, cols := v.table.Length(); rows != 1 || cols != len(tempRuleColumns) {
		t.Fatalf("empty: got %dx%d, want 1x%d", rows, cols, len(tempRuleColumns))
	}

	now := time.Now()
	store.Add(&daemon.TempRule{
		Rule:      daemon.Rule{Name: "a", Duration: daemon.Duration1h, Process: "/usr/bin/curl"},
		ExpiresAt: now.Add(time.Hour),
	})
	v.refresh()

	if rows, _ := v.table.Length(); rows != 2 {
		t.Fatalf("one rule: got %d rows, want 2", rows)
	}
}

// Rules expire out from under the list, so an index is not a stable handle on
// the selection: it must follow the rule it was pointing at.
func TestTempViewSelectionFollowsRule(t *testing.T) {
	v, store := newTestTempView(t)
	now := time.Now()
	store.Add(&daemon.TempRule{
		Rule:      daemon.Rule{Name: "soon", Duration: daemon.Duration30s},
		ExpiresAt: now.Add(30 * time.Second),
	})
	store.Add(&daemon.TempRule{
		Rule:      daemon.Rule{Name: "later", Duration: daemon.Duration1h},
		ExpiresAt: now.Add(time.Hour),
	})
	v.refresh()

	// Select "later", which sorts second behind the sooner rule.
	if v.rules[1].Name != "later" {
		t.Fatalf("rules = %v, want later second", v.rules)
	}
	v.selected = 1

	// "soon" lapses; "later" is now at index 0 and must still be selected.
	store.Remove("soon")
	v.refresh()

	if len(v.rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(v.rules))
	}
	if v.selected != 0 || v.rules[v.selected].Name != "later" {
		t.Errorf("selected = %d (%v), want the later rule", v.selected, v.rules)
	}
}

// If the selected rule expires there is nothing to delete, so the selection
// must clear rather than silently pointing at whatever took its place.
func TestTempViewSelectionClearsWhenRuleExpires(t *testing.T) {
	v, store := newTestTempView(t)
	now := time.Now()
	store.Add(&daemon.TempRule{
		Rule:      daemon.Rule{Name: "gone", Duration: daemon.Duration30s},
		ExpiresAt: now.Add(30 * time.Second),
	})
	store.Add(&daemon.TempRule{
		Rule:      daemon.Rule{Name: "stays", Duration: daemon.Duration1h},
		ExpiresAt: now.Add(time.Hour),
	})
	v.refresh()
	v.selected = 0
	if v.rules[0].Name != "gone" {
		t.Fatalf("rules = %v", v.rules)
	}

	store.Remove("gone")
	v.refresh()

	if v.selected != -1 {
		t.Errorf("selected = %d, want -1 after the rule went", v.selected)
	}
}

// An empty list is the normal state, so it should say why rather than looking
// like a failure to load.
func TestTempViewEmptyStatusExplains(t *testing.T) {
	v, _ := newTestTempView(t)
	v.refresh()
	got, err := v.status.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "No temporary rules") {
		t.Errorf("status = %q", got)
	}
}

func TestTempViewStatusCountsRules(t *testing.T) {
	v, store := newTestTempView(t)
	store.Add(&daemon.TempRule{
		Rule:      daemon.Rule{Name: "a", Duration: daemon.Duration1h},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	v.refresh()
	got, err := v.status.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1 temporary rule") {
		t.Errorf("status = %q", got)
	}
}
