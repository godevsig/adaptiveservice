package adaptiveservice

import (
	"encoding/binary"
	"fmt"
	"io"
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
	// Close closes the stream
	Close()
}

// streamCloseMsg is a special message sent by client to close the stream.
type streamCloseMsg struct{}

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

// for clients
func handshakeWithServer(netconn net.Conn) (info string, err error) {
	buf := net.Buffers{}
	bufSize := make([]byte, 2)
	marker := fmt.Sprintf("Adaptiveservice Spec Version %s", specVersion)
	bufMsg := []byte(marker)
	binary.BigEndian.PutUint16(bufSize, uint16(len(bufMsg)))
	buf = append(buf, bufSize, bufMsg)
	// |size(2bytes)|marker(variable size)|
	if _, err := buf.WriteTo(netconn); err != nil {
		return "", fmt.Errorf("failed to write marker: %w", err)
	}
	return "Local " + marker, nil
}

// for servers
func handshakeWithClient(netconn net.Conn) (info string, err error) {
	bufSize := make([]byte, 2)
	if _, err := io.ReadFull(netconn, bufSize); err != nil {
		return "", fmt.Errorf("failed to read marker size: %w", err)
	}
	size := binary.BigEndian.Uint16(bufSize)
	bufMsg := make([]byte, size)
	if _, err := io.ReadFull(netconn, bufMsg); err != nil {
		return "", fmt.Errorf("failed to read marker: %w", err)
	}
	marker := string(bufMsg)
	var specVer string
	if _, err := fmt.Sscanf(marker, "Adaptiveservice Spec Version %s", &specVer); err != nil {
		return "", fmt.Errorf("failed to parse SPEC version: %w", err)
	}
	// ToDo: check specVer with specVersion
	return fmt.Sprintf("Adaptiveservice Spec Version: Local %s, Remote %s", specVersion, specVer), nil
}

func init() {
	RegisterType(streamCloseMsg{})
}
