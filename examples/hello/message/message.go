package message

import (
	"strings"

	as "github.com/godevsig/adaptiveservice"
)

// HelloRequest is the request from clients
type HelloRequest struct {
	Who      string
	Question string
}

// HelloReply is the reply of HelloRequest to clients
type HelloReply struct {
	Answer string
}

// Handle handles msg.
func (msg HelloRequest) Handle(stream as.ContextStream) (reply interface{}) {
	answer := "I don't know"

	question := strings.ToLower(msg.Question)
	switch {
	case strings.Contains(question, "who are you"):
		answer = "I am hello server"
	case strings.Contains(question, "how are you"):
		answer = "I am good"
	}
	return HelloReply{answer + ", " + msg.Who}
}

func init() {
	as.RegisterType(HelloRequest{})
	as.RegisterType(HelloReply{})
}
