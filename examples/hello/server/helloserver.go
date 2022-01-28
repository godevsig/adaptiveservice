package main

import (
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	msg "github.com/godevsig/adaptiveservice/examples/hello/message"
)

func main() {
	s := as.NewServer().SetPublisher("example")

	knownMsgs := []as.KnownMessage{msg.HelloRequest{}}
	if err := s.Publish("hello", knownMsgs); err != nil {
		fmt.Println(err)
		return
	}

	if err := s.Serve(); err != nil { // ctrl+c to exit
		fmt.Println(err)
	}
}
