package adaptiveservice

import (
	"net"
	"time"
)

// Netconn is the underlying net connection.
type Netconn interface {
	// Close closes the connection.
	// Any blocked Read or Write operations will be unblocked and return errors.
	Close() error
	// LocalAddr returns the local network address.
	LocalAddr() net.Addr
	// RemoteAddr returns the remote network address.
	RemoteAddr() net.Addr
}

// Connection is the connection between client and server.
type Connection interface {
	// default stream.
	Stream
	// NewStream creates a new stream.
	NewStream() Stream
	// Close closes the connection.
	Close()
}

// Stream is an independent channel multiplexed from the underlying connection.
type Stream interface {
	// Send sends a message to the stream peer. If msg is an error value, it will
	// be received and returned by peer's Recv() as error.
	Send(msg interface{}) error

	// Recv receives a message from the stream peer and stores it into the value
	// that msgPtr points to.
	//
	// msgPtr can be nil, where user only cares about error, otherwise
	// it panics if msgPtr is not a non-nil pointer.
	Recv(msgPtr interface{}) error

	// SendRecv combines send and receive on the same stream.
	SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error

	// RecvTimeout is Recv with timeout, returns ErrRecvTimeout if timout happens.
	//RecvTimeout(msgPtr interface{}, timeout time.Duration) error

	// GetNetconn gets the transport connection.
	GetNetconn() Netconn

	// SetRecvTimeout sets the timeout for each Recv(), which waits at least duration
	// d and returns ErrRecvTimeout if no data was received within that duration.
	// A negative or zero duration causes Recv() waits forever.
	// Default is 0.
	SetRecvTimeout(d time.Duration)
}

// ContextStream is a stream with an associated context.
// Messages from the same stream have the same context, their handlers
// may be executed concurrently.
type ContextStream interface {
	Context
	Stream
}

type timeouter struct {
	d time.Duration
}

func (to *timeouter) SetRecvTimeout(d time.Duration) {
	to.d = d
}

func (to *timeouter) timeoutChan() (timeoutChan chan struct{}) {
	if to.d > 0 {
		timeoutChan = make(chan struct{}, 1)
		time.AfterFunc(to.d, func() { timeoutChan <- struct{}{} })
	}
	return
}
