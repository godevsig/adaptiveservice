package server

import (
	as "github.com/godevsig/adaptiveservice"
)

// MessageRequest is the message sent by client.
type MessageRequest struct {
	Msg string
	Num int32
}

// MessageReply is the message replied by server,
// with Num+1 and a signature of "yours echo.v1.0".
type MessageReply struct {
	*MessageRequest
	Signature string
}

// Handle handles MessageRequest.
func (msg *MessageRequest) Handle(stream as.ContextStream) (reply interface{}) {
	msg.Msg = msg.Msg + "!"
	msg.Num++
	return &MessageReply{msg, "yours echo.v1.0"}
}

// Run runs the server.
func Run() {
	s := as.NewServer(as.WithLogger(as.LoggerAll{}), as.WithRegistryAddr("10.182.105.138:11985")).
		SetPublisher("example.org").
		SetBroadcastPort("9923").
		EnableRootRegistry("11985").
		EnableReverseProxy().
		EnableServiceLister()

	knownMsg := []as.KnownMessage{(*MessageRequest)(nil)}
	s.Publish("echo.v1.0", knownMsg)
	s.Serve()
}

func init() {
	as.RegisterType((*MessageRequest)(nil))
	as.RegisterType((*MessageReply)(nil))
}
