package main

import (
	"fmt"
	"os"

	as "github.com/godevsig/adaptiveservice"
	msg "github.com/godevsig/adaptiveservice/examples/hello/message"
)

func main() {
	var opts []as.Option
	if len(os.Args) > 1 && os.Args[1] == "-d" {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}
	s := as.NewServer(opts...).SetPublisher("example").EnableMessageTracer()

	knownMsgs := []as.KnownMessage{msg.HelloRequest{}}
	if err := s.Publish("hello", knownMsgs); err != nil {
		fmt.Println(err)
		return
	}

	if err := s.Serve(); err != nil { // ctrl+c to exit
		fmt.Println(err)
	}
}
