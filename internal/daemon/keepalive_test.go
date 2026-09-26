package daemon

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

// The Notifications stream is held open for the life of the daemon and is
// idle by design between deletes, so MaxConnectionIdle must not reap it.
// grpc counts idleness from when outstanding RPCs hit zero, and a streaming
// RPC never lets that happen - this pins that, because getting it wrong would
// silently drop the daemon connection after a minute of quiet.
func TestIdleStreamSurvives(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	s := NewServer(NewStore(10), ActionAllow, DurationOnce, "test")
	gs := grpc.NewServer(
		grpc.MaxConcurrentStreams(maxConcurrentStreams),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 300 * time.Millisecond, // shrunk for the test
			Time:              20 * time.Second,
			Timeout:           10 * time.Second,
		}),
	)
	protocol.RegisterUIServer(gs, s)
	go gs.Serve(ln)
	t.Cleanup(gs.Stop)

	cc, _ := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	t.Cleanup(func() { cc.Close() })
	c := protocol.NewUIClient(cc)

	stream, err := c.Notifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitForStream(t, s)

	// Well past MaxConnectionIdle with no traffic at all.
	time.Sleep(time.Second)

	// The stream must still be usable: a delete has to reach the daemon.
	go func() {
		n, err := stream.Recv()
		if err == nil {
			stream.Send(&protocol.NotificationReply{Id: n.GetId(), Code: protocol.NotificationReplyCode_OK})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.DeleteRule(ctx, "r"); err != nil {
		t.Fatalf("stream died while idle: %v", err)
	}
}
