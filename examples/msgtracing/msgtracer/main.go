package main

import (
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.Parse()

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	s := as.NewServer(opts...).EnableMessageTracer()

	// ctrl+c to exit
	if err := s.Serve(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("done")
}
