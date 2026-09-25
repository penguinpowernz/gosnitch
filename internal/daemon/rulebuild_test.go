package daemon

import (
	"encoding/json"
	"testing"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

func testConn() *protocol.Connection {
	return &protocol.Connection{
		Protocol: "tcp", DstIp: "104.16.0.1", DstHost: "hooks.slack.com",
		DstPort: 443, UserId: 1000, ProcessId: 4242,
		ProcessPath: "/home/robert/bin/helpmailbot",
	}
}

func TestBuildRuleProcessOnly(t *testing.T) {
	r := BuildRule(testConn(), Decision{Action: ActionAllow, Duration: DurationOnce})
	if r.GetOperator().GetType() != "simple" {
		t.Errorf("type = %q, want simple", r.GetOperator().GetType())
	}
	if r.GetOperator().GetOperand() != "process.path" {
		t.Errorf("operand = %q", r.GetOperator().GetOperand())
	}
	if want := "allow-once-simple-home-robert-bin-helpmailbot"; r.GetName() != want {
		t.Errorf("name = %q, want %q", r.GetName(), want)
	}
}

// The full narrowing case: this is the shape the user reaches for when
// allowing something forever, and must match the Python UI's output.
func TestBuildRuleFullScopeMatchesPythonShape(t *testing.T) {
	r := BuildRule(testConn(), Decision{
		Action:   ActionAllow,
		Duration: DurationAlways,
		Scope:    Scope{Dest: true, Port: true, User: true},
	})

	op := r.GetOperator()
	if op.GetType() != "list" || op.GetOperand() != "list" {
		t.Fatalf("type/operand = %q/%q, want list/list", op.GetType(), op.GetOperand())
	}

	var ops []jsonOperator
	if err := json.Unmarshal([]byte(op.GetData()), &ops); err != nil {
		t.Fatalf("data is not a JSON operator array: %v", err)
	}
	want := []jsonOperator{
		{"simple", "dest.host", "hooks.slack.com"},
		{"simple", "dest.port", "443"},
		{"simple", "user.id", "1000"},
		{"simple", "process.path", "/home/robert/bin/helpmailbot"},
	}
	if len(ops) != len(want) {
		t.Fatalf("got %d operands, want %d: %v", len(ops), len(want), ops)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("operand %d = %+v, want %+v", i, ops[i], want[i])
		}
	}

	// Compare against a real rule file name from /etc/opensnitchd/rules.
	const wantName = "allow-always-list-home-robert-bin-helpmailbot-hooks-slack-com-443-1000"
	if r.GetName() != wantName {
		t.Errorf("name  = %q\nwant  = %q", r.GetName(), wantName)
	}
}

// Denying a destination forever is the other common answer.
func TestBuildRuleDenyDestinationOnly(t *testing.T) {
	r := BuildRule(testConn(), Decision{
		Action: ActionDeny, Duration: DurationAlways, Scope: Scope{Dest: true},
	})
	var ops []jsonOperator
	json.Unmarshal([]byte(r.GetOperator().GetData()), &ops)
	if len(ops) != 2 {
		t.Fatalf("got %d operands, want dest + process", len(ops))
	}
	if ops[0].Operand != "dest.host" || ops[1].Operand != "process.path" {
		t.Errorf("order wrong: %+v", ops)
	}
}

// With no hostname the rule must fall back to the IP rather than emit an
// empty operand, which the daemon would reject.
func TestBuildRuleFallsBackToDestIP(t *testing.T) {
	c := testConn()
	c.DstHost = ""
	r := BuildRule(c, Decision{Action: ActionDeny, Duration: DurationAlways, Scope: Scope{Dest: true}})

	var ops []jsonOperator
	json.Unmarshal([]byte(r.GetOperator().GetData()), &ops)
	if ops[0].Operand != "dest.ip" || ops[0].Data != "104.16.0.1" {
		t.Fatalf("got %+v, want dest.ip fallback", ops[0])
	}
}

func TestBuildRuleTimedDurations(t *testing.T) {
	for _, d := range []string{Duration30s, Duration5m, Duration1h, DurationUntilRestart, DurationAlways} {
		r := BuildRule(testConn(), Decision{Action: ActionAllow, Duration: d})
		if r.GetDuration() != d {
			t.Errorf("duration = %q, want %q", r.GetDuration(), d)
		}
	}
}

// A dest toggle with neither host nor IP must not produce an empty operand.
func TestBuildRuleSkipsEmptyDest(t *testing.T) {
	c := testConn()
	c.DstHost, c.DstIp = "", ""
	r := BuildRule(c, Decision{Action: ActionAllow, Duration: DurationOnce, Scope: Scope{Dest: true}})

	var ops []jsonOperator
	json.Unmarshal([]byte(r.GetOperator().GetData()), &ops)
	for _, op := range ops {
		if op.Data == "" {
			t.Fatalf("emitted an empty operand: %+v", ops)
		}
	}
}
