package adaptiveservice

type conf struct {
	lg           Logger
	registryAddr string
	providerID   string
	scope        Scope
	qsize        int
}

func newConf() *conf {
	return &conf{
		lg:    LoggerNull{},
		scope: ScopeAll,
		qsize: 128,
	}
}

// Option is option to be set.
type Option func(*conf)

// WithLogger sets the logger.
func WithLogger(lg Logger) Option {
	return func(c *conf) {
		c.lg = lg
	}
}

// WithScope sets the publishing or discovering scope, which is
// an ORed value of ScopeProcess, ScopeOS, ScopeLAN or ScopeWAN.
// Default is ScopeAll.
func WithScope(scope Scope) Option {
	return func(c *conf) {
		c.scope = scope
	}
}

// WithProviderID sets the provider ID which is used to identify service
// in the network where there are multiple publisher_service instances
// found in the registry.
// Provider ID is usually shared by servers in the same network node.
func WithProviderID(id string) Option {
	return func(c *conf) {
		c.providerID = id
	}
}

// WithRegistryAddr sets the registry address in format ip:port.
func WithRegistryAddr(addr string) Option {
	return func(c *conf) {
		c.registryAddr = addr
	}
}

// WithQsize sets the transport layer message queue size.
func WithQsize(size int) Option {
	return func(c *conf) {
		c.qsize = size
	}
}
