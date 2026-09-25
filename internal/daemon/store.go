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
type Store struct {
	mu      sync.RWMutex
	entries []Entry
	max     int
	seq     uint64
	onEvent func()
}

func NewStore(max int) *Store {
	if max <= 0 {
		max = 500
	}
	return &Store{max: max}
}

// OnEvent registers a callback fired after each Add. Used by the UI to refresh.
func (s *Store) OnEvent(fn func()) {
	s.mu.Lock()
	s.onEvent = fn
	s.mu.Unlock()
}

func (s *Store) Add(e Entry) {
	s.mu.Lock()
	// Newest first: the table is read top-down and new rows should appear there.
	s.entries = append([]Entry{e}, s.entries...)
	if len(s.entries) > s.max {
		s.entries = s.entries[:s.max]
	}
	s.seq++
	fn := s.onEvent
	s.mu.Unlock()

	if fn != nil {
		fn()
	}
}

// Snapshot returns a copy safe to read from the UI goroutine.
func (s *Store) Snapshot() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
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
	s.entries = nil
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
