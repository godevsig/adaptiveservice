package adaptiveservice

// SrvRegistryInfo : service registryInfo
const SrvRegistryInfo = "registryInfo"

var sharedInfo struct {
	registryAddr string
	providerID   string
}

// reply with string
type reqRegistryInfo struct{}

func (msg *reqRegistryInfo) Handle(stream ContextStream) (reply interface{}) {
	return sharedInfo.registryAddr
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

func init() {
	RegisterType((*reqRegistryInfo)(nil))
}
