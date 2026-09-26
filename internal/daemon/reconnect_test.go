package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// A daemon that goes quiet and comes back must re-notify the UI. Status and
// markSeen once judged "connected" differently, so during the watchdog's gap
// Status said disconnected while markSeen still saw the raw flag set and
// reported no change, leaving the UI stuck on "waiting".
func TestMarkSeenNotifiesAfterStaleGap(t *testing.T) {
	s := NewServer(NewStore(10), "", "", "test")
	s.markSeen("1.0")

	// Go quiet, without giving the watchdog a chance to clear the flag.
	s.mu.Lock()
	s.lastPing = time.Now().Add(-30 * time.Second)
	s.mu.Unlock()

	if live, _, _ := s.Status(); live {
		t.Fatal("Status should report a stale daemon as disconnected")
	}

	var mu sync.Mutex
	fired := 0
	s.OnStatus(func() { mu.Lock(); fired++; mu.Unlock() })

	s.markSeen("1.0") // the daemon comes back

	mu.Lock()
	got := fired
	mu.Unlock()
	if got != 1 {
		t.Fatalf("onStatus fired %d times on reconnect, want 1", got)
	}
	if live, _, _ := s.Status(); !live {
		t.Fatal("Status should report connected after markSeen")
	}
}

// A steady stream of pings must not re-fire the callback on every one.
func TestMarkSeenQuietWhileConnected(t *testing.T) {
	s := NewServer(NewStore(10), "", "", "test")
	var mu sync.Mutex
	fired := 0
	s.OnStatus(func() { mu.Lock(); fired++; mu.Unlock() })

	for i := 0; i < 5; i++ {
		s.markSeen("1.0")
	}
	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Fatalf("onStatus fired %d times, want 1 (connect only)", fired)
	}
}

// Ids must be unique even when two deletes land in the same nanosecond; a
// duplicate used to overwrite the first caller's entry in pending.
func TestNotificationIDsUnique(t *testing.T) {
	s := NewServer(NewStore(10), "", "", "test")
	const n = 2000
	seen := make(map[uint64]bool, n)
	var last uint64
	for i := 0; i < n; i++ {
		id := s.nextNotificationID()
		if seen[id] {
			t.Fatalf("duplicate notification id %d", id)
		}
		if id <= last && last != 0 {
			t.Fatalf("id %d not increasing after %d", id, last)
		}
		seen[id] = true
		last = id
	}
}

func TestNotificationIDsUniqueConcurrent(t *testing.T) {
	s := NewServer(NewStore(10), "", "", "test")
	var mu sync.Mutex
	seen := map[uint64]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 250; j++ {
				id := s.nextNotificationID()
				mu.Lock()
				if seen[id] {
					t.Errorf("duplicate id %d", id)
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// SetPrompter is documented as safe while serving; AskRule must not race it.
func TestSetPrompterConcurrentWithAskRule(t *testing.T) {
	s := NewServer(NewStore(100), ActionAllow, DurationOnce, "test")
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.SetPrompter(func(*protocol.Connection) (Decision, bool) {
					return Decision{Action: ActionDeny, Duration: DurationOnce}, true
				})
			}
		}
	}()

	for i := 0; i < 200; i++ {
		if _, err := s.AskRule(t.Context(), &protocol.Connection{ProcessPath: "/bin/sh"}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
