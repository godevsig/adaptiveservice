package main

import (
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	svcb "github.com/godevsig/adaptiveservice/examples/msgtracing/service_b"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.Parse()

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	err := svcb.RunService(opts)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("done")
}
