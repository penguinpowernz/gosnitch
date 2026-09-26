//go:build !unix

package daemon

import (
	"net"
	"os"
)

// listenSecurely is the plain bind on platforms without a umask. OpenSnitch is
// Linux-only, so this exists to keep the package building rather than to be
// used; unix sockets are handled properly in listen_unix.go.
func listenSecurely(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}

// ownedByUs cannot be answered without stat's uid, so it does not block.
func ownedByUs(fi os.FileInfo) (bool, int) { return true, -1 }
