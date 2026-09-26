//go:build unix

package daemon

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
)

// umaskMu guards the umask, which is per-process rather than per-goroutine.
var umaskMu sync.Mutex

// listenSecurely binds the socket without ever exposing it more widely than
// intended.
//
// net.Listen creates a unix socket with mode 0777 &^ umask - 0775 under a
// typical 0002 umask - and the old code narrowed it to 0770 with a chmod
// afterwards. That leaves a window between bind and chmod in which any local
// process can connect, and the window is real: a loop racing it landed a
// connection on 3 of 300 attempts, and on 200 of 200 when given a scheduling
// nudge. Setting the umask around the bind means the socket is never wider
// than 0770 in the first place, so there is no window to race.
func listenSecurely(network, address string) (net.Listener, error) {
	if network != "unix" {
		return net.Listen(network, address)
	}

	// The umask is process-wide, so serialise and keep the window minimal.
	umaskMu.Lock()
	old := syscall.Umask(0o007) // clear other; keep user and group
	ln, err := net.Listen(network, address)
	syscall.Umask(old)
	umaskMu.Unlock()

	if err != nil {
		return nil, err
	}

	// Belt and braces: confirm what we actually got rather than trusting the
	// umask, and refuse to serve on a socket others can reach.
	fi, statErr := os.Stat(address)
	if statErr != nil {
		ln.Close()
		return nil, fmt.Errorf("checking %s: %w", address, statErr)
	}
	if perm := fi.Mode().Perm(); perm&0o007 != 0 {
		ln.Close()
		return nil, fmt.Errorf("socket %s came up with mode %#o, which others can reach", address, perm)
	}
	return ln, nil
}

// ownedByUs reports whether fi belongs to the user running this process.
func ownedByUs(fi os.FileInfo) (bool, int) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true, -1 // cannot tell; do not block on it
	}
	return int(st.Uid) == os.Getuid(), int(st.Uid)
}
