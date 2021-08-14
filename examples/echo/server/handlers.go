package server

import as "github.com/godevsig/adaptiveservice"

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

func init() {
	as.RegisterType((*MessageRequest)(nil))
	as.RegisterType((*MessageReply)(nil))
}
