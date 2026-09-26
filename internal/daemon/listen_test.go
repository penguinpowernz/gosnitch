//go:build unix

package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shortTempDir keeps paths clear of the 107-byte sun_path limit; the usual
// t.TempDir() under a long test path can exceed it on its own.
func shortTempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// The socket must never be reachable by other users, not even for the instant
// between bind and chmod. net.Listen creates it 0777 &^ umask, so the old
// chmod-afterwards left a window a racing process won every time.
func TestListenSocketIsNeverWorldAccessible(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.sock")

	// Watch the mode from the moment the path appears.
	var sawOpen atomic.Bool
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if fi, err := os.Lstat(path); err == nil {
				if fi.Mode().Perm()&0o007 != 0 {
					sawOpen.Store(true)
					return
				}
			}
		}
	}()

	ln, err := Listen("unix://" + path)
	if err != nil {
		close(stop)
		<-done
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	time.Sleep(50 * time.Millisecond)
	close(stop)
	<-done

	if sawOpen.Load() {
		t.Error("socket was reachable by other users at some point")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o007 != 0 {
		t.Errorf("final mode %#o lets others in", perm)
	}
}

// The umask is process-wide, so Listen must put it back.
func TestListenRestoresUmask(t *testing.T) {
	dir := shortTempDir(t)

	before := currentUmask()
	ln, err := Listen("unix://" + filepath.Join(dir, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if after := currentUmask(); after != before {
		t.Errorf("umask changed from %#o to %#o", before, after)
	}

	// A file created afterwards must have the ordinary permissions.
	f := filepath.Join(dir, "probe")
	if err := os.WriteFile(f, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(f)
	if fi.Mode().Perm() == 0o660 && before != 0o007 {
		t.Errorf("file created with %#o; the umask looks left behind", fi.Mode().Perm())
	}
}

// A socket left by a previous run of ours is cleared; anything else is not.
func TestListenStaleSocketHandling(t *testing.T) {
	dir := shortTempDir(t)

	t.Run("our own stale socket is replaced", func(t *testing.T) {
		path := filepath.Join(dir, "stale.sock")
		old, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		old.Close()
		// Closing unlinks it, so recreate one to stand in for a crashed run.
		old2, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		_ = old2 // deliberately leaked: the file stays behind

		ln, err := Listen("unix://" + path)
		if err != nil {
			t.Fatalf("should have replaced our own stale socket: %v", err)
		}
		ln.Close()
	})

	t.Run("a regular file is refused", func(t *testing.T) {
		path := filepath.Join(dir, "notasock")
		if err := os.WriteFile(path, []byte("important"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Listen("unix://" + path)
		if err == nil {
			t.Fatal("expected a refusal")
		}
		if !strings.Contains(err.Error(), "not a socket") {
			t.Errorf("unhelpful error: %v", err)
		}
		// Critically, it must still be there.
		if b, err := os.ReadFile(path); err != nil || string(b) != "important" {
			t.Error("the file was destroyed")
		}
	})

	t.Run("a symlink is not followed", func(t *testing.T) {
		victim := filepath.Join(dir, "victim")
		if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.sock")
		if err := os.Symlink(victim, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Listen("unix://" + link); err == nil {
			t.Fatal("expected a refusal")
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "keep" {
			t.Error("the symlink target was touched")
		}
	})
}

// Malformed addresses must say what is wrong rather than binding something
// unexpected. "unix://foo.sock" used to parse foo.sock as a host and bind "".
func TestListenRejectsMalformedAddresses(t *testing.T) {
	for _, addr := range []string{
		"unix://relative.sock",
		"unix://",
		"unix:///" + strings.Repeat("a", maxUnixPath+10),
	} {
		ln, err := Listen(addr)
		if err == nil {
			ln.Close()
			t.Errorf("Listen(%q) succeeded, want an error", addr)
		}
	}
}

func TestListenTCPStillWorks(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, ok := ln.Addr().(*net.TCPAddr); !ok {
		t.Errorf("got %T, want a TCP listener", ln.Addr())
	}
}

func currentUmask() int {
	old := setUmask(0)
	setUmask(old)
	return old
}
