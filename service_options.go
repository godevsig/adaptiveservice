package adaptiveservice

// ServiceOption is option for service.
type ServiceOption func(*service)

// OnNewStreamFunc sets a function which is called to initialize
// the context when new incoming stream is accepted.
func OnNewStreamFunc(fn func(Context)) ServiceOption {
	return func(svc *service) {
		svc.fnOnNewStream = fn
	}
}

// OnConnectFunc sets a function which is called when new
// incoming connection is established.
// The following message dispaching on this connection will stop
// if fn returns true, leaving the connection NOT closed.
func OnConnectFunc(fn func(Netconn) bool) ServiceOption {
	return func(svc *service) {
		svc.fnOnConnect = fn
	}
}

// OnDisconnectFunc sets a function which is called when the connection
// was disconnected.
func OnDisconnectFunc(fn func(Netconn)) ServiceOption {
	return func(svc *service) {
		svc.fnOnDisconnect = fn
	}
}
