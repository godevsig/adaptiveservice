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
	clientCmd := flag.String("cmd", "", "client cmd in client mode")
	flag.Parse()

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	if serverMode {
		server.Run(opts)
	} else if clientMode {
		client.Run(*clientCmd, opts)
	} else {
		opts = append(opts, as.WithScope(as.ScopeProcess))
		go client.Run(*clientCmd, opts)
		done := make(chan struct{})
		go func() {
			server.Run(opts)
			close(done)
		}()
		<-done
	}
}
