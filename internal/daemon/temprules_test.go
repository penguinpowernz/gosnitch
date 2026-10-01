package daemon

import (
	"testing"
	"time"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

func tempConn() *protocol.Connection {
	return &protocol.Connection{
		Protocol: "tcp", DstIp: "1.2.3.4", DstHost: "example.com", DstPort: 443,
		UserId: 1000, ProcessId: 42, ProcessPath: "/usr/bin/curl",
	}
}

// record runs a decision through the same path AskRule uses.
func record(t *testing.T, ts *TempStore, d Decision, at time.Time) *protocol.Rule {
	t.Helper()
	r := BuildRule(tempConn(), d)
	ts.Record(r, d, at)
	return r
}

func TestIsTemporary(t *testing.T) {
	for _, d := range []string{DurationOnce, Duration30s, Duration5m, Duration1h, DurationUntilRestart} {
		if !IsTemporary(d) {
			t.Errorf("%q should be temporary", d)
		}
	}
	if IsTemporary(DurationAlways) {
		t.Error("always should not be temporary")
	}
}

// Only "always" rules reach the rules directory, so anything else must be
// recorded here or it cannot be inspected at all.
func TestRecordIgnoresAlways(t *testing.T) {
	ts := NewTempStore(0)
	record(t, ts, Decision{Action: ActionAllow, Duration: DurationAlways}, time.Now())
	if got := ts.Snapshot(time.Now()); len(got) != 0 {
		t.Fatalf("got %d rules, want 0", len(got))
	}
}

// "once" is spent by the daemon on the connection that created it, so listing
// one would only offer a delete that cannot apply to anything.
func TestRecordIgnoresOnce(t *testing.T) {
	ts := NewTempStore(0)
	record(t, ts, Decision{Action: ActionAllow, Duration: DurationOnce}, time.Now())
	if got := ts.Snapshot(time.Now()); len(got) != 0 {
		t.Fatalf("got %d rules, want 0", len(got))
	}
}

func TestRecordComputesExpiry(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		duration string
		want     time.Duration
	}{
		{Duration30s, 30 * time.Second},
		{Duration5m, 5 * time.Minute},
		{Duration15m, 15 * time.Minute},
		{Duration30m, 30 * time.Minute},
		{Duration1h, time.Hour},
	}
	for _, c := range cases {
		ts := NewTempStore(0)
		record(t, ts, Decision{Action: ActionAllow, Duration: c.duration}, now)
		got := ts.Snapshot(now)
		if len(got) != 1 {
			t.Fatalf("%s: got %d rules", c.duration, len(got))
		}
		if !got[0].HasExpiry() {
			t.Fatalf("%s: no expiry", c.duration)
		}
		if want := now.Add(c.want); !got[0].ExpiresAt.Equal(want) {
			t.Errorf("%s: expiry = %v, want %v", c.duration, got[0].ExpiresAt, want)
		}
	}
}

// "until restart" expires at a moment only the daemon knows, so it must not be
// given a computed time that would read as a countdown.
func TestUntilRestartHasNoExpiry(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	record(t, ts, Decision{Action: ActionDeny, Duration: DurationUntilRestart}, now)
	got := ts.Snapshot(now)
	if len(got) != 1 {
		t.Fatalf("got %d rules", len(got))
	}
	if got[0].HasExpiry() {
		t.Error("until restart should have no expiry")
	}
	if got[0].Remaining(now) != 0 {
		t.Error("remaining should be zero without an expiry")
	}
}

// The daemon has already dropped an expired rule, so the table must not keep
// offering a Delete for it.
func TestSnapshotDropsExpired(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	record(t, ts, Decision{Action: ActionAllow, Duration: Duration30s}, now)

	if got := ts.Snapshot(now.Add(29 * time.Second)); len(got) != 1 {
		t.Fatalf("before expiry: got %d rules, want 1", len(got))
	}
	if got := ts.Snapshot(now.Add(30 * time.Second)); len(got) != 0 {
		t.Fatalf("at expiry: got %d rules, want 0", len(got))
	}
	// The drop must be permanent, not just filtered from that one view.
	if got := ts.Snapshot(now); len(got) != 0 {
		t.Fatalf("after expiry: got %d rules, want 0", len(got))
	}
}

func TestSnapshotSortsSoonestFirst(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	// Added longest-first so the sort has something to do.
	ts.Add(&TempRule{Rule: Rule{Name: "hour", Duration: Duration1h}, ExpiresAt: now.Add(time.Hour)})
	ts.Add(&TempRule{Rule: Rule{Name: "restart", Duration: DurationUntilRestart}})
	ts.Add(&TempRule{Rule: Rule{Name: "short", Duration: Duration30s}, ExpiresAt: now.Add(30 * time.Second)})

	got := ts.Snapshot(now)
	want := []string{"short", "hour", "restart"}
	if len(got) != len(want) {
		t.Fatalf("got %d rules, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

// Re-answering a prompt whose rule is still live must not overwrite the
// existing entry.
//
// The daemon renames the second rule to "<name>-2" (setUniqueName) instead of
// replacing the first, so refreshing the expiry under the original name would
// aim the tab's Delete at a name the daemon does not hold - and since
// Loader.Delete returns nil for an unknown name, the daemon would answer OK
// having deleted nothing.
func TestRecordSameNameKeepsKnownGoodEntry(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	d := Decision{Action: ActionAllow, Duration: Duration30s}
	record(t, ts, d, now)
	record(t, ts, d, now.Add(20*time.Second))

	got := ts.Snapshot(now.Add(20 * time.Second))
	if len(got) != 1 {
		t.Fatalf("got %d rules, want 1", len(got))
	}
	if want := now.Add(30 * time.Second); !got[0].ExpiresAt.Equal(want) {
		t.Errorf("expiry = %v, want %v (the first record, left alone)", got[0].ExpiresAt, want)
	}
}

// A daemon that subscribes has rebuilt its loader from disk, so anything that
// only lasted a daemon lifetime is gone.
func TestDropUntilRestart(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	ts.Add(&TempRule{Rule: Rule{Name: "restart", Duration: DurationUntilRestart}})
	ts.Add(&TempRule{Rule: Rule{Name: "timed", Duration: Duration1h}, ExpiresAt: now.Add(time.Hour)})

	ts.DropUntilRestart()

	got := ts.Snapshot(now)
	if len(got) != 1 || got[0].Name != "timed" {
		t.Fatalf("got %v, want only the timed rule", got)
	}
}

// Subscribe is where the daemon says it has restarted.
func TestSubscribeDropsUntilRestart(t *testing.T) {
	ts := NewTempStore(0)
	s := NewServer(NewStore(10), ActionAllow, DurationOnce, "test")
	s.SetTempStore(ts)
	ts.Add(&TempRule{Rule: Rule{Name: "restart", Duration: DurationUntilRestart}})

	if _, err := s.Subscribe(t.Context(), &protocol.ClientConfig{Version: "1.5.8"}); err != nil {
		t.Fatal(err)
	}
	if got := ts.Snapshot(time.Now()); len(got) != 0 {
		t.Fatalf("got %d rules, want 0 after restart", len(got))
	}
}

// The list must show what the rule is actually scoped to, which is what
// BuildRule decided, not what the connection happened to carry.
func TestRecordFlattensListOperands(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	record(t, ts, Decision{
		Action: ActionAllow, Duration: Duration5m,
		Scope: Scope{Dest: true, Port: true, User: true},
	}, now)

	got := ts.Snapshot(now)
	if len(got) != 1 {
		t.Fatalf("got %d rules", len(got))
	}
	r := got[0]
	if r.Process != "/usr/bin/curl" {
		t.Errorf("process = %q", r.Process)
	}
	if r.Dest != "example.com" {
		t.Errorf("dest = %q", r.Dest)
	}
	if r.Port != "443" {
		t.Errorf("port = %q", r.Port)
	}
	if r.UserID != "1000" {
		t.Errorf("uid = %q", r.UserID)
	}
}

// A process-only rule is a "simple" rule, whose operand is read straight off
// the Operator rather than out of JSON.
func TestRecordFlattensSimpleOperand(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	record(t, ts, Decision{Action: ActionDeny, Duration: Duration1h}, now)

	got := ts.Snapshot(now)
	if len(got) != 1 {
		t.Fatalf("got %d rules", len(got))
	}
	if got[0].Process != "/usr/bin/curl" {
		t.Errorf("process = %q", got[0].Process)
	}
	if got[0].Dest != "" {
		t.Errorf("dest = %q, want empty: the rule is not scoped to it", got[0].Dest)
	}
}

// Any local process can drive AskRule, so the list must not grow without bound.
func TestStoreBoundsGrowth(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(3)
	for i := 0; i < 10; i++ {
		ts.Add(&TempRule{
			Rule:      Rule{Name: string(rune('a' + i)), Duration: Duration1h},
			ExpiresAt: now.Add(time.Duration(i+1) * time.Minute),
		})
	}
	if got := ts.Snapshot(now); len(got) != 3 {
		t.Fatalf("got %d rules, want 3", len(got))
	}
}

// Unbounded rules are the ones a user still has a reason to act on, so a full
// list should shed a timed rule before one of those.
func TestStoreEvictsTimedBeforeUnbounded(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(2)
	ts.Add(&TempRule{Rule: Rule{Name: "restart", Duration: DurationUntilRestart}})
	ts.Add(&TempRule{Rule: Rule{Name: "soon", Duration: Duration30s}, ExpiresAt: now.Add(30 * time.Second)})
	ts.Add(&TempRule{Rule: Rule{Name: "later", Duration: Duration1h}, ExpiresAt: now.Add(time.Hour)})

	got := ts.Snapshot(now)
	if len(got) != 2 {
		t.Fatalf("got %d rules, want 2", len(got))
	}
	for _, r := range got {
		if r.Name == "soon" {
			t.Error("dropped the unbounded rule instead of the soonest timed one")
		}
	}
}

func TestRemove(t *testing.T) {
	now := time.Now()
	ts := NewTempStore(0)
	record(t, ts, Decision{Action: ActionAllow, Duration: Duration1h}, now)
	got := ts.Snapshot(now)
	if len(got) != 1 {
		t.Fatalf("got %d rules", len(got))
	}

	ts.Remove(got[0].Name)
	if got := ts.Snapshot(now); len(got) != 0 {
		t.Fatalf("got %d rules after remove, want 0", len(got))
	}
}

func TestOnChangeFires(t *testing.T) {
	ts := NewTempStore(0)
	var calls int
	ts.OnChange(func() { calls++ })

	ts.Add(&TempRule{Rule: Rule{Name: "a", Duration: Duration1h}, ExpiresAt: time.Now().Add(time.Hour)})
	if calls != 1 {
		t.Fatalf("Add fired %d times, want 1", calls)
	}
	ts.Remove("a")
	if calls != 2 {
		t.Fatalf("Remove fired %d times, want 2", calls)
	}
}

// AskRule is the only place a temporary rule can be caught, so the recording
// has to happen there and not depend on the prompt being interactive.
func TestAskRuleRecordsTemporaryRule(t *testing.T) {
	ts := NewTempStore(0)
	s := NewServer(NewStore(10), ActionAllow, DurationOnce, "test")
	s.SetTempStore(ts)
	s.SetPrompter(func(*protocol.Connection) (Decision, bool) {
		return Decision{Action: ActionAllow, Duration: Duration5m}, true
	})

	rule, err := s.AskRule(t.Context(), tempConn())
	if err != nil {
		t.Fatal(err)
	}

	got := ts.Snapshot(time.Now())
	if len(got) != 1 {
		t.Fatalf("got %d rules, want 1", len(got))
	}
	if got[0].Name != rule.GetName() {
		t.Errorf("name = %q, want %q", got[0].Name, rule.GetName())
	}
	if got[0].Duration != Duration5m {
		t.Errorf("duration = %q", got[0].Duration)
	}
}

// A nil store must not change what AskRule applies.
func TestAskRuleWithoutTempStore(t *testing.T) {
	s := NewServer(NewStore(10), ActionAllow, Duration5m, "test")
	rule, err := s.AskRule(t.Context(), tempConn())
	if err != nil {
		t.Fatal(err)
	}
	if rule.GetDuration() != Duration5m {
		t.Errorf("duration = %q", rule.GetDuration())
	}
}
