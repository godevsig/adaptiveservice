package adaptiveservice

// SetProviderID sets the provider ID for this server.
// Provider ID identifies the service in the network where there are
// multiple publisher_service instances found in the registry.
// Provider ID is usually shared by servers in the same network node.
func (s *Server) SetProviderID(id string) *Server {
	s.providerID = id
	return s
}

// SetPublisher declares the publisher of the server, which for example
// is usually an organization name.
// Default is "default.org"
func (s *Server) SetPublisher(publisher string) *Server {
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
