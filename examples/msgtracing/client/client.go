package main

import (
	"flag"
	"fmt"
	"time"

	as "github.com/godevsig/adaptiveservice"
	svca "github.com/godevsig/adaptiveservice/examples/msgtracing/service_a"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.Parse()

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	c := as.NewClient(opts...)
	conn := <-c.Discover("example", "serviceA")
	if conn == nil {
		fmt.Println(as.ErrServiceNotFound("example", "serviceA"))
		return
	}
	defer conn.Close()

	// tracing only one session
	token, err := as.TraceMsgByType((*svca.Request)(nil))
	if err != nil {
		fmt.Println(err)
	}

	req := svca.Request{Input: "Ni Hao"}
	var rep svca.Reply
	if err := conn.SendRecv(&req, &rep); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(rep.Output)

	// wait tracing service to handle the records
	time.Sleep(time.Second)
	// traced message records will be read cleared
	msgs, err := as.ReadTracedMsg(token)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println()
	fmt.Println("tracing records with token:", token)
	fmt.Println(msgs)
}
