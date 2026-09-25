package main

// Development helper: pretends to be opensnitchd so the UI can be exercised
// without root privileges. Build it explicitly; it is not part of gosnitch.

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

var samples = []struct {
	path, host, ip, proto string
	port                  uint32
}{
	{"/usr/bin/firefox", "www.mozilla.org", "63.245.208.195", "tcp", 443},
	{"/usr/bin/curl", "api.github.com", "140.82.113.6", "tcp", 443},
	{"/usr/lib/systemd/systemd-resolved", "", "1.1.1.1", "udp", 53},
	{"/usr/bin/ssh", "git.example.net", "203.0.113.42", "tcp", 22},
	{"/opt/spotify/spotify", "audio-fa.scdn.co", "35.186.224.25", "tcp", 443},
	{"/usr/bin/apt", "deb.debian.org", "151.101.66.132", "tcp", 80},
}

func main() {
	addr := flag.String("address", "unix:///tmp/gosnitch-test.sock", "gosnitch address")
	every := flag.Duration("every", 1500*time.Millisecond, "interval between connections")
	count := flag.Int("count", 0, "number of connections to send (0 = forever)")
	flag.Parse()

	cc, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer cc.Close()
	c := protocol.NewUIClient(cc)
	ctx := context.Background()

	if _, err := c.Subscribe(ctx, &protocol.ClientConfig{Id: 1, Name: "mock-node", Version: "1.5.8"}); err != nil {
		log.Fatal("Subscribe: ", err)
	}
	log.Println("subscribed as mock-node")

	go func() {
		for i := uint64(0); ; i++ {
			c.Ping(ctx, &protocol.PingRequest{Id: i, Stats: &protocol.Statistics{DaemonVersion: "1.5.8"}})
			time.Sleep(time.Second)
		}
	}()

	for i := 0; *count == 0 || i < *count; i++ {
		s := samples[rand.Intn(len(samples))]
		rule, err := c.AskRule(ctx, &protocol.Connection{
			Protocol: s.proto, SrcIp: "192.168.1.10", SrcPort: uint32(40000 + rand.Intn(20000)),
			DstIp: s.ip, DstHost: s.host, DstPort: s.port,
			UserId: 1000, ProcessId: uint32(1000 + i),
			ProcessPath: s.path, ProcessArgs: []string{s.path},
		})
		if err != nil {
			log.Fatal("AskRule: ", err)
		}
		// Print the whole rule: the operand list is the part worth eyeballing
		// against a real file in /etc/opensnitchd/rules.
		fmt.Printf("ask %-30s -> %s/%s  [%s]\n", s.path, rule.GetAction(), rule.GetDuration(), rule.GetOperator().GetType())
		fmt.Printf("    name: %s\n", rule.GetName())
		if d := rule.GetOperator().GetData(); d != "" {
			fmt.Printf("    data: %s\n", d)
		}
		time.Sleep(*every)
	}
}
