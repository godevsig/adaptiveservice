package server

import (
	as "github.com/godevsig/adaptiveservice"
)

// Run runs the server.
func Run(lg as.Logger) {
	var opts []as.Option
	opts = append(opts, as.WithLogger(lg))

	s := as.NewServer(opts...).SetPublisher("example.org")

	knownMsg := []as.KnownMessage{(*MessageRequest)(nil)}
	s.Publish("echo.v1.0", knownMsg)
	s.Serve()
}
