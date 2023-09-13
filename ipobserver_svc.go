package adaptiveservice

import "net"

// SrvIPObserver : service IPObserver
const SrvIPObserver = "IPObserver"

// GetObservedIP returns the observed IP of the client.
// The reply is string type.
type GetObservedIP struct{}

// Handle handles GetObservedIP message.
func (msg GetObservedIP) Handle(stream ContextStream) (reply interface{}) {
	netconn := stream.GetNetconn()
	network := netconn.LocalAddr().Network()
	if network == "chan" || network == "unix" {
		return "127.0.0.1"
	}

	rhost, _, err := net.SplitHostPort(netconn.RemoteAddr().String())
	if err != nil {
		return err
	}
	return rhost
}

// publishIPObserverService declares the IP observer service.
func (s *Server) publishIPObserverService() error {
	knownMsgs := []KnownMessage{GetObservedIP{}}
	return s.publish(s.scope, BuiltinPublisher, SrvIPObserver, knownMsgs)
}

func init() {
	RegisterType(GetObservedIP{})
}
