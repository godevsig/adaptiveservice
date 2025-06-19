package template

import (
	"fmt"

	as "github.com/godevsig/adaptiveservice"
)

// Handle handles msg.
func (req *Request) Handle(stream as.ContextStream) (reply any) {
	svc := stream.GetContext().(*service)
	return &Reply{Output: fmt.Sprintf("%s %s template", req.Input, svc.info)}
}

var knownMsgs = []as.KnownMessage{
	(*Request)(nil),
}
