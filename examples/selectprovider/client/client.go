package main

import (
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	"github.com/godevsig/adaptiveservice/examples/selectprovider/selectme"
)

func main() {
	debug := flag.Bool("d", false, "enable debug")
	selectionMethod1 := flag.Int("m1", 0, "provider selection method 1")
	selectionMethod2 := flag.Int("m2", 0, "provider selection method 2")
	num := flag.Int("n", 1, "message count")
	flag.Parse()

	var opts []as.Option
	if *debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	c := as.NewClient(opts...)
	c.SetProviderSelectionMethod(as.ProviderSelectionMethod(*selectionMethod1))
	c.SetProviderSelectionMethod(as.ProviderSelectionMethod(*selectionMethod2))

	conn := <-c.Discover("example", "selectme*")
	if conn == nil {
		fmt.Println(as.ErrServiceNotFound("example", "selectme"))
		return
	}
	defer conn.Close()

	req := selectme.Request{Input: "Zai Ma"}
	var rep selectme.Reply
	for i := 0; i < *num; i++ {
		if err := conn.SendRecv(&req, &rep); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(rep.Output)
	}
	fmt.Println("done")
}
