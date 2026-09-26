package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func mkEntry(i int) Entry {
	return Entry{Time: time.Unix(int64(i), 0), Process: fmt.Sprintf("p%d", i)}
}

func names(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Process
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Snapshot is newest-first at every stage: empty, partly full, exactly full,
// and wrapped several times over.
func TestStoreSnapshotOrder(t *testing.T) {
	s := NewStore(3)

	if got := s.Snapshot(); len(got) != 0 {
		t.Fatalf("empty store returned %v", names(got))
	}

	s.Add(mkEntry(1))
	if got := names(s.Snapshot()); !eq(got, []string{"p1"}) {
		t.Errorf("after 1: %v", got)
	}

	s.Add(mkEntry(2))
	if got := names(s.Snapshot()); !eq(got, []string{"p2", "p1"}) {
		t.Errorf("after 2: %v", got)
	}

	s.Add(mkEntry(3)) // exactly full
	if got := names(s.Snapshot()); !eq(got, []string{"p3", "p2", "p1"}) {
		t.Errorf("full: %v", got)
	}

	s.Add(mkEntry(4)) // wraps: p1 falls off
	if got := names(s.Snapshot()); !eq(got, []string{"p4", "p3", "p2"}) {
		t.Errorf("after 1 wrap: %v", got)
	}

	s.Add(mkEntry(5))
	s.Add(mkEntry(6))
	if got := names(s.Snapshot()); !eq(got, []string{"p6", "p5", "p4"}) {
		t.Errorf("after full wrap: %v", got)
	}

	// Several laps round the ring must not disturb the ordering.
	for i := 7; i <= 30; i++ {
		s.Add(mkEntry(i))
	}
	if got := names(s.Snapshot()); !eq(got, []string{"p30", "p29", "p28"}) {
		t.Errorf("after many wraps: %v", got)
	}
}

// The store must never hand back more than max, however many events arrive.
func TestStoreRespectsMax(t *testing.T) {
	for _, max := range []int{1, 2, 7, 100} {
		s := NewStore(max)
		for i := range max * 3 {
			s.Add(mkEntry(i))
		}
		if n := len(s.Snapshot()); n != max {
			t.Errorf("max %d: snapshot has %d entries", max, n)
		}
		// Newest first, and the newest is the last one added.
		if got := s.Snapshot()[0].Process; got != fmt.Sprintf("p%d", max*3-1) {
			t.Errorf("max %d: newest is %s", max, got)
		}
	}
}

// Clear resets the ring, and adding afterwards starts clean rather than
// resurrecting entries left in the backing array.
func TestStoreClearResetsRing(t *testing.T) {
	s := NewStore(3)
	for i := range 5 {
		s.Add(mkEntry(i))
	}
	s.Clear()
	if got := s.Snapshot(); len(got) != 0 {
		t.Fatalf("after Clear: %v", names(got))
	}
	s.Add(mkEntry(99))
	if got := names(s.Snapshot()); !eq(got, []string{"p99"}) {
		t.Errorf("after Clear+Add: %v", got)
	}
}

// Snapshot must be a copy: mutating it cannot reach into the store.
func TestStoreSnapshotIsACopy(t *testing.T) {
	s := NewStore(3)
	s.Add(mkEntry(1))
	snap := s.Snapshot()
	snap[0].Process = "tampered"
	if got := s.Snapshot()[0].Process; got != "p1" {
		t.Errorf("store was mutated through the snapshot: %s", got)
	}
}

// A snapshot taken before more events arrive must not change underneath the
// UI goroutine that is rendering it.
func TestStoreSnapshotStableWhileAdding(t *testing.T) {
	s := NewStore(4)
	for i := range 4 {
		s.Add(mkEntry(i))
	}
	snap := s.Snapshot()
	before := names(snap)
	for i := 100; i < 120; i++ {
		s.Add(mkEntry(i))
	}
	if !eq(names(snap), before) {
		t.Errorf("snapshot changed: %v -> %v", before, names(snap))
	}
}

func TestStoreConcurrent(t *testing.T) {
	s := NewStore(50)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				s.Add(mkEntry(g*1000 + i))
				if n := len(s.Snapshot()); n > 50 {
					t.Errorf("snapshot exceeded max: %d", n)
				}
			}
		}()
	}
	wg.Wait()
	if n := len(s.Snapshot()); n != 50 {
		t.Errorf("final size %d, want 50", n)
	}
	if s.Seq() != 1600 {
		t.Errorf("seq = %d, want 1600", s.Seq())
	}
}

// Add is on a path any local process can drive as fast as it likes, so it
// must not allocate. Prepending to a slice copied the whole buffer per event.
func BenchmarkStoreAdd(b *testing.B) {
	s := NewStore(1000)
	e := Entry{Time: time.Now(), Action: "allow", Process: "/usr/bin/curl", Dest: "example.com"}
	for b.Loop() {
		s.Add(e)
	}
}

func BenchmarkStoreSnapshot(b *testing.B) {
	s := NewStore(1000)
	for i := range 1000 {
		s.Add(mkEntry(i))
	}
	b.ResetTimer()
	for b.Loop() {
		_ = s.Snapshot()
	}
}
