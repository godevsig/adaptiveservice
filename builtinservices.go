package adaptiveservice

import (
	"io"
	"net"
)

const (
	// BuiltinPublisher name
	BuiltinPublisher = "builtin"
	// SrvRegistryInfo : service registryInfo
	SrvRegistryInfo = "registryInfo"
	// SrvProviderInfo : service providerInfo
	SrvProviderInfo = "providerInfo"
	// SrvLANRegistry : service LANRegistry
	SrvLANRegistry = "LANRegistry"
	// SrvReverseProxy : service reverseProxy
	SrvReverseProxy = "reverseProxy"
	// SrvServiceLister : service serviceLister
	SrvServiceLister = "serviceLister"
	// SrvIPObserver : service IPObserver
	SrvIPObserver = "IPObserver"
)

var sharedInfo struct {
	registryAddr string
	providerID   string
}

// publishRegistryInfoService declares the registry info service,
// from which user can get registry address.
func (s *Server) publishRegistryInfoService() error {
	if len(s.registryAddr) == 0 {
		panic("registry address not specified")
	}
	sharedInfo.registryAddr = s.registryAddr
	knownMsgs := []KnownMessage{(*reqRegistryInfo)(nil)}
	return s.publish(ScopeProcess|ScopeOS, BuiltinPublisher, SrvRegistryInfo, knownMsgs)
}

// reply with string
type reqRegistryInfo struct{}

func (msg *reqRegistryInfo) Handle(stream ContextStream) (reply interface{}) {
	return sharedInfo.registryAddr
}

func discoverRegistryAddr(lg Logger) (addr string, err error) {
	c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(lg)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, SrvRegistryInfo)
	if conn == nil {
		return "", ErrServiceNotFound(BuiltinPublisher, SrvRegistryInfo)
	}
	defer conn.Close()
	err = conn.SendRecv(&reqRegistryInfo{}, &addr)
	return
}

// publishProviderInfoService declares the provider info service,
// from which user can get provider ID.
func (s *Server) publishProviderInfoService() error {
	if len(s.providerID) == 0 {
		panic("provider ID not specified")
	}
	sharedInfo.providerID = s.providerID
	knownMsgs := []KnownMessage{(*ReqProviderInfo)(nil)}
	return s.publish(ScopeProcess|ScopeOS, BuiltinPublisher, SrvProviderInfo, knownMsgs)
}

// ReqProviderInfo gets self provider ID, reply with string.
type ReqProviderInfo struct{}

// Handle handles ReqProviderInfo.
func (msg *ReqProviderInfo) Handle(stream ContextStream) (reply interface{}) {
	return sharedInfo.providerID
}

func discoverProviderID(lg Logger) (id string, err error) {
	c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(lg)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, SrvProviderInfo)
	if conn == nil {
		return "", ErrServiceNotFound(BuiltinPublisher, SrvProviderInfo)
	}
	defer conn.Close()
	err = conn.SendRecv(&ReqProviderInfo{}, &id)
	return
}

// publishLANRegistryService declares the LAN registry service,
// which provides service publishing and discovery service in LAN network.
func (s *Server) publishLANRegistryService() error {
	registry, err := s.newLANRegistry()
	if err != nil {
		return err
	}
	s.addCloser(registry)

	knownMsgs := []KnownMessage{(*queryServiceInLAN)(nil), (*regServiceInLAN)(nil), (*delServiceInLAN)(nil)}
	return s.publish(ScopeProcess|ScopeOS, BuiltinPublisher, SrvLANRegistry,
		knownMsgs,
		OnNewStreamFunc(func(ctx Context) {
			ctx.SetContext(registry)
		}))
}

// reply with []*ServiceInfo
type queryServiceInLAN struct {
	publisher string
	service   string
}

func (msg *queryServiceInLAN) Handle(stream ContextStream) (reply interface{}) {
	registry := stream.GetContext().(*registryLAN)
	return registry.queryServiceInLAN(msg.publisher, msg.service)
}

// reply OK on success or else reply err
type regServiceInLAN struct {
	publisher string
	service   string
	port      string
}

func (msg *regServiceInLAN) Handle(stream ContextStream) (reply interface{}) {
	registry := stream.GetContext().(*registryLAN)
	registry.registerServiceInLAN(msg.publisher, msg.service, msg.port)
	return OK
}

// no reply
type delServiceInLAN struct {
	publisher string
	service   string
}

func (msg *delServiceInLAN) Handle(stream ContextStream) (reply interface{}) {
	registry := stream.GetContext().(*registryLAN)
	registry.deleteServiceInLAN(msg.publisher, msg.service)
	return nil
}

// publishReverseProxyService declares the reverse proxy service.
func (s *Server) publishReverseProxyService(scope Scope) error {
	knownMsgs := []KnownMessage{(*proxyRegServiceInWAN)(nil)}
	return s.publish(scope, BuiltinPublisher, SrvReverseProxy,
		knownMsgs,
		OnNewStreamFunc(func(ctx Context) {
			ctx.SetContext(s)
		}))
}

type proxyRegServiceInWAN struct {
	publisher  string
	service    string
	providerID string
}

func (msg *proxyRegServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	s := stream.GetContext().(*Server)
	chanServerConn := make(chan net.Conn)

	onServerConnection := func(netconn Netconn) bool {
		chanServerConn <- netconn.(net.Conn)
		return true
	}

	reversesvc := &service{
		s:           s,
		fnOnConnect: onServerConnection,
	}
	reversetran, err := reversesvc.newTCPTransport("")
	if err != nil {
		return err
	}
	s.addCloser(reversetran)
	_, port, _ := net.SplitHostPort(reversetran.lnr.Addr().String()) // from [::]:43807

	var proxytran *streamTransport
	go func() {
		if err := stream.Recv(nil); err != nil {
			s.lg.Debugf("service cmdconn read lost, closing its proxy")
			reversetran.close()
			close(chanServerConn)
			if proxytran != nil {
				proxytran.close()
			}
		}
	}()

	onClientConnection := func(netconn Netconn) bool {
		clientConn := netconn.(net.Conn)
		s.lg.Debugf("reverse proxy: starting for client: %s", clientConn.RemoteAddr().String())
		if err := stream.Send(port); err != nil {
			s.lg.Debugf("service cmdconn write lost, closing its proxy")
			reversetran.close()
			proxytran.close()
			clientConn.Close()
			return true
		}
		serverConn := <-chanServerConn
		if serverConn == nil {
			clientConn.Close()
			return true
		}
		go func() {
			io.Copy(serverConn, clientConn)
			serverConn.Close()
			s.lg.Debugf("io copy client => server done")
		}()
		go func() {
			// acknowledge client real server connected
			clientConn.Write([]byte{0})
			io.Copy(clientConn, serverConn)
			clientConn.Close()
			s.lg.Debugf("io copy server => client done")
		}()
		return true
	}

	proxysvc := &service{
		publisherName: msg.publisher,
		serviceName:   msg.service,
		providerID:    msg.providerID,
		s:             s,
		scope:         ScopeWAN,
		fnOnConnect:   onClientConnection,
	}

	proxytran, err = proxysvc.newTCPTransport("")
	if err != nil {
		return err
	}
	s.addCloser(proxytran)

	return OK
}

// ListService lists all services in specified scopes matching
// publisher/service name which can be wildcard:
//   "*" matches all
//  "*bar*" matches bar, foobar, or foobarabc
//  "foo*abc*" matches foobarabc, foobarabc123, or fooabc
// The reply is [4][]*ServiceInfo
type ListService struct {
	TargetScope Scope
	Publisher   string
	Service     string
}

// Handle handles ListService message.
func (msg *ListService) Handle(stream ContextStream) (reply interface{}) {
	s := stream.GetContext().(*Server)
	var scopes [4][]*ServiceInfo

	if msg.TargetScope&s.scope&ScopeProcess == ScopeProcess {
		scopes[0] = queryServiceProcess(msg.Publisher, msg.Service)
	}
	if msg.TargetScope&s.scope&ScopeOS == ScopeOS {
		scopes[1] = queryServiceOS(msg.Publisher, msg.Service)
	}
	if msg.TargetScope&s.scope&ScopeLAN == ScopeLAN {
		scopes[2] = queryServiceLAN(msg.Publisher, msg.Service, s.lg)
	}
	if msg.TargetScope&s.scope&ScopeWAN == ScopeWAN {
		scopes[3] = queryServiceWAN(s.registryAddr, msg.Publisher, msg.Service, s.lg)
	}
	return scopes
}

// publishServiceListerService declares the lister service.
func (s *Server) publishServiceListerService(scope Scope) error {
	knownMsgs := []KnownMessage{(*ListService)(nil)}
	return s.publish(scope, BuiltinPublisher, SrvServiceLister,
		knownMsgs,
		OnNewStreamFunc(func(ctx Context) {
			ctx.SetContext(s)
		}))
}

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
	RegisterType((*reqRegistryInfo)(nil))
	RegisterType((*ReqProviderInfo)(nil))
	RegisterType((*queryServiceInLAN)(nil))
	RegisterType((*regServiceInLAN)(nil))
	RegisterType((*delServiceInLAN)(nil))
	RegisterType((*proxyRegServiceInWAN)(nil))
	RegisterType((*ListService)(nil))
	RegisterType([4][]*ServiceInfo{})
	RegisterType(GetObservedIP{})
}
