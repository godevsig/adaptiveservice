package adaptiveservice

import "strings"

// SetProviderID sets the provider ID for this server.
// Provider ID identifies the service in the network where there are
// multiple publisher_service instances found in the registry.
// Provider ID is usually shared by servers in the same network node.
func (s *Server) SetProviderID(id string) *Server {
	s.providerID = id
	return s
}

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
//  qWeight: smaller qWeight results higher message handling concurrency.
//  qScale: the maximum goroutines to handle message per CPU core.
// You should know what you are doing with these parameters.
func (s *Server) SetScaleFactor(qWeight, qScale int) *Server {
	s.qWeight = qWeight
	s.qScale = qScale
	return s
}

// DisableMsgTypeCheck disables message type checking for incoming messages.
func (s *Server) DisableMsgTypeCheck() *Server {
	s.msgTypeCheck = false
	return s
}

// SetBroadcastPort sets the broadcast port used by lan registry.
func (s *Server) SetBroadcastPort(port string) *Server {
	s.bcastPort = port
	return s
}

// EnableRootRegistry makes the server become root registry.
func (s *Server) EnableRootRegistry(port string) *Server {
	s.rootRegistryPort = port
	return s
}

// EnableReverseProxy enables reverse proxy.
func (s *Server) EnableReverseProxy() *Server {
	s.reverseProxy = true
	return s
}

// EnableServiceLister enables lister service which can list
// available services.
func (s *Server) EnableServiceLister() *Server {
	s.serviceLister = true
	return s
}
