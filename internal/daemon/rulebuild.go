package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Wire values for durations. The prompt shows friendlier labels ("until
// reboot", "forever"), but these are what the daemon actually parses.
const (
	Duration30s = "30s"
	Duration5m  = "5m"
	Duration15m = "15m"
	Duration30m = "30m"
	Duration1h  = "1h"
)

// Scope selects which parts of a connection a rule is narrowed to. The process
// path is always included; these are the optional extras.
type Scope struct {
	Dest bool // dest.host, falling back to dest.ip
	Port bool // dest.port
	User bool // user.id
}

// Any reports whether any narrowing is enabled, which decides whether the rule
// is a "list" rule or a plain "simple" one.
func (s Scope) Any() bool { return s.Dest || s.Port || s.User }

// Decision is the user's answer to a prompt.
type Decision struct {
	Action   string
	Duration string
	Scope    Scope
}

type jsonOperator struct {
	Type    string `json:"type"`
	Operand string `json:"operand"`
	Data    string `json:"data"`
}

// BuildRule turns a decision about a connection into a rule for the daemon.
//
// It follows the Python UI's construction exactly, because both clients write
// into the same rules directory: operands are appended in the order
// dest, port, user, process; the process operand goes last; and a rule with
// any narrowing becomes a "list" whose data is the JSON-encoded operand array.
func BuildRule(conn *protocol.Connection, d Decision) *protocol.Rule {
	procOp := jsonOperator{
		Type:    "simple",
		Operand: "process.path",
		Data:    conn.GetProcessPath(),
	}

	// The name accumulates the same fragments, in the same order, as the
	// Python UI's, so equivalent rules from either client collide by name
	// rather than silently duplicating.
	name := fmt.Sprintf("%s-%s", d.Action, d.Duration)
	if d.Scope.Any() {
		name += "-list"
	} else {
		name += "-simple"
	}
	name = slugify(name + "-" + procOp.Data)

	// frags are the values that fed the name, in order. The digest below is
	// keyed off exactly these, so it depends only on what the name encodes.
	frags := []string{procOp.Data}

	if !d.Scope.Any() {
		return &protocol.Rule{
			Name:     disambiguate(name, frags),
			Enabled:  true,
			Action:   d.Action,
			Duration: d.Duration,
			Operator: &protocol.Operator{
				Type:    procOp.Type,
				Operand: procOp.Operand,
				Data:    procOp.Data,
			},
		}
	}

	var ops []jsonOperator
	if d.Scope.Dest {
		operand, data := destOperand(conn)
		if data != "" {
			ops = append(ops, jsonOperator{Type: "simple", Operand: operand, Data: data})
			name = slugify(name + "-" + data)
			frags = append(frags, data)
		}
	}
	if d.Scope.Port {
		port := strconv.FormatUint(uint64(conn.GetDstPort()), 10)
		ops = append(ops, jsonOperator{Type: "simple", Operand: "dest.port", Data: port})
		name = slugify(name + "-" + port)
		frags = append(frags, port)
	}
	if d.Scope.User {
		uid := strconv.FormatUint(uint64(conn.GetUserId()), 10)
		ops = append(ops, jsonOperator{Type: "simple", Operand: "user.id", Data: uid})
		name = slugify(name + "-" + uid)
		frags = append(frags, uid)
	}
	// The process operand is appended last, matching the Python UI.
	ops = append(ops, procOp)

	data, err := json.Marshal(ops)
	if err != nil {
		// Marshalling a fixed struct slice cannot realistically fail; fall
		// back to a process-only rule rather than sending a malformed one.
		return BuildRule(conn, Decision{Action: d.Action, Duration: d.Duration})
	}

	return &protocol.Rule{
		Name:     disambiguate(name, frags),
		Enabled:  true,
		Action:   d.Action,
		Duration: d.Duration,
		Operator: &protocol.Operator{
			Type:    "list",
			Operand: "list",
			Data:    string(data),
		},
	}
}

// destOperand prefers the hostname, since an IP for the same service often
// changes between connections and would make the rule useless.
func destOperand(conn *protocol.Connection) (operand, data string) {
	if h := conn.GetDstHost(); h != "" {
		return "dest.host", h
	}
	return "dest.ip", conn.GetDstIp()
}

// disambiguate appends a short digest of the name's inputs when slugify has
// thrown away enough of them that a different rule could produce the same name.
//
// slugify keeps [a-z0-9] and turns every run of anything else into a single
// dash, so /usr/bin/foo-bar, foo_bar and foo.bar all reduce to the same name,
// and a value with no ASCII alphanumerics (a CJK-named binary, say) reduces to
// nothing. The daemon keys rules by name, so those rules would silently
// overwrite each other.
//
// frags are the values that fed the name, in order. When every one survived
// slugify intact the name is left alone, so the ordinary case still matches
// the Python UI's name byte for byte and rules from either client collide by
// name as intended.
func disambiguate(name string, frags []string) string {
	lossy := name == ""
	for _, f := range frags {
		if !identifying(f) {
			lossy = true
			break
		}
	}
	if !lossy {
		return name
	}

	// Hash the fragments with a separator that cannot appear in them, so
	// ["ab","c"] and ["a","bc"] cannot digest alike.
	h := sha256.New()
	for _, f := range frags {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	suffix := hex.EncodeToString(h.Sum(nil)[:4])
	if name == "" {
		return suffix
	}
	return name + "-" + suffix
}

// identifying reports whether the slug of v still says which value it came
// from, which is all a rule name has to do.
//
// slugify keeps [a-z0-9] and turns every run of anything else into one dash,
// so it is not reversible: /usr/bin/foo-bar and /usr/bin/foo_bar both give
// "usr-bin-foo-bar". Suffixing everything that cannot be reconstructed was
// the first thing I tried and it is the wrong bar - opensnitchd already holds
// rules under the Python UI's names, and on this machine that rule renamed
// 125 of 267 of them, so gosnitch would write a duplicate beside each rather
// than update it. Matching the existing scheme is worth more than closing a
// case that needs two binaries differing only in punctuation, which the
// Python UI does not close either.
//
// What genuinely breaks is a value that leaves nothing of itself behind: a
// CJK-named binary slugs to just its parent directory, so every such binary
// in /usr/bin shares one name and the rules overwrite each other. That is
// what earns a digest.
func identifying(v string) bool {
	if v == "" {
		return true // nothing was asked of it
	}
	slug := slugify(v)
	if slug == "" {
		return false // the whole value vanished
	}

	// The last path segment is the part that names the binary. If it did not
	// survive, the slug points at a directory and every sibling collides.
	last := v
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		last = v[i+1:]
	}
	return last == "" || slugify(last) != ""
}
