package adaptiveservice

import "net"

// ServiceOption is option for service.
type ServiceOption func(*service)

// OnNewStreamFunc sets a function which is called to initialize
// the context when new incoming stream is accepted.
func OnNewStreamFunc(fn func(Context)) ServiceOption {
	return func(svc *service) {
		svc.fnOnNewStream = fn
	}
}

// OnNewConnectionFunc sets a function which is called when new
// incoming stream connection is established.
// The following message dispaching on this connection will stop
// if fn returns true, leaving the connection NOT closed.
// Only works for stream transport.
func OnNewConnectionFunc(fn func(net.Conn) bool) ServiceOption {
	return func(svc *service) {
		svc.fnOnNewConnection = fn
	}
}
