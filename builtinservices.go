package adaptiveservice

import (
	"io"
	"net"
	"strings"
)

const (
	// BuiltinPublisher name
	BuiltinPublisher = "builtin"
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
	return s.publish(ScopeProcess|ScopeOS, BuiltinPublisher, "registryInfo", knownMsgs)
}

// reply with string
type reqRegistryInfo struct{}

func (msg *reqRegistryInfo) Handle(stream ContextStream) (reply interface{}) {
	return sharedInfo.registryAddr
}

func discoverRegistryAddr() (addr string, err error) {
	c := NewClient(WithScope(ScopeProcess | ScopeOS)).SetDiscoverTimeout(0)
	err = ErrServiceNotFound
	conn := <-c.Discover(BuiltinPublisher, "registryInfo")
	if conn != nil {
		defer conn.Close()
		err = conn.SendRecv(&reqRegistryInfo{}, &addr)
	}
	return
}

// publishProviderInfoService declares the provider info service,
// from which user can get provider ID.
func (s *Server) publishProviderInfoService() error {
	if len(s.providerID) == 0 {
		panic("provider ID not specified")
	}
	sharedInfo.providerID = s.providerID
	knownMsgs := []KnownMessage{(*reqProviderInfo)(nil)}
	return s.publish(ScopeProcess|ScopeOS, BuiltinPublisher, "providerInfo", knownMsgs)
}

// reply with string
type reqProviderInfo struct{}

func (msg *reqProviderInfo) Handle(stream ContextStream) (reply interface{}) {
	return sharedInfo.providerID
}

func discoverProviderID() (id string, err error) {
	c := NewClient(WithScope(ScopeProcess | ScopeOS)).SetDiscoverTimeout(0)
	err = ErrServiceNotFound
	conn := <-c.Discover(BuiltinPublisher, "providerInfo")
	if conn != nil {
		defer conn.Close()
		err = conn.SendRecv(&reqProviderInfo{}, &id)
	}
	return
}

// publishLANRegistryService declares the LAN registry service,
// which provides service publishing and discovery service in LAN network.
func (s *Server) publishLANRegistryService() error {
	if len(s.bcastPort) == 0 {
		panic("listening port not specified")
	}

	registry, err := s.newLANRegistry()
	if err != nil {
		return err
	}
	s.addCloser(registry)

	knownMsgs := []KnownMessage{(*queryServiceInLAN)(nil), (*registerServiceForLAN)(nil)}
	return s.publish(ScopeProcess|ScopeOS, BuiltinPublisher, "LANRegistry",
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
type registerServiceForLAN struct {
	publisher string
	service   string
	port      string
}

func (msg *registerServiceForLAN) Handle(stream ContextStream) (reply interface{}) {
	registry := stream.GetContext().(*registryLAN)
	registry.registerServiceForLAN(msg.publisher, msg.service, msg.port)
	return OK
}

// publishReverseProxyService declares the reverse proxy service.
func (s *Server) publishReverseProxyService(scope Scope) error {
	knownMsgs := []KnownMessage{(*proxyRegServiceInWAN)(nil)}
	return s.publish(scope, BuiltinPublisher, "reverseProxy",
		knownMsgs,
		OnNewStreamFunc(func(ctx Context) {
			ctx.SetContext(s)
		}))
}

type proxyRegServiceInWAN regServiceInWAN

func (msg *proxyRegServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	s := stream.GetContext().(*Server)
	chanServerConn := make(chan net.Conn)

	onNewServerConnection := func(netconn net.Conn) bool {
		chanServerConn <- netconn
		return true
	}

	reversesvc := &service{
		s:                 s,
		fnOnNewConnection: onNewServerConnection,
	}
	reversetran, err := reversesvc.newTCPTransport("")
	if err != nil {
		return err
	}
	s.addCloser(reversetran)
	_, port, _ := net.SplitHostPort(reversetran.lnr.Addr().String()) // from [::]:43807

	onNewClientConnection := func(clientConn net.Conn) bool {
		if err := stream.Send(port); err != nil {
			clientConn.Close()
			return true
		}
		serverConn := <-chanServerConn
		go io.Copy(serverConn, clientConn)
		go io.Copy(clientConn, serverConn)
		return true
	}

	proxysvc := &service{
		publisherName:     msg.publisher,
		serviceName:       msg.service,
		providerID:        msg.providerID,
		s:                 s,
		scope:             ScopeWAN,
		fnOnNewConnection: onNewClientConnection,
	}

	proxytran, err := proxysvc.newTCPTransport("")
	if err != nil {
		return err
	}
	s.addCloser(proxytran)

	return OK
}

// ListService lists all services matching name which can
// be wildcard:
//   "*" matches all
//  "*bar*" matches bar, foobar, or foobarabc
//  "foo*abc*" matches foobarabc, foobarabc123, or fooabc
// "abc.org_echo.v1.0" matches exactly the service whose publisher
// is "abc.org" and service is "echo.v1.0"
//
// Only at most one "_" is allowed.
// The reply is []*ServiceInfo
type ListService struct {
	name string
}

// Handle handles ListService message.
func (msg *ListService) Handle(stream ContextStream) (reply interface{}) {
	var serviceInfos []*ServiceInfo

	publisher, service := msg.name, msg.name
	if strings.Contains(msg.name, "_") {
		strs := strings.Split(msg.name, "_")
		publisher, service = strs[0], strs[1]
	}

	serviceInfos = append(serviceInfos, queryServiceProcess(publisher, service)...)
	serviceInfos = append(serviceInfos, queryServiceOS(publisher, service)...)
	serviceInfos = append(serviceInfos, queryServiceLAN(publisher, service)...)
	serviceInfos = append(serviceInfos, queryServiceWAN(publisher, service)...)
	return serviceInfos
}

// publishServiceListerService declares the lister service.
func (s *Server) publishServiceListerService(scope Scope) error {
	knownMsgs := []KnownMessage{(*ListService)(nil)}
	return s.publish(scope, BuiltinPublisher, "serviceLister", knownMsgs)
}

func init() {
	RegisterType((*reqRegistryInfo)(nil))
	RegisterType((*reqProviderInfo)(nil))
	RegisterType((*queryServiceInLAN)(nil))
	RegisterType((*registerServiceForLAN)(nil))
	RegisterType((*proxyRegServiceInWAN)(nil))
	RegisterType((*ListService)(nil))
}
