package resource

import (
	"net"
	"testing"
	"time"
)

func TestConnectionLimitedListenerWaitsForConnectionClose(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	underlying := &singleConnectionListener{connections: make(chan net.Conn, 2)}
	underlying.connections <- server
	listener := NewConnectionLimitedListener(underlying, 1)

	first, err := listener.Accept()
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}

	accepted := make(chan struct{})
	go func() {
		_, _ = listener.Accept()
		close(accepted)
	}()

	select {
	case <-accepted:
		t.Fatal("second Accept() completed before the first connection closed")
	case <-time.After(10 * time.Millisecond):
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	underlying.connections <- nil
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("second Accept() did not resume after connection close")
	}
}

func TestLimitedConnectionReleasesSlotOnce(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	underlying := &singleConnectionListener{connections: make(chan net.Conn, 1)}
	underlying.connections <- server
	listener := NewConnectionLimitedListener(underlying, 1)

	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

type singleConnectionListener struct {
	connections chan net.Conn
}

func (listener *singleConnectionListener) Accept() (net.Conn, error) {
	return <-listener.connections, nil
}

func (*singleConnectionListener) Close() error { return nil }

func (*singleConnectionListener) Addr() net.Addr { return &net.TCPAddr{} }
