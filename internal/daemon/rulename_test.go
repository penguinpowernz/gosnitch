package daemon

import (
	"strconv"
	"testing"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Rule names must keep matching the ones opensnitchd already has on disk.
//
// The daemon keys rules by name, so a naming change does not just alter new
// rules: it makes gosnitch write a duplicate beside every existing rule
// instead of updating it. These are real names from a live rules directory,
// covering the shapes that make naming awkward - hyphens in the binary name,
// dotted version directories, dotted hostnames, and dotted IPs.
func TestRuleNamesMatchRulesOnDisk(t *testing.T) {
	cases := []struct{ name, action, duration, path, host, ip, port, uid string }{
		{"allow-always-simple-usr-sbin-networkmanager", "allow", "always", "/usr/sbin/NetworkManager", "", "", "", ""},
		{"allow-always-list-usr-bin-docker-proxy-172-22-0-3-3833-0", "allow", "always", "/usr/bin/docker-proxy", "", "172.22.0.3", "3833", "0"},
		{"deny-always-list-usr-lib-chatgpt-chatgpt-o33249-ingest-us-sentry-io", "deny", "always", "/usr/lib/chatgpt/ChatGPT", "o33249.ingest.us.sentry.io", "", "", ""},
		{"allow-always-simple-tmp-go-build4264078922-b001-exe-nexusui", "allow", "always", "/tmp/go-build4264078922/b001/exe/nexusui", "", "", "", ""},
		{"allow-always-list-home-robert-local-share-claude-versions-2-1-116-platform-claude-com-443-1000", "allow", "always", "/home/robert/.local/share/claude/versions/2.1.116", "platform.claude.com", "", "443", "1000"},
		{"allow-always-list-home-robert-local-share-claude-versions-2-1-77-53-1000", "allow", "always", "/home/robert/.local/share/claude/versions/2.1.77", "", "", "53", "1000"},
		{"deny-always-list-home-robert-local-share-claude-versions-2-1-150-downloads-claude-ai-443", "deny", "always", "/home/robert/.local/share/claude/versions/2.1.150", "downloads.claude.ai", "", "443", ""},
		{"allow-always-list-home-robert-local-share-claude-versions-2-1-142-mcp-proxy-anthropic-com", "allow", "always", "/home/robert/.local/share/claude/versions/2.1.142", "mcp-proxy.anthropic.com", "", "", ""},
	}

	atoi := func(s string) uint32 {
		n, _ := strconv.ParseUint(s, 10, 32)
		return uint32(n)
	}

	for _, c := range cases {
		conn := &protocol.Connection{
			ProcessPath: c.path, DstHost: c.host, DstIp: c.ip,
			DstPort: atoi(c.port), UserId: atoi(c.uid),
		}
		d := Decision{Action: c.action, Duration: c.duration, Scope: Scope{
			Dest: c.host != "" || c.ip != "",
			Port: c.port != "",
			User: c.uid != "",
		}}
		if got := BuildRule(conn, d).GetName(); got != c.name {
			t.Errorf("rule would be renamed:\n  on disk: %s\n  built:   %s", c.name, got)
		}
	}
}

// A value that leaves nothing of itself in the slug still has to be
// distinguishable, or every such binary in a directory shares one rule.
func TestRuleNamesDisambiguateVanishedPaths(t *testing.T) {
	seen := map[string]string{}
	for _, p := range []string{"/usr/bin/日本", "/usr/bin/中文", "/usr/bin/한국"} {
		name := BuildRule(&protocol.Connection{ProcessPath: p},
			Decision{Action: ActionAllow, Duration: DurationAlways}).GetName()
		if prev, dup := seen[name]; dup {
			t.Errorf("%s and %s share the name %q", prev, p, name)
		}
		seen[name] = p
	}
}
