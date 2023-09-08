package adaptiveservice

// KnownMessage represents a message with a handler that knows how to process the message.
// KnownMessage is normal priority message.
type KnownMessage interface {
	// Handle handles the message.
	// If reply is nil, no message will be sent back.
	// If reply is not nil, the value will be sent back to the stream peer.
	// If reply is error type, the peer's Recv() call will return it as error.
	// Otherwise the reply will be received by the peer's Recv() as normal message.
	//
	// The message may be marshaled or compressed.
	// Remember in golang assignment to interface is also value copy,
	// so return reply as &someStruct whenever possible in your handler implementation.
	//
	// Users can directly use ContextStream to send/receive messages to/from the stream
	// peer(the client) via a private dedicated channel.
	//
	// Use of reply is more like RPC fashion, where clients "call" the Handle() method
	// on the server side as if Handle() were called on clients.
	// Always return reply to client is a good idea since client is waiting for the reply.
	// In the case where only final status is needed, return builtin OK on success or
	// return error type if any error happened, e.g. return errors.New("some error").
	//
	// Use of ContextStream is more like client and server entered in an interactive
	// session, in which several messages are exchanged between client and server.
	//
	// Cares should be taken if you mix the use of reply and ContextStream.
	Handle(stream ContextStream) (reply interface{})
}

// HighPriorityMessage is high priority KnownMessage.
type HighPriorityMessage interface {
	KnownMessage
	IsHighPriority()
}

// LowPriorityMessage is Low priority KnownMessage.
type LowPriorityMessage interface {
	KnownMessage
	IsLowPriority()
}

type metaMsg struct {
	msg       any
	tracingID uuidptr
}

type metaKnownMsg struct {
	stream    ContextStream
	msg       KnownMessage
	tracingID uuidptr
}
