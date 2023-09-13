package adaptiveservice

// SrvLANRegistry : service LANRegistry
const SrvLANRegistry = "LANRegistry"

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

func init() {
	RegisterType((*queryServiceInLAN)(nil))
	RegisterType((*regServiceInLAN)(nil))
	RegisterType((*delServiceInLAN)(nil))
}
