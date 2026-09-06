package proxy

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestDialWithContext_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()
	_, err := dialWithContext(ctx, func() (net.Conn, error) {
		close(started)
		<-release // the dial only finishes after the caller has given up
		return nil, errors.New("should be cancelled first")
	})
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}
