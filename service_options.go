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

// OnStreamCloseFunc sets a function which is called when the stream
// is being closed.
func OnStreamCloseFunc(fn func(Context)) ServiceOption {
	return func(svc *service) {
		svc.fnOnStreamClose = fn
	}
}

// OnConnectFunc sets a function which is called when new
// incoming connection is established.
// Further message dispaching on this connection will stop
// if fn returns true, leaving the connection NOT closed, fn
// should then take over this Netconn and close it when finished.
func OnConnectFunc(fn func(Netconn) (takeOver bool)) ServiceOption {
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

// UseNamedUDS sets the service to use named Unix Domain Socket for Scope OS.
// The socket path will be
// /tmp/adaptiveservice/sockets/$providerID/$publisher_$service.sock.
// Default is using anonymous UDS.
func UseNamedUDS() ServiceOption {
	return func(svc *service) {
		svc.useNamedUDS = true
	}
}
