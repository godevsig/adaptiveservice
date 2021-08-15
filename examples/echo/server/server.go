package server

import (
	"os"

	as "github.com/godevsig/adaptiveservice"
)

// Run runs the server.
func Run(lg as.Logger) {
	var opts []as.Option
	opts = append(opts, as.WithLogger(lg))
	if ra := os.Getenv("registryAddr"); len(ra) != 0 {
		opts = append(opts, as.WithRegistryAddr(ra))
	}
	bcastport := os.Getenv("lanBroadcastPort")

	s := as.NewServer(opts...).
		SetPublisher("example.org").
		SetBroadcastPort(bcastport).
		EnableRootRegistry().
		//EnableReverseProxy().
		EnableServiceLister()

	knownMsg := []as.KnownMessage{(*MessageRequest)(nil)}
	s.Publish("echo.v1.0", knownMsg)
	s.Serve()
}
