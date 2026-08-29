package proxy

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDialWithContext_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()
	_, err := dialWithContext(ctx, func() (net.Conn, error) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		return nil, errors.New("should be cancelled first")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}
