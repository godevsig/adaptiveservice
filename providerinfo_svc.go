package adaptiveservice

// SrvProviderInfo : service providerInfo
const SrvProviderInfo = "providerInfo"

// ReqProviderInfo gets self provider ID, reply with string.
type ReqProviderInfo struct{}

// Handle handles ReqProviderInfo.
func (msg *ReqProviderInfo) Handle(stream ContextStream) (reply interface{}) {
	return sharedInfo.providerID
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

var cachedSelfProviderID string

// GetSelfProviderID gets self provider ID and cache it for further use
func GetSelfProviderID() (id string, err error) {
	if len(cachedSelfProviderID) != 0 {
		return cachedSelfProviderID, nil
	}
	c := NewClient(WithScope(ScopeProcess | ScopeOS)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, SrvProviderInfo)
	if conn == nil {
		return "", ErrServiceNotFound(BuiltinPublisher, SrvProviderInfo)
	}
	defer conn.Close()
	err = conn.SendRecv(&ReqProviderInfo{}, &id)
	if err == nil {
		cachedSelfProviderID = id
	}
	return
}

func init() {
	RegisterType((*ReqProviderInfo)(nil))
}
