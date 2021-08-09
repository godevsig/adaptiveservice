package adaptiveservice

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
// Used for client side.
type Stream interface {
	// Send sends a message to the stream peer.
	Send(msg interface{}) error

	// Recv receives a message from the stream peer and stores it into the value
	// that msgPtr points to.
	//
	// If the peer's message handler returns error type, the error will be
	// returned by Recv() as error.
	//
	// msgPtr can be nil, where user only cares about error, otherwise
	// it panics if msgPtr is not a non-nil pointer.
	Recv(msgPtr interface{}) error

	// SendRecv combines send and receive on the same stream.
	SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error

	// RecvTimeout is Recv with timeout, not supported in raw mode.
	// It returns ErrRecvTimeout if timout happens in addition to Recv.
	//RecvTimeout(timeout time.Duration) (msg interface{}, err error)

	// GetNetConn gets the raw network connection.
	//GetNetConn() net.Conn
}

// ContextStream is a stream with an associated context.
// Messages from the same stream have the same context, their handlers
// may be executed concurrently.
// Used for server side.
type ContextStream interface {
	Context
	Stream
	sendNoPrivate(msg interface{}) error
}
