package daemon

import (
	"encoding/json"
	"fmt"
	"strconv"

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

	if !d.Scope.Any() {
		return &protocol.Rule{
			Name:     name,
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
		}
	}
	if d.Scope.Port {
		port := strconv.FormatUint(uint64(conn.GetDstPort()), 10)
		ops = append(ops, jsonOperator{Type: "simple", Operand: "dest.port", Data: port})
		name = slugify(name + "-" + port)
	}
	if d.Scope.User {
		uid := strconv.FormatUint(uint64(conn.GetUserId()), 10)
		ops = append(ops, jsonOperator{Type: "simple", Operand: "user.id", Data: uid})
		name = slugify(name + "-" + uid)
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
		Name:     name,
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
