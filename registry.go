package adaptiveservice

import (
	"net"
	"os"
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

func lookupServiceChan(publisherName, serviceName string) *chanTransport {
	name := publisherName + "_" + serviceName
	chanRegistry.RLock()
	defer chanRegistry.RUnlock()
	return chanRegistry.table[name]
}

func lookupServiceUDS(publisherName, serviceName string) (addr string) {
	filename := udsRegistryDir + publisherName + "_" + serviceName + ".sock"
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return
	}
	return filename
}

func regServiceLAN(publisherName, serviceName, port string) error {
	c := NewClient(WithScope(ScopeProcess | ScopeOS)).SetDiscoverTimeout(0)
	defer c.Close()

	conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
	if conn == nil {
		panic("LANRegistry not found")
	}
	defer conn.Close()
	return conn.SendRecv(&registerServiceForLAN{publisherName, serviceName, port}, nil)
}

func lookupServiceLAN(publisherName, serviceName string, providerIDs ...string) (addrs []string) {
	c := NewClient(WithScope(ScopeProcess | ScopeOS)).SetDiscoverTimeout(0)
	defer c.Close()

	conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
	if conn == nil {
		panic("LANRegistry not found")
	}
	defer conn.Close()
	var serviceInfos []*serviceInfo
	err := conn.SendRecv(&queryServiceInLAN{publisherName, serviceName}, &serviceInfos)
	if err != nil {
		return
	}
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
		if has(provider.providerID) {
			addrs = append(addrs, provider.addr)
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

type providers struct {
	timeStamp time.Time         // last update time
	table     map[string]string // {"providerID1":"192.168.0.11:12345", "providerID2":"192.168.0.26:33556"}
}

type registryLAN struct {
	s                 *Server
	packetConn        net.PacketConn
	infoLANs          []*infoLAN
	done              chan struct{}
	cmdChan           chan interface{}
	packetChan        chan *packetMsg
	localServiceTable map[string]string // "publisher_service": "12345"
	// "publisher_service": {time.Time, {"providerID1":"192.168.0.11:12345", "providerID2":"192.168.0.26:33556"}}
	serviceCache map[string]*providers
}

func (s *Server) newLANRegistry() (*registryLAN, error) {
	packetConn, err := net.ListenPacket("udp4", ":"+s.bcastPort)
	if err != nil {
		return nil, err
	}

	var infoLANs []*infoLAN
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		ip, ipnet, _ := net.ParseCIDR(addr.String())
		if ip.To4() != nil && !ip.IsLoopback() {
			bcast := broadcastAddr(ipnet)
			bcastAddr, _ := net.ResolveUDPAddr("udp4", bcast.String()+":"+s.bcastPort)
			il := &infoLAN{
				ip:        ip,
				ipnet:     ipnet,
				bcastAddr: bcastAddr,
			}
			infoLANs = append(infoLANs, il)
		}
	}

	r := &registryLAN{
		s:                 s,
		packetConn:        packetConn,
		infoLANs:          infoLANs,
		done:              make(chan struct{}),
		cmdChan:           make(chan interface{}),
		packetChan:        make(chan *packetMsg),
		localServiceTable: make(map[string]string),
		serviceCache:      make(map[string]*providers),
	}

	go r.run()
	return r, nil
}

// msg must be pointer
func (r *registryLAN) broadcast(msg interface{}) error {
	bufMsg := gotiny.Marshal(msg)

	for _, lan := range r.infoLANs {
		if _, err := r.packetConn.WriteTo(bufMsg, lan.bcastAddr); err != nil {
			return err
		}
	}
	return nil
}

type serviceInfo struct {
	name       string // "publisher_service"
	providerID string
	addr       string // "192.168.0.11:12345"
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
	RegisterType(([]*serviceInfo)(nil))
	RegisterType((*queryInLAN)(nil))
	RegisterType((*foundInLAN)(nil))
}

type cmdLANRegister struct {
	name string // "publisher_service"
	port string
}

type cmdLANQuery struct {
	name            string // "publisher_service"
	interval        int    // in seconds
	chanServiceInfo chan []*serviceInfo
}

func (r *registryLAN) registerServiceForLAN(publisher, service, port string) {
	name := publisher + "_" + service
	r.cmdChan <- &cmdLANRegister{name, port}
}

func (r *registryLAN) queryServiceInLAN(publisher, service string) []*serviceInfo {
	name := publisher + "_" + service
	cmd := &cmdLANQuery{name, 1, make(chan []*serviceInfo, 1)}
	r.cmdChan <- cmd

	return <-cmd.chanServiceInfo
}

func (r *registryLAN) run() {
	pconn := r.packetConn
	lg := r.s.lg

	readConn := func() {
		buf := make([]byte, 512)
		for {
			if r.done == nil {
				break
			}
			pconn.SetReadDeadline(time.Now().Add(3 * time.Second))
			n, raddr, err := pconn.ReadFrom(buf)
			if err != nil {
				lg.Warnln("lan registry receive error: ", err)
				continue
			}
			var msg interface{}
			gotiny.Unmarshal(buf[:n], &msg)
			r.packetChan <- &packetMsg{msg, raddr}
		}
	}
	go readConn()

	replyTo := func(msg interface{}, raddr net.Addr) {
		bufMsg := gotiny.Marshal(msg)
		if _, err := pconn.WriteTo(bufMsg, raddr); err != nil {
			lg.Warnln("lan registry send error: ", err)
		}
	}

	getServiceInfos := func(cmd *cmdLANQuery) {
		prvds := r.serviceCache[cmd.name]
		var serviceInfos []*serviceInfo
		for providerID, addr := range prvds.table {
			svcInfo := &serviceInfo{providerID: providerID, addr: addr}
			serviceInfos = append(serviceInfos, svcInfo)
		}
		cmd.chanServiceInfo <- serviceInfos
	}

	chanDelay := make(chan *cmdLANQuery, 8)

	for {
		select {
		case <-r.done:
			return
		case cmd := <-r.cmdChan:
			switch cmd := cmd.(type) {
			case *cmdLANRegister:
				r.localServiceTable[cmd.name] = cmd.port
			case *cmdLANQuery:
				t := time.Now()
				prvds, has := r.serviceCache[cmd.name]
				if !has {
					prvds = &providers{t, make(map[string]string)}
					r.serviceCache[cmd.name] = prvds
				}
				if t.After(prvds.timeStamp.Add(time.Duration(cmd.interval) * time.Second)) {
					getServiceInfos(cmd)
					if err := r.broadcast(&queryInLAN{cmd.name}); err != nil {
						lg.Warnln("lan registry send broadcast error: ", err)
						break
					}
					prvds.timeStamp = t
				} else {
					time.AfterFunc(time.Duration(cmd.interval)*time.Second, func() { chanDelay <- cmd })
				}
			default:
				panic("unknown cmd")
			}
		case cmd := <-chanDelay:
			getServiceInfos(cmd)
		case packetMsg := <-r.packetChan:
			msg := packetMsg.msg
			raddr := packetMsg.raddr
			switch msg := msg.(type) {
			case *queryInLAN:
				port, has := r.localServiceTable[msg.name]
				if has {
					replyTo(&foundInLAN{msg.name, r.s.providerID, port}, raddr)
				}
			case *foundInLAN:
				rhost, _, _ := net.SplitHostPort(raddr.String())
				prvds := r.serviceCache[msg.name]
				prvds.table[msg.providerID] = rhost + ":" + msg.port
			default:
				panic("unknown msg")
			}
		}
	}
}

func (r *registryLAN) close() {
	close(r.done)
	r.done = nil
}

func regServiceWAN(publisherName, serviceName, providerID, port string) error {
	c := NewClient(WithScope(ScopeWAN))
	c.init()
	defer c.Close()

	conn, err := c.newTCPConnection(c.registryAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.SendRecv(&regServiceInWAN{publisherName, serviceName, providerID, port}, nil)
}

// support wildcard
func queryServiceWAN(publisherName, serviceName string) (serviceInfos []*serviceInfo) {
	c := NewClient(WithScope(ScopeWAN))
	c.init()
	defer c.Close()

	conn, err := c.newTCPConnection(c.registryAddr)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SendRecv(&queryServiceInWAN{publisherName, serviceName}, &serviceInfos)
	return
}

func allServicesWAN() []*serviceInfo {
	return queryServiceWAN("*", "*")
}

func lookupServiceWAN(publisherName, serviceName string, providerIDs ...string) (addrs []string) {
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

	serviceInfos := queryServiceWAN(publisherName, serviceName)
	for _, provider := range serviceInfos {
		if has(provider.providerID) {
			addrs = append(addrs, provider.addr)
		}
	}
	return
}

type providerInfo struct {
	timeStamp time.Time
	addr      string
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

// reply OK or ErrServiceNotReachable
type regServiceInWAN struct {
	publisher  string
	service    string
	providerID string
	port       string
}

func (msg *regServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	rr := stream.GetContext().(*rootRegistry)
	ss := stream.(*streamServerStream)
	rhost, _, _ := net.SplitHostPort(ss.netconn.RemoteAddr().String())

	name := msg.publisher + "_" + msg.service
	rr.Lock()
	pmap, has := rr.serviceMap[name]
	if !has {
		pmap = &providerMap{
			providers: make(map[string]*providerInfo),
		}
	}
	rr.Unlock()
	raddr := rhost + msg.port
	if err := pingService(raddr); err != nil {
		return err
	}
	pinfo := &providerInfo{time.Now(), raddr}
	pmap.Lock()
	pmap.providers[msg.providerID] = pinfo
	pmap.Unlock()
	return OK
}

// reply []*serviceInfo
// support wildcard(*) matching
type queryServiceInWAN struct {
	publisher string
	service   string
}

func (msg *queryServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	rr := stream.GetContext().(*rootRegistry)
	name := msg.publisher + "_" + msg.service
	var serviceInfos []*serviceInfo

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
				svcInfo := &serviceInfo{name: service, providerID: pID, addr: pInfo.addr}
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
}

func (s *Server) runRootRegistry() error {
	if len(s.rootRegistryPort) == 0 {
		panic("no rootRegistryPort")
	}
	svc := &service{
		publisherName: "godevsig",
		serviceName:   "rootRegistry",
		knownMsgTypes: make(map[reflect.Type]struct{}),
		s:             s,
		scope:         ScopeWAN,
	}
	svc.knownMsgTypes[reflect.TypeOf((*regServiceInWAN)(nil))] = struct{}{}
	svc.knownMsgTypes[reflect.TypeOf((*queryServiceInWAN)(nil))] = struct{}{}

	rr := &rootRegistry{
		serviceMap: make(map[string]*providerMap),
	}
	svc.fnOnNewStream = func(ctx Context) error {
		ctx.SetContext(rr)
		return nil
	}
	tran, err := svc.newTCPTransport(s.rootRegistryPort)
	if err != nil {
		return err
	}
	s.addCloser(tran)
	return nil
}
