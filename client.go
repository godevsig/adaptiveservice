package adaptiveservice

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client uses services.
type Client struct {
	*conf
	providerSelectionMethods []ProviderSelectionMethod
	discoverTimeout          int // in seconds
	checkIntervalMS          int // in milliseconds
	deepCopy                 bool
}

// NewClient creates a client which discovers services.
func NewClient(options ...Option) *Client {
	c := &Client{
		conf:            newConf(),
		discoverTimeout: -1,
		checkIntervalMS: 1000,
	}

	for _, o := range options {
		o(c.conf)
	}

	mTraceHelper.init(c.lg)
	c.lg.Debugf("new client created")
	return c
}

type providerScoreInfo struct {
	addr    string
	mqi     *MsgQInfo
	score   float32
	latency time.Duration
}

func (pi *providerScoreInfo) String() string {
	return fmt.Sprintf("providerScoreInfo:{addr: %v msgQinfo: %+v score: %v latency: %v}",
		pi.addr, *pi.mqi, pi.score, pi.latency)
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
		// str can be wildcard
		for _, str := range providerIDs {
			if wildcardMatch(str, target) {
				return true
			}
		}
		return false
	}

	expect := 1
	if strings.Contains(publisher+service, "*") {
		expect = -1
	}
	if len(providerIDs) != 0 {
		expect = len(providerIDs)
		if strings.Contains(strings.Join(providerIDs, " "), "*") {
			expect = -1
		}
		if len(c.providerID) == 0 {
			providerID, err := discoverProviderID(c.lg)
			if err != nil {
				providerID = "NA"
			}
			c.providerID = providerID
		}
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
		connect := func(addrs []string) {
			for i := 0; i < len(addrs) && found != expect; i++ {
				addr := addrs[i]
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

		selectAndConnect := func(addrs []string) {
			rand.Shuffle(len(addrs), func(i, j int) { addrs[i], addrs[j] = addrs[j], addrs[i] })
			if len(c.providerSelectionMethods) != 0 {
				providerScoreInfoChan := make(chan *providerScoreInfo, len(addrs))
				var wg sync.WaitGroup
				for _, addr := range addrs {
					wg.Add(1)
					go func(addr string) {
						defer wg.Done()
						conn, err := c.newTCPConnection(addr)
						if err != nil {
							c.lg.Warnf("dial " + addr + " failed")
							return
						}
						defer conn.Close()
						conn.SetRecvTimeout(3 * time.Second)

						var mqi MsgQInfo
						pinfo := providerScoreInfo{
							addr: addr,
							mqi:  &mqi,
						}
						ts := time.Now()
						for i := 0; i < 4; i++ {
							err := conn.SendRecv(QueryMsgQInfo{}, &mqi)
							if err != nil {
								c.lg.Warnf("%v", err)
								return
							}
						}
						pinfo.latency = time.Now().Sub(ts) / 4
						if mqi.QueueWeight > 0 {
							pinfo.score = float32(mqi.NumCPU) / float32(mqi.QueueLen/mqi.QueueWeight+mqi.ResidentWorkers+mqi.BusyWorkerNum)
						} else {
							workableCPU := mqi.ResidentWorkers
							if workableCPU > mqi.NumCPU {
								workableCPU = mqi.NumCPU
							}
							pinfo.score = 0 - float32(mqi.IdleWorkerNum+workableCPU)/float32(mqi.IdleWorkerNum*workableCPU)
						}
						providerScoreInfoChan <- &pinfo
					}(addr)
				}
				wg.Wait()
				close(providerScoreInfoChan)
				c.lg.Debugf("provider msgQ info scaned")
				providerScoreInfos := make([]*providerScoreInfo, 0, len(providerScoreInfoChan))
				for pinfo := range providerScoreInfoChan {
					providerScoreInfos = append(providerScoreInfos, pinfo)
				}

				sorted := false
				for _, method := range c.providerSelectionMethods {
					switch method {
					case Capacity:
						c.lg.Debugf("sort %v by Capacity", providerScoreInfos)
						providerScoreInfos := providerScoreInfos
						if sorted {
							providerScoreInfos = providerScoreInfos[:len(providerScoreInfos)/2]
						}
						sort.SliceStable(providerScoreInfos, func(i, j int) bool {
							return providerScoreInfos[i].score > providerScoreInfos[j].score
						})
						sorted = true
					case Latency:
						c.lg.Debugf("sort %v by Latency", providerScoreInfos)
						providerScoreInfos := providerScoreInfos
						if sorted {
							providerScoreInfos = providerScoreInfos[:len(providerScoreInfos)/2]
						}
						sort.SliceStable(providerScoreInfos, func(i, j int) bool {
							return providerScoreInfos[i].latency < providerScoreInfos[j].latency
						})
						sorted = true
					}
				}
				c.lg.Debugf("sorted providerScoreInfos: %v", providerScoreInfos)
				addrs = make([]string, 0, len(addrs))
				for _, pinfo := range providerScoreInfos {
					addrs = append(addrs, pinfo.addr)
				}
			}
			connect(addrs)
		}

		if found != expect && c.scope&ScopeLAN == ScopeLAN {
			c.lg.Debugf("finding %s_%s in ScopeLAN", publisher, service)
			addrs := c.lookupServiceLAN(publisher, service, providerIDs...)
			selectAndConnect(addrs)
		}
		if found != expect && c.scope&ScopeWAN == ScopeWAN {
			c.lg.Debugf("finding %s_%s in ScopeWAN", publisher, service)
			if len(c.registryAddr) == 0 {
				addr, err := discoverRegistryAddr(c.lg)
				if err != nil {
					addr = "NA"
				}
				c.registryAddr = addr
				c.lg.Debugf("discovered registry address: %s", addr)
			}
			if c.registryAddr != "NA" {
				addrs := c.lookupServiceWAN(publisher, service, providerIDs...)
				selectAndConnect(addrs)
			}
		}
		return found
	}

	go func() {
		defer close(connections)
		found := 0
		timeout := c.discoverTimeout * 1000 / c.checkIntervalMS
		checkIntervalMS := time.Duration(c.checkIntervalMS) * time.Millisecond
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
			time.Sleep(checkIntervalMS)
			timeout--
		}
	}()

	return connections
}
