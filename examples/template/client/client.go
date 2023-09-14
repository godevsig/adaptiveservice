package main

import (
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	"github.com/godevsig/adaptiveservice/examples/template/template"
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
	conn := <-c.Discover("example", "template")
	if conn == nil {
		fmt.Println(as.ErrServiceNotFound("example", "template"))
		return
	}
	defer conn.Close()

	req := template.Request{Input: "Ni Hao"}
	var rep template.Reply
	if err := conn.SendRecv(&req, &rep); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(rep.Output)
}
