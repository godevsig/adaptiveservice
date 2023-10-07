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
	c := as.NewClient(opts...)

	conn := <-c.Discover("example", "hello")
	if conn == nil {
		fmt.Println(as.ErrServiceNotFound("example", "hello"))
		return
	}
	defer conn.Close()

	// tracing only one session
	token, err := as.TraceMsgByType(msg.HelloRequest{}, 1)
	if err != nil {
		fmt.Println(err)
	}

	request := msg.HelloRequest{Who: "J", Question: "who are you"}
	for i := 0; i < 5; i++ {
		var reply msg.HelloReply
		if err := conn.SendRecv(request, &reply); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(reply.Answer)
	}

	// traced message records will be read cleared
	msgs, err := as.ReadTracedMsg(token)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println()
	fmt.Println("tracing records with token:", token)
	fmt.Println(msgs)

	// should be empty
	msgs, err = as.ReadTracedMsg(token)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(len(msgs))
}
