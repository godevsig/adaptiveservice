package adaptiveservice

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/niubaoshu/gotiny"
)

var (
	udsRegistryDir = "/var/tmp/adaptiveservice/"
	chanRegistry   = struct {
		sync.RWMutex
		table map[string]*chanTransport
	}{table: make(map[string]*chanTransport)}
)

func init() {
	if err := os.MkdirAll(udsRegistryDir, 0755); err != nil {
		panic(err)
	}
}

func regServiceChan(publisherName, serviceName string, ct *chanTransport) {
	name := publisherName + "_" + serviceName
	chanRegistry.Lock()
	defer chanRegistry.Unlock()
	if _, has := chanRegistry.table[name]; has {
		panic("registering duplicated channel for " + name)
	}
	chanRegistry.table[name] = ct
}

// support wildcard
func queryServiceProcess(publisherName, serviceName string) (serviceInfos []*ServiceInfo) {
	name := publisherName + "_" + serviceName
	if strings.Contains(name, "*") {
		chanRegistry.RLock()
		for ctname := range chanRegistry.table {
			chanRegistry.RUnlock()
			if wildcardMatch(name, ctname) {
				strs := strings.Split(ctname, "_")
				sInfo := &ServiceInfo{strs[0], strs[1], "self", "internal"}
				serviceInfos = append(serviceInfos, sInfo)
			}
			chanRegistry.RLock()
		}
		chanRegistry.RUnlock()
	} else {
		chanRegistry.RLock()
		ct := chanRegistry.table[name]
		chanRegistry.RUnlock()
		if ct != nil {
			sInfo := &ServiceInfo{publisherName, serviceName, "self", "internal"}
			serviceInfos = append(serviceInfos, sInfo)
		}
	}
	return
}

func lookupServiceChan(publisherName, serviceName string) *chanTransport {
	name := publisherName + "_" + serviceName
	chanRegistry.RLock()
	defer chanRegistry.RUnlock()
	return chanRegistry.table[name]
}

// support wildcard
func queryServiceOS(publisherName, serviceName string) (serviceInfos []*ServiceInfo) {
	name := publisherName + "_" + serviceName
	if strings.Contains(name, "*") {
		sockets, _ := filepath.Glob(udsRegistryDir + "*_*.sock")
		for _, socket := range sockets {
			s := strings.TrimPrefix(socket, udsRegistryDir)
			s = strings.TrimSuffix(s, ".sock")
			strs := strings.Split(s, "_")
			if wildcardMatch(name, s) {
				sInfo := &ServiceInfo{strs[0], strs[1], "self", socket}
				serviceInfos = append(serviceInfos, sInfo)
			}
		}
	} else {
		socket := udsRegistryDir + name + ".sock"
		if _, err := os.Stat(socket); os.IsNotExist(err) {
			return
		}
		sInfo := &ServiceInfo{publisherName, serviceName, "self", socket}
		serviceInfos = append(serviceInfos, sInfo)
	}

	return
}

func addrUDS(publisherName, serviceName string) (addr string) {
	return udsRegistryDir + publisherName + "_" + serviceName + ".sock"
}

func lookupServiceUDS(publisherName, serviceName string) (addr string) {
	filename := addrUDS(publisherName, serviceName)
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return
	}
	return filename
}

func (svc *service) regServiceLAN(port string) error {
	c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(svc.s.lg)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
	if conn == nil {
		panic("LANRegistry not found")
	}
	defer conn.Close()
	return conn.SendRecv(&registerServiceForLAN{svc.publisherName, svc.serviceName, port}, nil)
}

// support wildcard
func queryServiceLAN(publisherName, serviceName string, lg Logger) (serviceInfos []*ServiceInfo) {
	c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(lg)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
	if conn == nil {
		panic("lan registry not found")
	}
	defer conn.Close()
	conn.SendRecv(&queryServiceInLAN{publisherName, serviceName}, &serviceInfos)
	return
}

func (c *Client) lookupServiceLAN(publisherName, serviceName string, providerIDs ...string) (addrs []string) {
	serviceInfos := queryServiceLAN(publisherName, serviceName, c.lg)
	has := func(target string) bool {
		if len(providerIDs) == 0 { // match all
			return true
		}
		for _, str := range providerIDs {
			if str == target {
				return true
			}
		}
		return false
	}
	for _, provider := range serviceInfos {
		if has(provider.ProviderID) {
			addrs = append(addrs, provider.Addr)
		}
	}
	return
}

// taken from https://github.com/IBM/netaddr/blob/master/net_utils.go
// NewIP returns a new IP with the given size. The size must be 4 for IPv4 and
// 16 for IPv6.
func newIP(size int) net.IP {
	if size == 4 {
		return net.ParseIP("0.0.0.0").To4()
	}
	if size == 16 {
		return net.ParseIP("::")
	}
	panic("Bad value for size")
}

// BroadcastAddr returns the last address in the given network, or the broadcast address.
func broadcastAddr(n *net.IPNet) net.IP {
	// The golang net package doesn't make it easy to calculate the broadcast address. :(
	broadcast := newIP(len(n.IP))
	for i := 0; i < len(n.IP); i++ {
		broadcast[i] = n.IP[i] | ^n.Mask[i]
	}
	return broadcast
}

type infoLAN struct {
	ip        net.IP // self ip
	ipnet     *net.IPNet
	bcastAddr *net.UDPAddr
}

type packetMsg struct {
	msg   interface{}
	raddr net.Addr
}

type providerInfo struct {
	timeStamp time.Time
	addr      string
	proxied   bool
}

type providers struct {
	timeStamp time.Time                // last update time
	table     map[string]*providerInfo // {"providerID1":{time, "192.168.0.11:12345"}, {time, "providerID2":"192.168.0.26:33556"}}
}

type registryLAN struct {
	s          *Server
	packetConn net.PacketConn
	infoLANs   []*infoLAN
	done       chan struct{}
	cmdChan    chan interface{}
}

func (s *Server) newLANRegistry() (*registryLAN, error) {
	packetConn, err := net.ListenPacket("udp4", ":"+s.broadcastPort)
	if err != nil {
		return nil, err
	}

	var infoLANs []*infoLAN
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		ip, ipnet, _ := net.ParseCIDR(addr.String())
		if ip.To4() != nil && !ip.IsLoopback() {
			bcast := broadcastAddr(ipnet)
			bcastAddr, _ := net.ResolveUDPAddr("udp4", bcast.String()+":"+s.broadcastPort)
			il := &infoLAN{
				ip:        ip,
				ipnet:     ipnet,
				bcastAddr: bcastAddr,
			}
			infoLANs = append(infoLANs, il)
		}
	}

	r := &registryLAN{
		s:          s,
		packetConn: packetConn,
		infoLANs:   infoLANs,
		done:       make(chan struct{}),
		cmdChan:    make(chan interface{}),
	}

	go r.run()
	return r, nil
}

// msg must be pointer
func (r *registryLAN) broadcast(msg interface{}) error {
	bufMsg := gotiny.Marshal(&msg)
	r.s.lg.Debugf("broadcast LAN msg %#v:  %s", msg, bufMsg)
	for _, lan := range r.infoLANs {
		if _, err := r.packetConn.WriteTo(bufMsg, lan.bcastAddr); err != nil {
			return err
		}
	}
	return nil
}

// ServiceInfo is service information.
type ServiceInfo struct {
	Publisher  string
	Service    string
	ProviderID string
	Addr       string // "192.168.0.11:12345"
}

type queryInLAN struct {
	name string // "publisher_service"
}

type foundInLAN struct {
	name       string // "publisher_service"
	providerID string
	port       string
}

func init() {
	RegisterType(([]*ServiceInfo)(nil))
	RegisterType((*queryInLAN)(nil))
	RegisterType((*foundInLAN)(nil))
}

type cmdLANRegister struct {
	name string // "publisher_service"
	port string
}

type cmdLANQuery struct {
	name            string // "publisher_service"
	chanServiceInfo chan []*ServiceInfo
}

func (r *registryLAN) registerServiceForLAN(publisher, service, port string) {
	name := publisher + "_" + service
	r.cmdChan <- &cmdLANRegister{name, port}
}

// support wildcard
func (r *registryLAN) queryServiceInLAN(publisher, service string) []*ServiceInfo {
	name := publisher + "_" + service
	cmd := &cmdLANQuery{name, make(chan []*ServiceInfo, 1)}
	r.cmdChan <- cmd

	return <-cmd.chanServiceInfo
}

func (r *registryLAN) run() {
	pconn := r.packetConn
	lg := r.s.lg
	packetChan := make(chan *packetMsg)
	// "publisher_service": "12345"
	localServiceTable := make(map[string]string)
	// "publisher_service": {time.Time, {"providerID1":"192.168.0.11:12345", "providerID2":"192.168.0.26:33556"}}
	serviceCache := make(map[string]*providers)

	readConn := func() {
		buf := make([]byte, 512)
		for {
			if r.done == nil {
				break
			}
			pconn.SetReadDeadline(time.Now().Add(3 * time.Second))
			n, raddr, err := pconn.ReadFrom(buf)
			if err != nil {
				if !os.IsTimeout(err) {
					lg.Warnf("lan registry receive error: %v", err)
				}
				continue
			}
			var msg interface{}
			gotiny.Unmarshal(buf[:n], &msg)
			lg.Debugf("received LAN msg from %v: %#v", raddr, msg)
			packetChan <- &packetMsg{msg, raddr}
		}
	}
	go readConn()

	replyTo := func(msg interface{}, raddr net.Addr) {
		bufMsg := gotiny.Marshal(&msg)
		lg.Debugf("sending LAN msg to %v: %#v", raddr, msg)
		if _, err := pconn.WriteTo(bufMsg, raddr); err != nil {
			lg.Warnf("lan registry send error: %v", err)
		}
	}

	getServiceInfos := func(cmd *cmdLANQuery) {
		var serviceInfos []*ServiceInfo
		walkProviders := func(prvds *providers, name string) {
			ss := strings.Split(name, "_")
			for pID, pInfo := range prvds.table {
				svcInfo := &ServiceInfo{Publisher: ss[0], Service: ss[1], ProviderID: pID, Addr: pInfo.addr}
				serviceInfos = append(serviceInfos, svcInfo)
			}
		}

		if strings.Contains(cmd.name, "*") {
			for name, prvds := range serviceCache {
				if wildcardMatch(cmd.name, name) {
					walkProviders(prvds, name)
				}
			}
		} else {
			if prvds, has := serviceCache[cmd.name]; has {
				walkProviders(prvds, cmd.name)
			}
		}

		cmd.chanServiceInfo <- serviceInfos
	}

	wildcardQuery := ""
	wildcardQueryTime := time.Now()
	chanDelay := make(chan *cmdLANQuery, 8)
	for {
		select {
		case <-r.done:
			return
		case cmd := <-r.cmdChan:
			switch cmd := cmd.(type) {
			case *cmdLANRegister:
				localServiceTable[cmd.name] = cmd.port
			case *cmdLANQuery:
				wait := false
				t := time.Now()
				if strings.Contains(cmd.name, "*") {
					if wildcardQuery != cmd.name || t.After(wildcardQueryTime.Add(5*time.Second)) {
						for name, prvds := range serviceCache {
							if wildcardMatch(cmd.name, name) {
								for pID, pInfo := range prvds.table {
									if t.After(pInfo.timeStamp.Add(15 * time.Minute)) {
										delete(prvds.table, pID)
									}
								}
								prvds.timeStamp = t
							}
						}
						if err := r.broadcast(&queryInLAN{cmd.name}); err != nil {
							lg.Warnf("lan registry send broadcast error: %v", err)
							break
						}
						wildcardQuery = cmd.name
						wildcardQueryTime = t
						wait = true
					}
				} else {
					prvds, has := serviceCache[cmd.name]
					if !has || t.After(prvds.timeStamp.Add(5*time.Second)) {
						if err := r.broadcast(&queryInLAN{cmd.name}); err != nil {
							lg.Warnf("lan registry send broadcast error: %v", err)
							break
						}
						wait = true
						if has {
							prvds.timeStamp = t
						}
					}
				}
				if wait {
					time.AfterFunc(time.Second, func() { chanDelay <- cmd })
				} else {
					getServiceInfos(cmd)
				}
			default:
				panic(fmt.Sprintf("unknown cmd: %v", cmd))
			}
		case cmd := <-chanDelay:
			getServiceInfos(cmd)
		case packetMsg := <-packetChan:
			msg := packetMsg.msg
			raddr := packetMsg.raddr
			switch msg := msg.(type) {
			case *queryInLAN:
				if strings.Contains(msg.name, "*") {
					for name, port := range localServiceTable {
						if wildcardMatch(msg.name, name) {
							replyTo(&foundInLAN{name, r.s.providerID, port}, raddr)
						}
					}
				} else {
					port, has := localServiceTable[msg.name]
					if has {
						replyTo(&foundInLAN{msg.name, r.s.providerID, port}, raddr)
					}
				}
			case *foundInLAN:
				rhost, _, _ := net.SplitHostPort(raddr.String())
				t := time.Now()
				prvds, has := serviceCache[msg.name]
				if !has {
					prvds = &providers{t, make(map[string]*providerInfo)}
					serviceCache[msg.name] = prvds
				}
				prvds.table[msg.providerID] = &providerInfo{t, rhost + ":" + msg.port, false}
			default:
				panic(fmt.Sprintf("unknown msg: %v", msg))
			}
		}
	}
}

func (r *registryLAN) close() {
	close(r.done)
	r.done = nil
}

func (svc *service) regServiceWAN(port string) error {
	c := NewClient(WithScope(ScopeWAN), WithLogger(svc.s.lg))
	conn, err := c.newTCPConnection(svc.s.registryAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	proxied := false
	if svc.providerID != svc.s.providerID {
		proxied = true
	}
	return conn.SendRecv(&regServiceInWAN{svc.publisherName, svc.serviceName, svc.providerID, port, proxied}, nil)
}

// support wildcard
func queryServiceWAN(registryAddr, publisherName, serviceName string, lg Logger) (serviceInfos []*ServiceInfo) {
	c := NewClient(WithScope(ScopeWAN), WithLogger(lg))
	conn, err := c.newTCPConnection(registryAddr)
	if err != nil {
		lg.Errorf("connect to registry failed: %v", err)
		return
	}
	defer conn.Close()

	conn.SendRecv(&queryServiceInWAN{publisherName, serviceName}, &serviceInfos)
	return
}

func (c *Client) lookupServiceWAN(publisherName, serviceName string, providerIDs ...string) (addrs []string) {
	has := func(target string) bool {
		if len(providerIDs) == 0 { // match all
			return true
		}
		for _, str := range providerIDs {
			if str == target {
				return true
			}
		}
		return false
	}

	serviceInfos := queryServiceWAN(c.registryAddr, publisherName, serviceName, c.lg)
	for _, provider := range serviceInfos {
		if has(provider.ProviderID) {
			addrs = append(addrs, provider.Addr)
		}
	}
	return
}

type providerMap struct {
	sync.RWMutex
	providers map[string]*providerInfo
}

type rootRegistry struct {
	sync.RWMutex
	// "publisher_service": {"providerID1":{timeStamp, "192.168.0.11:12345"}, "providerID2":{timeStamp, "192.168.0.26:33556"}}
	serviceMap map[string]*providerMap
}

func pingService(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return ErrServiceNotReachable
	}
	conn.Close()
	return nil
}

// reply OK or error
type testReverseProxy struct {
	port string
}

func (msg *testReverseProxy) Handle(stream ContextStream) (reply interface{}) {
	ss := stream.(*streamServerStream)
	rhost, _, _ := net.SplitHostPort(ss.netconn.RemoteAddr().String())

	raddr := rhost + ":" + msg.port
	if err := pingService(raddr); err != nil {
		return err
	}
	return OK
}

// reply OK or error
type regServiceInWAN struct {
	publisher  string
	service    string
	providerID string
	port       string
	proxied    bool
}

func (msg *regServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	rr := stream.GetContext().(*rootRegistry)
	ss := stream.(*streamServerStream)
	rhost, _, _ := net.SplitHostPort(ss.netconn.RemoteAddr().String())

	raddr := rhost + ":" + msg.port
	if err := pingService(raddr); err != nil {
		return err
	}

	name := msg.publisher + "_" + msg.service
	rr.Lock()
	pmap, has := rr.serviceMap[name]
	if !has {
		pmap = &providerMap{
			providers: make(map[string]*providerInfo),
		}
		rr.serviceMap[name] = pmap
	}
	rr.Unlock()

	pinfo := &providerInfo{time.Now(), raddr, msg.proxied}
	pmap.Lock()
	pmap.providers[msg.providerID] = pinfo
	pmap.Unlock()

	return OK
}

// reply []*ServiceInfo
// support wildcard(*) matching
type queryServiceInWAN struct {
	publisher string
	service   string
}

func (msg *queryServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	rr := stream.GetContext().(*rootRegistry)
	name := msg.publisher + "_" + msg.service
	var serviceInfos []*ServiceInfo

	walkProviderMap := func(service string, pmap *providerMap) {
		pmap.RLock()
		for pID, pInfo := range pmap.providers {
			func() {
				pmap.RUnlock()
				defer pmap.RLock()
				t := time.Now()
				if t.After(pInfo.timeStamp.Add(15 * time.Minute)) {
					if err := pingService(pInfo.addr); err != nil {
						pmap.Lock()
						delete(pmap.providers, pID)
						pmap.Unlock()
						return
					}
					pInfo.timeStamp = t
				}
				addr := pInfo.addr
				if pInfo.proxied {
					addr += "P"
				}
				svcInfo := &ServiceInfo{ProviderID: pID, Addr: addr}
				if len(service) != 0 {
					ss := strings.Split(service, "_")
					svcInfo.Publisher = ss[0]
					svcInfo.Service = ss[1]
				}
				serviceInfos = append(serviceInfos, svcInfo)
			}()
		}
		pmap.RUnlock()
	}

	if strings.Contains(name, "*") {
		rr.RLock()
		for service, pmap := range rr.serviceMap {
			rr.RUnlock()
			if wildcardMatch(name, service) {
				walkProviderMap(service, pmap)
			}
			rr.RLock()
		}
		rr.RUnlock()
	} else {
		rr.RLock()
		pmap, has := rr.serviceMap[name]
		rr.RUnlock()
		if has {
			walkProviderMap("", pmap)
		}
	}

	return serviceInfos
}

func init() {
	RegisterType((*regServiceInWAN)(nil))
	RegisterType((*queryServiceInWAN)(nil))
	RegisterType((*testReverseProxy)(nil))
}

func (s *Server) startRootRegistry(port string) error {
	svc := &service{
		publisherName: BuiltinPublisher,
		serviceName:   "rootRegistry",
		providerID:    s.providerID,
		knownMsgTypes: make(map[reflect.Type]struct{}),
		s:             s,
	}
	svc.knownMsgTypes[reflect.TypeOf((*regServiceInWAN)(nil))] = struct{}{}
	svc.knownMsgTypes[reflect.TypeOf((*queryServiceInWAN)(nil))] = struct{}{}
	svc.knownMsgTypes[reflect.TypeOf((*testReverseProxy)(nil))] = struct{}{}

	rr := &rootRegistry{
		serviceMap: make(map[string]*providerMap),
	}
	svc.fnOnNewStream = func(ctx Context) {
		ctx.SetContext(rr)
	}
	tran, err := svc.newTCPTransport(port)
	if err != nil {
		return err
	}
	s.addCloser(tran)
	return nil
}
