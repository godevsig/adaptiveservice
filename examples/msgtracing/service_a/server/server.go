package main

import (
	"flag"
	"fmt"

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

	err := svca.RunService(opts)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("done")
}
