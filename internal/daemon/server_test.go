package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

func TestAskRuleUsesDefaultWithoutPrompter(t *testing.T) {
	st := NewStore(10)
	s := NewServer(st, ActionDeny, DurationAlways, "test")

	rule, err := s.AskRule(context.Background(), &protocol.Connection{
		ProcessPath: "/usr/bin/curl", DstHost: "example.com", DstPort: 443, Protocol: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rule.GetAction() != ActionDeny || rule.GetDuration() != DurationAlways {
		t.Fatalf("got %s/%s, want %s/%s", rule.GetAction(), rule.GetDuration(), ActionDeny, DurationAlways)
	}
	if got := rule.GetOperator().GetData(); got != "/usr/bin/curl" {
		t.Fatalf("operator data = %q", got)
	}
	if n := len(st.Snapshot()); n != 1 {
		t.Fatalf("store has %d entries, want 1", n)
	}
}

func TestAskRuleUsesPrompterAnswer(t *testing.T) {
	st := NewStore(10)
	s := NewServer(st, ActionAllow, DurationOnce, "test")
	s.SetPrompter(func(*protocol.Connection) (string, string, bool) {
		return ActionReject, DurationUntilRestart, true
	})

	rule, _ := s.AskRule(context.Background(), &protocol.Connection{ProcessPath: "/bin/sh"})
	if rule.GetAction() != ActionReject || rule.GetDuration() != DurationUntilRestart {
		t.Fatalf("got %s/%s", rule.GetAction(), rule.GetDuration())
	}
}

// A prompter that declines (window closed, timed out) must fall back rather
// than leaving the daemon without an answer.
func TestAskRuleFallsBackWhenPrompterDeclines(t *testing.T) {
	st := NewStore(10)
	s := NewServer(st, ActionAllow, DurationOnce, "test")
	s.SetPrompter(func(*protocol.Connection) (string, string, bool) { return "", "", false })

	rule, _ := s.AskRule(context.Background(), &protocol.Connection{ProcessPath: "/bin/sh"})
	if rule.GetAction() != ActionAllow {
		t.Fatalf("got %s, want fallback %s", rule.GetAction(), ActionAllow)
	}
}

func TestRuleNameMatchesPythonSlug(t *testing.T) {
	got := ruleName(ActionAllow, DurationOnce, &protocol.Connection{ProcessPath: "/usr/bin/curl"})
	if want := "allow-once-simple-usr-bin-curl"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStoreRingBufferKeepsNewestFirst(t *testing.T) {
	st := NewStore(3)
	for i := 0; i < 5; i++ {
		st.Add(Entry{Process: string(rune('a' + i))})
	}
	snap := st.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d, want 3", len(snap))
	}
	if snap[0].Process != "e" {
		t.Fatalf("newest = %q, want \"e\"", snap[0].Process)
	}
}

func TestEntryFromConnFallsBackToIP(t *testing.T) {
	e := EntryFromConn(&protocol.Connection{DstIp: "1.1.1.1"}, ActionAllow, time.Now())
	if e.Dest != "1.1.1.1" {
		t.Fatalf("dest = %q, want the IP when no hostname is known", e.Dest)
	}
}

func TestStatusReportsDisconnectAfterStalePing(t *testing.T) {
	s := NewServer(NewStore(1), ActionAllow, DurationOnce, "test")
	s.Ping(context.Background(), &protocol.PingRequest{Stats: &protocol.Statistics{DaemonVersion: "1.5.8"}})

	if ok, ver, _ := s.Status(); !ok || ver != "1.5.8" {
		t.Fatalf("connected=%v version=%q", ok, ver)
	}

	s.mu.Lock()
	s.lastPing = time.Now().Add(-30 * time.Second)
	s.mu.Unlock()

	if ok, _, _ := s.Status(); ok {
		t.Fatal("still reported connected after a stale ping")
	}
}
