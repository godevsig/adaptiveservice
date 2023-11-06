package adaptiveservice

// SetPublisher declares the publisher of the server, which
// is usually an organization name.
// The default value of publisherName is "default.org".
//
// SetPublisher panics if publisherName contains "_" or "/" or whitespace.
func (s *Server) SetPublisher(publisherName string) *Server {
	if err := checkNameConvention(publisherName); err != nil {
		panic(err)
	}
	s.publisher = publisherName
	return s
}

// SetScaleFactors sets the scale factors to be applied on the internal message queue.
//  residentWorkers:
//    > 0: the number of resident workers.
//   <= 0: uses default value. Default is 1.
//  qSizePerCore:
//    > 0: the internal message queue size per core.
//   <= 0: uses default value. Default is 128.
//  qWeight:
//    > 0: the weight of the message queue.
//      0: uses default value. Default is 16.
//    < 0: the number of workers is fixed at residentWorkers.
// A Server has one internal message queue, messages received from transport layer are put into
// the queue, a number of workers get message from the queue and handle it.
// The size of the message queue is qSizePerCore*core number.
//
// When qWeight >= 0, a worker pool is created, the number of wokers scales automatically in the pool.
// The target number of available workers is dynamically adjusted by below formula periodically:
//  target available workers = len(message queue)/qWeight + residentWorkers
// The worker pool continuously adds/removes workers to follow the target.
//
// Be careful to set qWeight < 0, which effectively disables auto scala worker pool, which
// in turn only uses fixed number of workers(residentWorkers). Forever blocking may occur in
// such case, especially when residentWorkers = 1.
func (s *Server) SetScaleFactors(residentWorkers, qSizePerCore, qWeight int) *Server {
	if residentWorkers > 0 {
		s.residentWorkers = residentWorkers
	}

	if qSizePerCore > 0 {
		s.qSizePerCore = qSizePerCore
	}

	if qWeight != 0 {
		s.qWeight = qWeight
	}

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

// EnableMessageTracer enables message tracing service which helps to
// collect local traced messages.
func (s *Server) EnableMessageTracer() *Server {
	s.msgTracer = true
	return s
}
