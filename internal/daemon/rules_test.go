package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleListRule = `{
  "created": "2026-09-23T22:58:29.963089503+12:00",
  "updated": "2026-09-23T22:58:29.963173408+12:00",
  "name": "allow-always-list-test",
  "enabled": true,
  "action": "allow",
  "duration": "always",
  "operator": {
    "type": "list", "operand": "list", "data": "",
    "list": [
      {"type":"simple","operand":"dest.host","data":"hooks.slack.com"},
      {"type":"simple","operand":"dest.port","data":"443"},
      {"type":"simple","operand":"user.id","data":"1000"},
      {"type":"simple","operand":"process.path","data":"/home/robert/bin/helpmailbot"}
    ]
  }
}`

const sampleSimpleRule = `{
  "created": "2026-01-02T03:04:05Z",
  "name": "deny-once-simple-curl",
  "enabled": true,
  "action": "deny",
  "duration": "once",
  "operator": {"type":"simple","operand":"process.path","data":"/usr/bin/curl"}
}`

func writeRules(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParseListRuleExtractsOperands(t *testing.T) {
	dir := writeRules(t, map[string]string{"a.json": sampleListRule})
	rs := NewRuleStore(dir)
	if err := rs.Reload(); err != nil {
		t.Fatal(err)
	}
	got := rs.Snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d rules", len(got))
	}
	r := got[0]
	if r.Process != "/home/robert/bin/helpmailbot" {
		t.Errorf("process = %q", r.Process)
	}
	if r.Dest != "hooks.slack.com" {
		t.Errorf("dest = %q", r.Dest)
	}
	if r.Port != "443" {
		t.Errorf("port = %q", r.Port)
	}
	if r.UserID != "1000" {
		t.Errorf("uid = %q", r.UserID)
	}
}

func TestParseSimpleRule(t *testing.T) {
	dir := writeRules(t, map[string]string{"b.json": sampleSimpleRule})
	rs := NewRuleStore(dir)
	rs.Reload()
	r := rs.Snapshot()[0]
	if r.Process != "/usr/bin/curl" || r.Action != "deny" {
		t.Fatalf("got process=%q action=%q", r.Process, r.Action)
	}
}

func TestRulesSortedNewestFirst(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"old.json": sampleSimpleRule, // 2026-01-02
		"new.json": sampleListRule,   // 2026-09-23
	})
	rs := NewRuleStore(dir)
	rs.Reload()
	got := rs.Snapshot()
	if got[0].Name != "allow-always-list-test" {
		t.Fatalf("newest first failed: %q came first", got[0].Name)
	}
	if !got[0].Created.After(got[1].Created) {
		t.Fatal("ordering is not descending by created")
	}
}

// A single unparseable file must not hide the rest.
func TestMalformedRuleIsSkipped(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"good.json": sampleSimpleRule,
		"bad.json":  "{ not json",
	})
	rs := NewRuleStore(dir)
	rs.Reload()
	if n := len(rs.Snapshot()); n != 1 {
		t.Fatalf("got %d rules, want 1 good one", n)
	}
}

// Rules written before the daemon recorded timestamps must still sort.
func TestMissingTimestampFallsBackToModTime(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"c.json": `{"name":"x","action":"allow","operator":{"type":"simple","operand":"process.path","data":"/bin/x"}}`,
	})
	rs := NewRuleStore(dir)
	rs.Reload()
	if got := rs.Snapshot()[0].Created; got.IsZero() {
		t.Fatal("created is zero; expected file mtime fallback")
	} else if time.Since(got) > time.Hour {
		t.Fatalf("mtime fallback looks wrong: %v", got)
	}
}

func TestRemoveDropsRuleLocally(t *testing.T) {
	dir := writeRules(t, map[string]string{"a.json": sampleListRule, "b.json": sampleSimpleRule})
	rs := NewRuleStore(dir)
	rs.Reload()
	rs.Remove("allow-always-list-test")
	got := rs.Snapshot()
	if len(got) != 1 || got[0].Name != "deny-once-simple-curl" {
		t.Fatalf("remove failed: %+v", got)
	}
}

func TestMissingDirectoryReportsError(t *testing.T) {
	rs := NewRuleStore("/nonexistent/gosnitch/rules")
	if err := rs.Reload(); err == nil {
		t.Fatal("expected an error")
	}
	if rs.Err() == nil {
		t.Fatal("Err() should report the failure")
	}
}
