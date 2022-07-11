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

// SetScaleFactors sets the scale factors to be applied on the internal message queue.
//  residentWorkers: the number of resident workers. Default is 1.
//  qSizePerCore: the internal message queue size per core. Default is 32.
// A Server has one internal message queue, messages received from transport layer are put into
// the queue, a number of workers get message from the queue and handle it.
// The number of wokers scales automatically from residentWorkers to qSizePerCore * core number.
func (s *Server) SetScaleFactors(residentWorkers, qSizePerCore int) *Server {
	s.residentWorkers = residentWorkers
	s.qSizePerCore = qSizePerCore
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
