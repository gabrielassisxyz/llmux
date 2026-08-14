package resource

import (
	"net"
	"sync"
)

// ConnectionLimitedListener prevents more than limit accepted connections from being live.
type ConnectionLimitedListener struct {
	net.Listener
	slots chan struct{}
}

// NewConnectionLimitedListener wraps listener with a live-connection ceiling.
func NewConnectionLimitedListener(listener net.Listener, limit int) *ConnectionLimitedListener {
	if limit < 1 {
		panic("connection limit must be positive")
	}
	return &ConnectionLimitedListener{Listener: listener, slots: make(chan struct{}, limit)}
}

func (listener *ConnectionLimitedListener) Accept() (net.Conn, error) {
	listener.slots <- struct{}{}
	connection, err := listener.Listener.Accept()
	if err != nil {
		<-listener.slots
		return nil, err
	}
	return &limitedConn{Conn: connection, release: listener.release}, nil
}

func (listener *ConnectionLimitedListener) release() {
	<-listener.slots
}

type limitedConn struct {
	net.Conn
	release func()
	once    sync.Once
	err     error
}

func (connection *limitedConn) Close() error {
	connection.once.Do(func() {
		connection.err = connection.Conn.Close()
		connection.release()
	})
	return connection.err
}
