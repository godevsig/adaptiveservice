package adaptiveservice

import "strings"

// SetPublisher declares the publisher of the server, which for example
// is usually an organization name. The name should not contain "_" or "/".
// Default is "default.org".
func (s *Server) SetPublisher(publisher string) *Server {
	if strings.ContainsAny(publisher, "_/") {
		panic("publisher should not contain _ or /")
	}
	s.publisher = publisher
	return s
}

// SetScaleFactor sets the scale factors:
// The size of the message queue is qWeight * qScale * CPU num.
// (n-1)*qWeight+1 .. n*qWeight messages in the queue ==> n workers, where
// n >=1 && n <= qScale * CPU num
//  qWeight: how many messages a worker can handle before a new worker is created.
//  qScale: the maximum goroutines to handle message per CPU core.
// You should know what you are doing with these parameters.
func (s *Server) SetScaleFactor(qWeight, qScale int) *Server {
	s.qWeight = qWeight
	s.qScale = qScale
	return s
}

// SetBroadcastPort sets the broadcast port used by lan registry.
func (s *Server) SetBroadcastPort(port string) *Server {
	s.broadcastPort = port
	return s
}

// DisableMsgTypeCheck disables message type checking for incoming messages.
func (s *Server) DisableMsgTypeCheck() *Server {
	s.msgTypeCheck = false
	return s
}

// EnableRootRegistry makes the server become root registry.
func (s *Server) EnableRootRegistry() *Server {
	s.rootRegistry = true
	return s
}

// EnableAutoReverseProxy tries to enable reverse proxy.
func (s *Server) EnableAutoReverseProxy() *Server {
	s.autoReverseProxy = true
	return s
}

// EnableServiceLister enables lister service which can list
// available services.
func (s *Server) EnableServiceLister() *Server {
	s.serviceLister = true
	return s
}

// EnableIPObserver enables IP observer service which helps the
// requesting client to find out its observed IP address.
func (s *Server) EnableIPObserver() *Server {
	s.ipObserver = true
	return s
}
