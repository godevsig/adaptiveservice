package adaptiveservice

import (
	"math/rand"
	"time"
)

// Client uses services.
type Client struct {
	*conf
	discoverTimeout int // in seconds
	localProviderID string
}

// NewClient creates a client which discovers services.
func NewClient(options ...Option) *Client {
	c := &Client{
		conf:            newConf(),
		discoverTimeout: -1,
	}

	for _, o := range options {
		o(c.conf)
	}

	c.lg.Debugln("new client created")
	return c
}

func (c *Client) init() {
	providerID, _ := discoverProviderID()
	if len(providerID) != 0 {
		c.localProviderID = providerID
	}

	if c.scope&ScopeWAN == ScopeWAN {
		// registryAddr not provided by user
		if len(c.registryAddr) == 0 {
			if addr, err := discoverRegistryAddr(); err != nil {
				c.lg.Warnf("registry address not found: %s", err)
			} else {
				c.registryAddr = addr
				c.lg.Infof("discovered registry address: %s", addr)
			}
		}
	}
	c.lg.Debugln("client initialized")
}

// Discover discovers the wanted service and returns the connection channel,
// from which user can get one or more connections.
// Each connection represents a connection to one of the service providers
// providing the wanted micro service.
//
// Use providerIDs to select target providers only.
// If no provider id presents, discover searches scopes by distance and returns
// only one connection towards the found service which may have been randomly selected
// if more than one services found.
// If any of the provider ids is "*", discover will return all available
// connections of the wanted service.
func (c *Client) Discover(publisher, service string, providerIDs ...string) <-chan Connection {
	c.once.Do(c.init)
	connections := make(chan Connection)

	has := func(target string) bool {
		if len(providerIDs) == 0 { // match all
			return true
		}
		if len(target) == 0 {
			return false
		}
		for _, str := range providerIDs {
			if str == target {
				return true
			}
		}
		return false
	}

	expect := 1
	if len(providerIDs) != 0 {
		expect = len(providerIDs)
		if has("*") {
			expect = -1 // all
			providerIDs = nil
		}
	}

	findWithinOS := func() int {
		if !has(c.localProviderID) {
			return 0
		}
		if c.scope&ScopeProcess == ScopeProcess {
			ct := lookupServiceChan(publisher, service)
			if ct != nil {
				connections <- ct.newConnection()
				return 1
			}
		}
		if c.scope&ScopeOS == ScopeOS {
			addr := lookupServiceUDS(publisher, service)
			if len(addr) != 0 {
				conn, err := c.newUDSConnection(addr)
				if err != nil {
					c.lg.Errorln("dial " + addr + " failed")
				} else {
					connections <- conn
					return 1
				}
			}
		}
		return 0
	}

	findNetwork := func(expect int) int {
		var addrs []string
		found := 0

		connect := func() {
			for len(addrs) != 0 && found != expect {
				i := rand.Intn(len(addrs))
				addr := addrs[i]
				addrs[i] = addrs[len(addrs)-1]
				addrs = addrs[:len(addrs)-1]

				conn, err := c.newTCPConnection(addr)
				if err != nil {
					c.lg.Warnln("dial " + addr + " failed")
				} else {
					connections <- conn
					found++
				}
			}
		}

		if found != expect && c.scope&ScopeLAN == ScopeLAN {
			addrs = lookupServiceLAN(publisher, service, providerIDs...)
			connect()
		}
		if found != expect && c.scope&ScopeWAN == ScopeWAN {
			addrs = lookupServiceWAN(publisher, service, providerIDs...)
			connect()
		}
		return found
	}

	go func() {
		defer close(connections)
		found := 0
		timeout := c.discoverTimeout
		for found == 0 && timeout != 0 {
			if found += findWithinOS(); found == expect {
				break
			}
			if found += findNetwork(expect - found); found == expect {
				break
			}
			time.Sleep(time.Second)
			timeout--
		}
	}()

	return connections
}

// Close closes the client.
func (c *Client) Close() error {
	return nil
}
