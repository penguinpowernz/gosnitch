package daemon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// startServer runs a Server on a pipe listener and returns a connected client.
func startServer(t *testing.T) (*Server, protocol.UIClient) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(NewStore(10), ActionAllow, DurationOnce, "test")
	gs := grpc.NewServer()
	protocol.RegisterUIServer(gs, s)
	go gs.Serve(ln)
	t.Cleanup(gs.Stop)

	cc, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	return s, protocol.NewUIClient(cc)
}

// The daemon side of the Notifications stream: echo back the given code.
func runFakeDaemon(t *testing.T, c protocol.UIClient, code protocol.NotificationReplyCode, data string) chan *protocol.Notification {
	t.Helper()
	got := make(chan *protocol.Notification, 4)
	stream, err := c.Notifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			n, err := stream.Recv()
			if err != nil {
				return
			}
			got <- n
			stream.Send(&protocol.NotificationReply{Id: n.GetId(), Code: code, Data: data})
		}
	}()
	return got
}

func TestDeleteRuleSendsCorrectNotification(t *testing.T) {
	s, c := startServer(t)
	got := runFakeDaemon(t, c, protocol.NotificationReplyCode_OK, "")
	waitForStream(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.DeleteRule(ctx, "allow-always-simple-usr-bin-curl"); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	select {
	case n := <-got:
		if n.GetType() != protocol.Action_DELETE_RULE {
			t.Errorf("type = %v, want DELETE_RULE", n.GetType())
		}
		if len(n.GetRules()) != 1 {
			t.Fatalf("got %d rules", len(n.GetRules()))
		}
		r := n.GetRules()[0]
		if r.GetName() != "allow-always-simple-usr-bin-curl" {
			t.Errorf("name = %q", r.GetName())
		}
		// The Python UI blanks every field but the name; match it exactly.
		if r.GetEnabled() || r.GetAction() != "" || r.GetDuration() != "" {
			t.Errorf("non-name fields should be blank, got %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon never received the notification")
	}
}

func TestDeleteRuleFailsWithoutDaemon(t *testing.T) {
	s := NewServer(NewStore(1), ActionAllow, DurationOnce, "test")
	err := s.DeleteRule(context.Background(), "whatever")
	if !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("got %v, want ErrNoDaemon", err)
	}
}

func TestDeleteRuleSurfacesDaemonError(t *testing.T) {
	s, c := startServer(t)
	runFakeDaemon(t, c, protocol.NotificationReplyCode_ERROR, "rule is read-only")
	waitForStream(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := s.DeleteRule(ctx, "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !contains(err.Error(), "read-only") {
		t.Fatalf("error should carry the daemon's reason, got %v", err)
	}
}

// A daemon that never replies must not block the UI forever.
func TestDeleteRuleRespectsContext(t *testing.T) {
	s, c := startServer(t)
	stream, err := c.Notifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
			// Deliberately never reply.
		}
	}()
	waitForStream(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := s.DeleteRule(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
}

func waitForStream(t *testing.T, s *Server) {
	t.Helper()
	for i := 0; i < 100; i++ {
		s.mu.RLock()
		ok := s.notify != nil
		s.mu.RUnlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("notifications stream never registered")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
