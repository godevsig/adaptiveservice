package main

import (
	"flag"

	as "github.com/godevsig/adaptiveservice"
	"github.com/godevsig/adaptiveservice/examples/echo/client"
	"github.com/godevsig/adaptiveservice/examples/echo/server"
)

func main() {
	var debug bool
	var serverMode bool
	var clientMode bool
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.BoolVar(&serverMode, "s", false, "server mode")
	flag.BoolVar(&clientMode, "c", false, "client mode")
	flag.Parse()

	var lg as.Logger = as.LoggerNull{}
	if debug {
		lg = as.LoggerAll{}
	}

	if serverMode {
		server.Run(lg)
	} else if clientMode {
		client.Run(lg)
	} else {
		go server.Run(lg)
		go client.Run(lg)
		select {}
	}
}
