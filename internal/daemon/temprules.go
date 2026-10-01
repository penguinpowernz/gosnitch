package daemon

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Temporary rules never reach the rules directory. opensnitchd holds them in
// its own loader and drops them on a timer (it logs "Temporary rule expired"),
// so a RuleStore reading /etc/opensnitchd/rules can only ever show "always"
// rules. There is no RPC to read the daemon's in-memory set either: the UI
// service exposes no equivalent of the loader's GetAll, and Statistics carries
// only a count.
//
// What gosnitch does have is the rule itself: AskRule builds every rule the
// daemon applies, so recording them on the way out is the only way to show a
// temporary rule at all. That makes this list authoritative for rules this
// gosnitch answered and blind to everything else - see TempStore.
//
// ExpiresAt is the corresponding wire duration resolved against the answer's
// timestamp. Durations the daemon treats as unbounded (always, until restart)
// have no expiry, so callers must check HasExpiry rather than comparing a zero
// time.
type TempRule struct {
	Rule
	ExpiresAt time.Time
}

// HasExpiry reports whether this rule expires at a known time. "until restart"
// does expire, but at a moment only the daemon knows, so it reports false.
func (t TempRule) HasExpiry() bool { return !t.ExpiresAt.IsZero() }

// Remaining is how long the rule has left, or zero once it is due.
func (t TempRule) Remaining(now time.Time) time.Duration {
	if !t.HasExpiry() {
		return 0
	}
	if d := t.ExpiresAt.Sub(now); d > 0 {
		return d
	}
	return 0
}

// IsTemporary reports whether a duration makes the daemon hold the rule in
// memory instead of writing it to the rules directory. Everything but "always"
// is temporary, including "once".
func IsTemporary(duration string) bool {
	return duration != "" && duration != DurationAlways
}

// timedDuration resolves a wire duration to how long the rule lasts.
//
// ok=false covers the two the daemon does not put a clock on: "always" is not
// temporary at all, and "until restart" lasts for a daemon lifetime, which the
// UI cannot predict. "once" is listed explicitly at zero because the daemon
// consumes it on the connection that created it, so it is already spent by the
// time it could be shown.
func timedDuration(d string) (time.Duration, bool) {
	switch d {
	case DurationOnce:
		return 0, true
	case Duration30s:
		return 30 * time.Second, true
	case Duration5m:
		return 5 * time.Minute, true
	case Duration15m:
		return 15 * time.Minute, true
	case Duration30m:
		return 30 * time.Minute, true
	case Duration1h:
		return time.Hour, true
	}
	return 0, false
}

// TempStore records the temporary rules this gosnitch has handed the daemon.
//
// It is deliberately not presented as the daemon's full temporary set, because
// it cannot be: rules answered by another client, or before this process
// started, are invisible here, and the daemon's own expiry timer is the real
// authority on when one goes. The store mirrors that timer rather than driving
// it - Snapshot drops what is due, so the list cannot offer a Delete for a
// rule the daemon has already forgotten.
type TempStore struct {
	mu       sync.RWMutex
	rules    []TempRule
	max      int
	onChange func()
}

// defaultMaxTempRules bounds the list. Any local process can drive AskRule as
// fast as it likes, and each answer with a temporary duration lands here, so
// this is the same kind of cap the event ring has.
const defaultMaxTempRules = 500

func NewTempStore(max int) *TempStore {
	if max <= 0 {
		max = defaultMaxTempRules
	}
	return &TempStore{max: max}
}

func (t *TempStore) OnChange(fn func()) {
	t.mu.Lock()
	t.onChange = fn
	t.mu.Unlock()
}

// Add records a rule the daemon has just been given. Rules that are not
// temporary are ignored, as are "once" rules: the daemon spends those on the
// connection that produced them, so listing one would only ever offer a Delete
// that cannot apply to anything.
//
// Re-answering a prompt whose rule is still live does NOT replace the entry,
// because the daemon does not replace it either. An AskRule reply reaches the
// daemon's loader through Add -> addUserRule -> setUniqueName, and
// setUniqueName renames a colliding rule to "<name>-2" rather than overwriting
// it (opensnitch 1.5.8.1, daemon/rule/loader.go):
//
//	for l.isUniqueName(rule.Name) == false { idx++; rule.Name = ... }
//
// isUniqueName returns !found, so that loop runs while the name is absent and
// stops as soon as it collides - inverted from what its name says, and it
// starts at idx=2. For a name the loader does not hold, which is the ordinary
// case, the name is left alone and this store's record is accurate. For one it
// does hold, the daemon stores a rule we never named.
//
// So the entry we already have is left exactly as it is. Overwriting it under
// the original name would point the tab's Delete at a name the daemon no
// longer has, and Loader.Delete returns nil for an unknown name, so the daemon
// would reply OK and the row would vanish having deleted nothing. Keeping the
// known-good record means Delete stays aimed at a rule that really is there;
// the renamed duplicate is simply not listed, the same as any rule this
// gosnitch did not name.
func (t *TempStore) Add(r *TempRule) {
	if r == nil || !IsTemporary(r.Duration) || r.Duration == DurationOnce {
		return
	}

	t.mu.Lock()
	known := false
	for i := range t.rules {
		if t.rules[i].Name == r.Name {
			known = true
			break
		}
	}
	if !known {
		if len(t.rules) >= t.max {
			// Drop whatever is closest to expiring; it is the least useful
			// thing in the list and the daemon is about to forget it anyway.
			t.dropSoonestLocked()
		}
		t.rules = append(t.rules, *r)
	}
	fn := t.onChange
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// dropSoonestLocked removes the entry expiring first. Entries without an
// expiry ("until restart") are kept in preference to timed ones, since they
// are the ones a user still has a reason to act on.
func (t *TempStore) dropSoonestLocked() {
	best := -1
	for i, r := range t.rules {
		if !r.HasExpiry() {
			continue
		}
		if best < 0 || r.ExpiresAt.Before(t.rules[best].ExpiresAt) {
			best = i
		}
	}
	if best < 0 {
		best = 0 // all unbounded; drop the oldest recorded
	}
	t.rules = append(t.rules[:best], t.rules[best+1:]...)
}

// Record builds and stores the temporary rule implied by a decision. created is
// when the decision was made, which is what the expiry is measured from.
func (t *TempStore) Record(rule *protocol.Rule, d Decision, created time.Time) {
	if rule == nil || !IsTemporary(d.Duration) {
		return
	}
	tr := TempRule{Rule: ruleFromProto(rule, created)}
	if lifetime, ok := timedDuration(d.Duration); ok {
		tr.ExpiresAt = created.Add(lifetime)
	}
	t.Add(&tr)
}

// ruleFromProto flattens a rule we just built into the same shape the rules
// table displays, so both tables read identically.
//
// The operands are read back out of the built rule rather than off the
// Connection: BuildRule decides which parts of the connection the rule is
// actually scoped to, and showing the connection's values instead would claim
// a narrowing the rule does not have.
func ruleFromProto(rule *protocol.Rule, created time.Time) Rule {
	r := Rule{
		Name:     rule.GetName(),
		Created:  created,
		Enabled:  rule.GetEnabled(),
		Action:   rule.GetAction(),
		Duration: rule.GetDuration(),
	}

	op := rule.GetOperator()
	var ops []ruleOperator
	if op.GetType() == "list" {
		// A list rule carries its operands as JSON in Data, the same encoding
		// BuildRule wrote and parseRuleFile reads back off disk.
		var decoded []ruleOperator
		if err := json.Unmarshal([]byte(op.GetData()), &decoded); err == nil {
			ops = decoded
		}
	} else {
		ops = []ruleOperator{{
			Type:    op.GetType(),
			Operand: op.GetOperand(),
			Data:    op.GetData(),
		}}
	}

	for _, o := range ops {
		switch o.Operand {
		case "process.path", "process.command":
			if r.Process == "" {
				r.Process = o.Data
			}
		case "dest.host", "dest.ip", "dest.network":
			if r.Dest == "" {
				r.Dest = o.Data
			}
		case "dest.port":
			if r.Port == "" {
				r.Port = o.Data
			}
		case "user.id":
			if r.UserID == "" {
				r.UserID = o.Data
			}
		}
	}
	return r
}

// Snapshot returns the rules still live at now, soonest to expire first so the
// list reads as a queue of what is about to lapse. Expired entries are dropped
// as a side effect: the daemon has already removed them, and offering a Delete
// for one would send a notification the daemon refuses.
func (t *TempStore) Snapshot(now time.Time) []TempRule {
	t.mu.Lock()
	kept := t.rules[:0]
	for _, r := range t.rules {
		if r.HasExpiry() && !r.ExpiresAt.After(now) {
			continue
		}
		kept = append(kept, r)
	}
	t.rules = kept
	out := make([]TempRule, len(kept))
	copy(out, kept)
	t.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		// Unbounded rules sort last: everything timed is more urgent.
		switch {
		case out[i].HasExpiry() != out[j].HasExpiry():
			return out[i].HasExpiry()
		case out[i].HasExpiry():
			if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
				return out[i].ExpiresAt.Before(out[j].ExpiresAt)
			}
		}
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Remove drops a rule by name, once the daemon has confirmed the delete.
func (t *TempStore) Remove(name string) {
	t.mu.Lock()
	kept := t.rules[:0]
	for _, r := range t.rules {
		if r.Name != name {
			kept = append(kept, r)
		}
	}
	t.rules = kept
	fn := t.onChange
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// DropUntilRestart forgets the rules that only lasted a daemon lifetime. The
// daemon rebuilt its loader from disk when it reconnected, so those rules are
// gone; keeping them would leave the list offering deletes that cannot land.
func (t *TempStore) DropUntilRestart() {
	t.mu.Lock()
	kept := t.rules[:0]
	for _, r := range t.rules {
		if r.Duration == DurationUntilRestart {
			continue
		}
		kept = append(kept, r)
	}
	changed := len(kept) != len(t.rules)
	t.rules = kept
	fn := t.onChange
	t.mu.Unlock()
	if fn != nil && changed {
		fn()
	}
}
