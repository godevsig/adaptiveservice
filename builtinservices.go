package adaptiveservice

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
	defer c.Close()

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
	defer c.Close()

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
		WithOnNewStreamFunc(func(ctx Context) error {
			ctx.SetContext(registry)
			return nil
		}))
}

// reply with []*serviceInfo
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

func init() {
	RegisterType((*reqRegistryInfo)(nil))
	RegisterType((*reqProviderInfo)(nil))
	RegisterType((*queryServiceInLAN)(nil))
	RegisterType((*registerServiceForLAN)(nil))
}
