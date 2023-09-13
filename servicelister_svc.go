package adaptiveservice

// SrvServiceLister : service serviceLister
const SrvServiceLister = "serviceLister"

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

func init() {
	RegisterType((*ListService)(nil))
	RegisterType([4][]*ServiceInfo{})
}
