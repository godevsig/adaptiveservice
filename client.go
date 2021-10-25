package adaptiveservice

import (
	"math/rand"
	"strings"
	"time"
)

// Client uses services.
type Client struct {
	*conf
	discoverTimeout int // in seconds
	deepCopy        bool
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

	c.lg.Debugf("new client created")
	return c
}

// Discover discovers the wanted service and returns the connection channel,
// from which user can get one or more connections.
// Each connection represents a connection to one of the service providers
// providing the wanted micro service.
//
// Only one service(identified by publisher name and service name) can exist in
// ScopeProcess and ScopeOS, but in ScopeLAN and ScopeWAN there can be many systems
// providing the same service, each systeam(called provider) has an unique provider ID.
//
// Use providerIDs to select target providers in ScopeLAN or ScopeWAN,
// if no provider id presents, discover searches scopes by distance that is
// in the order of ScopeProcess, ScopeOS, ScopeLAN, ScopeWAN, and returns
// only one connection towards the found service which may have been randomly selected
// if more than one services were found.
//
// If any of publisher or service or provider ids contains "*", discover will return
// all currently available connections of the wanted service(s). Make sure to close
// ALL the connections it returns after use.
func (c *Client) Discover(publisher, service string, providerIDs ...string) <-chan Connection {
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
		if len(c.providerID) == 0 {
			providerID, err := discoverProviderID(c.lg)
			if err != nil {
				providerID = "NA"
			}
			c.providerID = providerID
		}
	}
	if strings.Contains(publisher+service, "*") {
		expect = -1
	}

	findWithinOS := func() (found int) {
		if !has(c.providerID) {
			return 0
		}
		if c.scope&ScopeProcess == ScopeProcess {
			c.lg.Debugf("finding %s_%s in ScopeProcess", publisher, service)
			ccts := c.lookupServiceChan(publisher, service)
			for _, cct := range ccts {
				connections <- cct.newConnection()
				c.lg.Debugf("channel transport connected")
				found++
				if found == expect {
					return
				}
			}
		}
		if c.scope&ScopeOS == ScopeOS {
			c.lg.Debugf("finding %s_%s in ScopeOS", publisher, service)
			addrs := lookupServiceUDS(publisher, service)
			for _, addr := range addrs {
				conn, err := c.newUDSConnection(addr)
				if err != nil {
					c.lg.Errorf("dial " + addr + " failed")
				} else {
					connections <- conn
					c.lg.Debugf("unix domain socket connected to: %s", addr)
					found++
					if found == expect {
						return
					}
				}
			}
		}
		return
	}

	findNetwork := func(expect int) (found int) {
		var addrs []string

		connect := func() {
			for len(addrs) != 0 && found != expect {
				i := rand.Intn(len(addrs))
				addr := addrs[i]
				addrs[i] = addrs[len(addrs)-1]
				addrs = addrs[:len(addrs)-1]

				conn, err := c.newTCPConnection(addr)
				if err != nil {
					c.lg.Warnf("dial " + addr + " failed")
				} else {
					connections <- conn
					c.lg.Debugf("tcp socket connected to: %s", addr)
					found++
				}
			}
		}

		if found != expect && c.scope&ScopeLAN == ScopeLAN {
			c.lg.Debugf("finding %s_%s in ScopeLAN", publisher, service)
			addrs = c.lookupServiceLAN(publisher, service, providerIDs...)
			connect()
		}
		if found != expect && c.scope&ScopeWAN == ScopeWAN {
			c.lg.Debugf("finding %s_%s in ScopeWAN", publisher, service)
			if len(c.registryAddr) == 0 {
				addr, err := discoverRegistryAddr(c.lg)
				if err != nil {
					addr = "NA"
				}
				c.registryAddr = addr
				c.lg.Infof("discovered registry address: %s", addr)
			}
			if c.registryAddr != "NA" {
				addrs = c.lookupServiceWAN(publisher, service, providerIDs...)
				connect()
			}
		}
		return found
	}

	go func() {
		defer close(connections)
		found := 0
		timeout := c.discoverTimeout
		for found == 0 {
			if found += findWithinOS(); found == expect {
				break
			}
			if found += findNetwork(expect - found); found == expect {
				break
			}
			if timeout == 0 {
				break
			}
			c.lg.Debugf("waiting for service: %s_%s", publisher, service)
			time.Sleep(time.Second)
			timeout--
		}
	}()

	return connections
}
