package main

import (
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	msg "github.com/godevsig/adaptiveservice/examples/hello/message"
)

func main() {
	c := as.NewClient()

	conn := <-c.Discover("example", "hello")
	if conn == nil {
		fmt.Println(as.ErrServiceNotFound("example", "hello"))
		return
	}
	defer conn.Close()

	request := msg.HelloRequest{Who: "John", Question: "who are you"}
	var reply msg.HelloReply
	if err := conn.SendRecv(request, &reply); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(reply.Answer)
}
