package main

import (
	"flag"
	"os"

	as "github.com/godevsig/adaptiveservice"
	"github.com/godevsig/adaptiveservice/examples/echo/client"
	"github.com/godevsig/adaptiveservice/examples/echo/server"
)

func main() {
	var debug bool
	var serverMode bool
	var clientMode bool
	var registryAddr string
	var lanBroadcastPort string
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.BoolVar(&serverMode, "s", false, "server mode")
	flag.BoolVar(&clientMode, "c", false, "client mode")
	flag.StringVar(&registryAddr, "registry", "10.182.105.138:11985", "registry addr")
	flag.StringVar(&lanBroadcastPort, "bcast", "9923", "broadcast port for LAN")
	flag.Parse()

	os.Setenv("registryAddr", registryAddr)
	os.Setenv("lanBroadcastPort", lanBroadcastPort)

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
