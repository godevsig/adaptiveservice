package server

import (
	"net"
	"os"

	as "github.com/godevsig/adaptiveservice"
)

// Run runs the server.
func Run(lg as.Logger) {
	var opts []as.Option
	opts = append(opts, as.WithLogger(lg))
	rrport := ""
	if ra := os.Getenv("registryAddr"); len(ra) != 0 {
		opts = append(opts, as.WithRegistryAddr(ra))
		_, rrport, _ = net.SplitHostPort(ra)
	}
	bcastport := os.Getenv("lanBroadcastPort")

	s := as.NewServer(opts...).
		SetPublisher("example.org").
		SetBroadcastPort(bcastport).
		EnableRootRegistry(rrport).
		//EnableReverseProxy().
		EnableServiceLister()

	knownMsg := []as.KnownMessage{(*MessageRequest)(nil)}
	s.Publish("echo.v1.0", knownMsg)
	s.Serve()
}
