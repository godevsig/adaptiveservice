package adaptiveservice

import (
	"context"
	"sync"
)

type conf struct {
	ctx          context.Context
	lg           Logger
	registryAddr string
	scope        Scope
	once         sync.Once
	qsize        int
}

func newConf() *conf {
	return &conf{
		ctx:   context.Background(),
		lg:    null{},
		scope: ScopeAll,
		qsize: 128,
	}
}

// Option is option to be set.
type Option func(*conf)

// WithContext sets the cancelable context.
func WithContext(ctx context.Context) Option {
	return func(c *conf) {
		c.ctx = ctx
	}
}

// WithLogger sets the logger.
func WithLogger(lg Logger) Option {
	return func(c *conf) {
		c.lg = lg
	}
}

// WithScope sets the publishing and discovering scope.
func WithScope(scope Scope) Option {
	return func(c *conf) {
		c.scope = scope
	}
}

// WithRegistryAddr sets the registry address in format ip:port.
func WithRegistryAddr(addr string) Option {
	return func(c *conf) {
		c.registryAddr = addr
	}
}

// WithQsize sets the internal message queue size.
func WithQsize(size int) Option {
	return func(c *conf) {
		c.qsize = size
	}
}
