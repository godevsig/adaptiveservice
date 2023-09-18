package main

import (
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	svca "github.com/godevsig/adaptiveservice/examples/msgtracing/service_a"
	svcb "github.com/godevsig/adaptiveservice/examples/msgtracing/service_b"
	svcc "github.com/godevsig/adaptiveservice/examples/msgtracing/service_c"
	svcd "github.com/godevsig/adaptiveservice/examples/msgtracing/service_d"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.Parse()

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	go func() {
		err := svca.RunService(opts)
		if err != nil {
			fmt.Println(err)
		}
	}()

	go func() {
		err := svcb.RunService(opts)
		if err != nil {
			fmt.Println(err)
		}
	}()

	go func() {
		err := svcc.RunService(opts)
		if err != nil {
			fmt.Println(err)
		}
	}()

	err := svcd.RunService(opts)
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("done")
}
