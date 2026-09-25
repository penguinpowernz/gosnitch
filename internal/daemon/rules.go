package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultRulesPath is where opensnitchd keeps its rules, matching the
// -rules-path the daemon is started with.
const DefaultRulesPath = "/etc/opensnitchd/rules"

// ruleOperator mirrors the operator block of a rule file. A rule is either a
// single "simple" operator or a "list" of them, so both shapes are decoded.
type ruleOperator struct {
	Type    string         `json:"type"`
	Operand string         `json:"operand"`
	Data    string         `json:"data"`
	List    []ruleOperator `json:"list"`
}

type ruleFile struct {
	Created  time.Time    `json:"created"`
	Updated  time.Time    `json:"updated"`
	Name     string       `json:"name"`
	Enabled  bool         `json:"enabled"`
	Action   string       `json:"action"`
	Duration string       `json:"duration"`
	Operator ruleOperator `json:"operator"`
}

// Rule is a rule flattened for display.
type Rule struct {
	Name     string
	Created  time.Time
	Enabled  bool
	Action   string
	Duration string
	Process  string // process.path operand, when the rule has one
	Dest     string // dest.host or dest.ip
	Port     string
	UserID   string
	Path     string // the file this came from
}

// RuleStore lists the rules on disk. Rules are owned by root, so gosnitch only
// ever reads them; deleting goes through the daemon, which owns the files.
type RuleStore struct {
	dir string

	mu       sync.RWMutex
	rules    []Rule
	err      error
	onChange func()
}

func NewRuleStore(dir string) *RuleStore {
	if dir == "" {
		dir = DefaultRulesPath
	}
	return &RuleStore{dir: dir}
}

func (r *RuleStore) OnChange(fn func()) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
}

// Reload rereads the rules directory, newest first.
func (r *RuleStore) Reload() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.mu.Lock()
		r.err = err
		r.rules = nil
		fn := r.onChange
		r.mu.Unlock()
		if fn != nil {
			fn()
		}
		return err
	}

	var out []Rule
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		full := filepath.Join(r.dir, e.Name())
		rule, err := parseRuleFile(full)
		if err != nil {
			// One malformed rule should not hide the other 266.
			continue
		}
		out = append(out, rule)
	}

	// Newest first. Fall back to name so the order is stable when two rules
	// share a timestamp.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created.Equal(out[j].Created) {
			return out[i].Name < out[j].Name
		}
		return out[i].Created.After(out[j].Created)
	})

	r.mu.Lock()
	r.rules = out
	r.err = nil
	fn := r.onChange
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

func parseRuleFile(path string) (Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Rule{}, err
	}
	var rf ruleFile
	if err := json.Unmarshal(b, &rf); err != nil {
		return Rule{}, err
	}

	created := rf.Created
	if created.IsZero() {
		created = rf.Updated
	}
	if created.IsZero() {
		// Very old rules predate the timestamps; use the file's mtime so they
		// still sort sensibly rather than all landing at the epoch.
		if fi, err := os.Stat(path); err == nil {
			created = fi.ModTime()
		}
	}

	r := Rule{
		Name:     rf.Name,
		Created:  created,
		Enabled:  rf.Enabled,
		Action:   rf.Action,
		Duration: rf.Duration,
		Path:     path,
	}
	if r.Name == "" {
		r.Name = strings.TrimSuffix(filepath.Base(path), ".json")
	}

	for _, op := range flattenOperators(rf.Operator) {
		switch op.Operand {
		case "process.path", "process.command":
			if r.Process == "" {
				r.Process = op.Data
			}
		case "dest.host", "dest.ip", "dest.network":
			if r.Dest == "" {
				r.Dest = op.Data
			}
		case "dest.port":
			if r.Port == "" {
				r.Port = op.Data
			}
		case "user.id":
			if r.UserID == "" {
				r.UserID = op.Data
			}
		}
	}
	return r, nil
}

// flattenOperators walks a rule's operator tree into a flat slice, so a "list"
// rule and a "simple" rule can be read the same way.
func flattenOperators(op ruleOperator) []ruleOperator {
	if len(op.List) == 0 {
		return []ruleOperator{op}
	}
	var out []ruleOperator
	for _, child := range op.List {
		out = append(out, flattenOperators(child)...)
	}
	return out
}

// Snapshot returns the rules, newest first.
func (r *RuleStore) Snapshot() []Rule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Rule, len(r.rules))
	copy(out, r.rules)
	return out
}

// Err reports why the last Reload failed, if it did.
func (r *RuleStore) Err() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.err
}

// Remove drops a rule from the local list. The daemon owns the file, so this
// only keeps the table in step until the next Reload.
func (r *RuleStore) Remove(name string) {
	r.mu.Lock()
	kept := r.rules[:0]
	for _, rule := range r.rules {
		if rule.Name != name {
			kept = append(kept, rule)
		}
	}
	r.rules = kept
	fn := r.onChange
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}
