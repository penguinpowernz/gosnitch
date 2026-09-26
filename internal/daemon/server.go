package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// Action and duration values the daemon understands. These strings are part of
// the wire contract; they match opensnitch's own config.py constants.
const (
	ActionAllow  = "allow"
	ActionDeny   = "deny"
	ActionReject = "reject"

	DurationOnce         = "once"
	DurationUntilRestart = "until restart"
	DurationAlways       = "always"
)

// Prompter decides what to do with a connection the daemon has no rule for.
// Returning ok=false (or a nil Prompter) falls back to the default action.
type Prompter func(*protocol.Connection) (Decision, bool)

// Server implements protocol.UIServer. OpenSnitch inverts the usual roles: the
// UI listens and the daemon connects to it, so this is a server, not a client.
type Server struct {
	protocol.UnimplementedUIServer

	store   *Store
	prompt  Prompter
	defAct  string
	defDur  string
	version string

	idMu   sync.Mutex
	lastID uint64

	mu        sync.RWMutex
	notify    *notifyStream
	connected bool
	daemonVer string
	lastPing  time.Time
	onStatus  func()
}

func NewServer(store *Store, defaultAction, defaultDuration, version string) *Server {
	if defaultAction == "" {
		defaultAction = ActionAllow
	}
	if defaultDuration == "" {
		defaultDuration = DurationOnce
	}
	return &Server{
		store:   store,
		defAct:  defaultAction,
		defDur:  defaultDuration,
		version: version,
	}
}

// SetDefaults changes what an unanswered prompt applies. Safe to call while
// serving: AskRule reads these under the same lock.
func (s *Server) SetDefaults(action, duration string) {
	s.mu.Lock()
	if action != "" {
		s.defAct = action
	}
	if duration != "" {
		s.defDur = duration
	}
	s.mu.Unlock()
}

// SetPrompter installs the interactive prompt. Safe to call while serving:
// AskRule reads it under the same lock.
func (s *Server) SetPrompter(p Prompter) {
	s.mu.Lock()
	s.prompt = p
	s.mu.Unlock()
}

// OnStatus fires when the daemon connects, disconnects or pings.
func (s *Server) OnStatus(fn func()) {
	s.mu.Lock()
	s.onStatus = fn
	s.mu.Unlock()
}

// Status reports whether the daemon is talking to us and which version it is.
func (s *Server) Status() (connected bool, daemonVersion string, lastPing time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.liveLocked(), s.daemonVer, s.lastPing
}

// staleAfter is how long a silence makes the daemon count as gone. It pings
// every second, so this is a generous margin.
const staleAfter = 10 * time.Second

// liveLocked reports whether the daemon counts as connected right now. Both
// Status and markSeen go through this: when they disagreed, a daemon that
// returned during the watchdog's 5s gap left the UI stuck on "waiting",
// because markSeen saw the raw flag still set and decided nothing had changed.
//
// Callers must hold s.mu (read or write).
func (s *Server) liveLocked() bool {
	return s.connected && time.Since(s.lastPing) < staleAfter
}

func (s *Server) markSeen(version string) {
	s.mu.Lock()
	was := s.liveLocked()
	s.connected = true
	s.lastPing = time.Now()
	if version != "" {
		s.daemonVer = version
	}
	fn := s.onStatus
	changed := !was
	s.mu.Unlock()
	if fn != nil && changed {
		fn()
	}
}

// Ping is the daemon's heartbeat; it carries the running statistics with it.
func (s *Server) Ping(ctx context.Context, req *protocol.PingRequest) (*protocol.PingReply, error) {
	s.markSeen(req.GetStats().GetDaemonVersion())
	return &protocol.PingReply{Id: req.GetId()}, nil
}

// Subscribe is the daemon introducing itself when it first connects.
func (s *Server) Subscribe(ctx context.Context, cfg *protocol.ClientConfig) (*protocol.ClientConfig, error) {
	s.markSeen(cfg.GetVersion())
	log.Printf("daemon subscribed: name=%s version=%s", cfg.GetName(), cfg.GetVersion())
	return cfg, nil
}

// AskRule is called when the daemon sees a connection no rule covers. Whatever
// we return here decides the connection's fate, so it must always answer.
func (s *Server) AskRule(ctx context.Context, conn *protocol.Connection) (*protocol.Rule, error) {
	s.markSeen("")

	s.mu.RLock()
	d := Decision{Action: s.defAct, Duration: s.defDur}
	prompt := s.prompt
	s.mu.RUnlock()
	// Called without the lock: the prompt blocks on the user, and holding
	// s.mu across that would stall every ping and status read behind it.
	if prompt != nil {
		if answer, ok := prompt(conn); ok {
			d = answer
		}
	}

	s.store.Add(EntryFromConn(conn, d.Action, time.Now()))

	return BuildRule(conn, d), nil
}

// notifyStream is the live Notifications stream to the daemon, plus the
// pending replies we are waiting on.
type notifyStream struct {
	send    func(*protocol.Notification) error
	pending map[uint64]chan *protocol.NotificationReply
	mu      sync.Mutex

	// gRPC forbids concurrent SendMsg on one stream, and two overlapping
	// DeleteRule calls would do exactly that. This is separate from mu so a
	// network write never blocks dispatching a reply that has already arrived.
	sendMu sync.Mutex
}

// sendNotification serialises writes to the stream.
func (ns *notifyStream) sendNotification(n *protocol.Notification) error {
	ns.sendMu.Lock()
	defer ns.sendMu.Unlock()
	return ns.send(n)
}

// Notifications is a bidirectional stream. The daemon is the gRPC client here,
// so we push Notification messages down to it and read its replies back.
func (s *Server) Notifications(stream protocol.UI_NotificationsServer) error {
	s.markSeen("")

	ns := &notifyStream{
		send:    stream.Send,
		pending: map[uint64]chan *protocol.NotificationReply{},
	}

	s.mu.Lock()
	s.notify = ns
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.notify == ns {
			s.notify = nil
		}
		s.mu.Unlock()

		// Release anyone still blocked on a reply.
		ns.mu.Lock()
		for id, ch := range ns.pending {
			close(ch)
			delete(ns.pending, id)
		}
		ns.mu.Unlock()
	}()

	for {
		reply, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		ns.mu.Lock()
		ch, ok := ns.pending[reply.GetId()]
		if ok {
			delete(ns.pending, reply.GetId())
		}
		ns.mu.Unlock()

		if ok {
			ch <- reply
			close(ch)
		}
	}
}

// nextNotificationID returns an id no in-flight notification is using.
//
// The Python UI derives ids from the clock, and matching that keeps ids from
// colliding across both clients if they ever run against one daemon. The clock
// alone is not enough on our side though: two DeleteRule calls in the same
// nanosecond produced the same id, and the second overwrote the first's entry
// in pending, leaving the first caller blocked until its context expired. The
// counter only ever bumps the id past one already handed out, so ids stay
// clock-ordered and stay unique.
func (s *Server) nextNotificationID() uint64 {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	id := uint64(time.Now().UnixNano())
	if id <= s.lastID {
		id = s.lastID + 1
	}
	s.lastID = id
	return id
}

// ErrNoDaemon is returned when a request needs the daemon but it is not
// currently connected.
var ErrNoDaemon = errors.New("opensnitchd is not connected")

// DeleteRule asks the daemon to delete a rule by name. The daemon owns the
// rule files, so this is the only way to remove one without root. It mirrors
// the Python UI: a Rule carrying just the name, with every other field blank.
func (s *Server) DeleteRule(ctx context.Context, name string) error {
	s.mu.RLock()
	ns := s.notify
	s.mu.RUnlock()

	if ns == nil {
		return ErrNoDaemon
	}

	id := s.nextNotificationID()

	ch := make(chan *protocol.NotificationReply, 1)
	ns.mu.Lock()
	ns.pending[id] = ch
	ns.mu.Unlock()

	notif := &protocol.Notification{
		Id:   id,
		Type: protocol.Action_DELETE_RULE,
		Rules: []*protocol.Rule{{
			Name:     name,
			Enabled:  false,
			Action:   "",
			Duration: "",
			Operator: &protocol.Operator{Type: "", Operand: "", Data: ""},
		}},
	}

	if err := ns.sendNotification(notif); err != nil {
		ns.mu.Lock()
		delete(ns.pending, id)
		ns.mu.Unlock()
		return fmt.Errorf("sending delete for %q: %w", name, err)
	}

	select {
	case reply, ok := <-ch:
		if !ok {
			return fmt.Errorf("daemon disconnected while deleting %q", name)
		}
		if reply.GetCode() != protocol.NotificationReplyCode_OK {
			return fmt.Errorf("daemon refused to delete %q: %s", name, reply.GetData())
		}
		return nil
	case <-ctx.Done():
		ns.mu.Lock()
		delete(ns.pending, id)
		ns.mu.Unlock()
		return ctx.Err()
	}
}

func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Listen opens the socket the daemon will connect to. addr uses the daemon's
// own config syntax, e.g. "unix:///tmp/osui.sock" or "127.0.0.1:50051".
func Listen(addr string) (net.Listener, error) {
	network, address := "tcp", addr
	if u, err := url.Parse(addr); err == nil && u.Scheme == "unix" {
		network, address = "unix", u.Path
		// A stale socket from a previous run would block binding.
		if fi, err := os.Stat(address); err == nil && fi.Mode()&os.ModeSocket != 0 {
			os.Remove(address)
		}
	}

	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	if network == "unix" {
		// The daemon runs as root and connects in; the socket must be writable
		// by it while staying out of other users' reach. If this fails the
		// socket keeps Go's default 0777, which is wider than intended, so
		// fail rather than quietly listening on a world-writable socket.
		if err := os.Chmod(address, 0o770); err != nil {
			ln.Close()
			return nil, fmt.Errorf("securing %s: %w", address, err)
		}
	}
	return ln, nil
}

// Serve registers the UI service and blocks until the listener is closed.
func (s *Server) Serve(ln net.Listener) error {
	gs := grpc.NewServer(
		// The daemon can send sizable statistics payloads on Ping.
		grpc.MaxRecvMsgSize(32 * 1024 * 1024),
	)
	protocol.RegisterUIServer(gs, s)

	go func() {
		// Flip the UI to "disconnected" when pings stop arriving.
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			s.mu.Lock()
			dropped := s.connected && time.Since(s.lastPing) > staleAfter
			if dropped {
				s.connected = false
			}
			fn := s.onStatus
			s.mu.Unlock()

			if dropped && fn != nil {
				fn()
			}
		}
	}()

	return gs.Serve(ln)
}
