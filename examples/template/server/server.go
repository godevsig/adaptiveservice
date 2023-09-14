package main

import (
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	"github.com/godevsig/adaptiveservice/examples/template"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.Parse()

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	err := template.RunService(opts)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("done")
}
