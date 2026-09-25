// Command gosnitch is a lightweight Fyne front-end for the OpenSnitch firewall
// daemon. OpenSnitch inverts the usual client/server roles: the UI listens on a
// socket and opensnitchd connects to it, so this process is the gRPC server.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
	"github.com/penguinpowernz/gosnitch/internal/ui"
)

const version = "0.1.0"

func main() {
	var (
		addr        = flag.String("address", defaultAddress(), "address to listen on for the daemon (unix:///path or host:port)")
		rulesPath   = flag.String("rules", daemon.DefaultRulesPath, "directory opensnitchd keeps its rules in")
		defAction   = flag.String("default-action", daemon.ActionAllow, "initial action when a prompt is not answered (allow|deny|reject); changeable from the tray")
		interactive = flag.Bool("interactive", true, "prompt on connections with no matching rule")
		hidden      = flag.Bool("hidden", false, "start with the window hidden in the tray")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		log.SetFlags(0)
		log.Println("gosnitch", version)
		return
	}

	if err := validAction(*defAction); err != nil {
		log.Fatal(err)
	}

	store := daemon.NewStore(1000)
	// The duration a timeout applies is fixed at "once" so an unanswered
	// prompt can never create a lasting rule.
	srv := daemon.NewServer(store, *defAction, ui.FallbackDuration, version)

	ln, err := daemon.Listen(*addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v\n\nIs another OpenSnitch UI already running?", *addr, err)
	}
	log.Printf("gosnitch %s listening on %s", version, *addr)

	go func() {
		if err := srv.Serve(ln); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	// Remove the unix socket on the way out so the next start can bind.
	cleanup := func() { ln.Close() }
	defer cleanup()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cleanup()
		os.Exit(0)
	}()

	a := ui.New(store, srv, ui.Options{
		RulesPath:     *rulesPath,
		DefaultAction: *defAction,
		Interactive:   *interactive,
		StartHidden:   *hidden,
	})
	a.Run()
}

func defaultAddress() string {
	if v := os.Getenv("GOSNITCH_ADDRESS"); v != "" {
		return v
	}
	return "unix:///tmp/osui.sock"
}

func validAction(a string) error {
	switch a {
	case daemon.ActionAllow, daemon.ActionDeny, daemon.ActionReject:
		return nil
	}
	return fmt.Errorf("invalid -default-action %q: want allow, deny or reject", a)
}
