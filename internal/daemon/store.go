package daemon

import (
	"sync"
	"time"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Entry is one connection event, flattened into just the fields the table shows.
type Entry struct {
	Time    time.Time
	Action  string // allow / deny / reject, or "" when the daemon only reported it
	Process string
	Dest    string
	Port    uint32
	Proto   string
	UserID  uint32
	PID     uint32
}

// Store keeps the most recent events in a ring buffer. It is the only state
// shared between the gRPC server and the UI, so every method is safe to call
// from any goroutine.
//
// The buffer really is a ring: entries is used circularly with next pointing
// at the slot to write. Prepending to a slice instead, to keep it newest-first
// in memory, copied the whole buffer on every event - 105KB of garbage per
// connection at max 1000 - which is a lot of churn on a path any local process
// can drive as fast as it likes. Snapshot does the reordering instead, once
// per redraw rather than once per event.
type Store struct {
	mu      sync.RWMutex
	entries []Entry // circular; len() is the capacity once filled
	next    int     // index of the next slot to write
	filled  bool    // whether the ring has wrapped at least once
	max     int
	seq     uint64
	onEvent func()
}

func NewStore(max int) *Store {
	if max <= 0 {
		max = 500
	}
	return &Store{max: max, entries: make([]Entry, 0, max)}
}

// OnEvent registers a callback fired after each Add. Used by the UI to refresh.
func (s *Store) OnEvent(fn func()) {
	s.mu.Lock()
	s.onEvent = fn
	s.mu.Unlock()
}

func (s *Store) Add(e Entry) {
	s.mu.Lock()
	if len(s.entries) < s.max {
		// Still growing into the ring.
		s.entries = append(s.entries, e)
		s.next = len(s.entries) % s.max
		if s.next == 0 {
			s.filled = true
		}
	} else {
		// Full: overwrite the oldest, which is wherever next points.
		s.entries[s.next] = e
		s.next = (s.next + 1) % s.max
		s.filled = true
	}
	s.seq++
	fn := s.onEvent
	s.mu.Unlock()

	if fn != nil {
		fn()
	}
}

// Snapshot returns a copy safe to read from the UI goroutine, newest first:
// the table is read top-down and new rows should appear at the top.
func (s *Store) Snapshot() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, 0, len(s.entries))
	// Walk backwards from the most recently written slot.
	for i := 0; i < len(s.entries); i++ {
		idx := s.next - 1 - i
		if idx < 0 {
			if !s.filled {
				break // nothing wrapped around; the ring starts at 0
			}
			idx += len(s.entries)
		}
		out = append(out, s.entries[idx])
	}
	return out
}

// Seq changes on every Add, letting the UI skip redraws when nothing is new.
func (s *Store) Seq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

func (s *Store) Clear() {
	s.mu.Lock()
	s.entries = s.entries[:0]
	s.next = 0
	s.filled = false
	s.seq++
	fn := s.onEvent
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// EntryFromConn flattens a daemon Connection into a table row.
func EntryFromConn(c *protocol.Connection, action string, when time.Time) Entry {
	e := Entry{
		Time:    when,
		Action:  action,
		Dest:    c.GetDstHost(),
		Port:    c.GetDstPort(),
		Proto:   c.GetProtocol(),
		UserID:  c.GetUserId(),
		PID:     c.GetProcessId(),
		Process: c.GetProcessPath(),
	}
	if e.Dest == "" {
		e.Dest = c.GetDstIp()
	}
	return e
}
