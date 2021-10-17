package adaptiveservice

import (
	"errors"
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
	if err := os.MkdirAll(udsRegistryDir, 0777); err != nil {
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

func serviceNamesInProcess(publisherName, serviceName string) (names []string) {
	name := publisherName + "_" + serviceName
	if strings.Contains(name, "*") {
		chanRegistry.RLock()
		for ctname := range chanRegistry.table {
			chanRegistry.RUnlock()
			if wildcardMatch(name, ctname) {
				names = append(names, ctname)
			}
			chanRegistry.RLock()
		}
		chanRegistry.RUnlock()
	} else {
		chanRegistry.RLock()
		ct := chanRegistry.table[name]
		chanRegistry.RUnlock()
		if ct != nil {
			names = append(names, name)
		}
	}
	return
}

// support wildcard
func queryServiceProcess(publisherName, serviceName string) (serviceInfos []*ServiceInfo) {
	names := serviceNamesInProcess(publisherName, serviceName)
	for _, name := range names {
		strs := strings.Split(name, "_")
		sInfo := &ServiceInfo{strs[0], strs[1], "self", "internal"}
		serviceInfos = append(serviceInfos, sInfo)
	}
	return
}

// support wildcard
func (c *Client) lookupServiceChan(publisherName, serviceName string) (ccts []*clientChanTransport) {
	names := serviceNamesInProcess(publisherName, serviceName)
	for _, name := range names {
		chanRegistry.RLock()
		ct := chanRegistry.table[name]
		chanRegistry.RUnlock()
		if ct != nil {
			ccts = append(ccts, &clientChanTransport{c, ct})
		}
	}
	return
}

func addrUDS(publisherName, serviceName string) (addr string) {
	return udsRegistryDir + publisherName + "_" + serviceName + ".sock"
}

func serviceNamesInOS(publisherName, serviceName string) (names []string) {
	name := publisherName + "_" + serviceName
	if strings.Contains(name, "*") {
		sockets, _ := filepath.Glob(udsRegistryDir + "*_*.sock")
		for _, socket := range sockets {
			s := strings.TrimPrefix(socket, udsRegistryDir)
			s = strings.TrimSuffix(s, ".sock")
			if wildcardMatch(name, s) {
				names = append(names, s)
			}
		}
	} else {
		socket := udsRegistryDir + name + ".sock"
		if _, err := os.Stat(socket); os.IsNotExist(err) {
			return
		}
		names = append(names, name)
	}
	return
}

// support wildcard
func queryServiceOS(publisherName, serviceName string) (serviceInfos []*ServiceInfo) {
	names := serviceNamesInOS(publisherName, serviceName)
	for _, name := range names {
		strs := strings.Split(name, "_")
		sInfo := &ServiceInfo{strs[0], strs[1], "self", udsRegistryDir + name + ".sock"}
		serviceInfos = append(serviceInfos, sInfo)
	}
	return
}

// support wildcard
func lookupServiceUDS(publisherName, serviceName string) (addrs []string) {
	names := serviceNamesInOS(publisherName, serviceName)
	for _, name := range names {
		addrs = append(addrs, udsRegistryDir+name+".sock")
	}
	return
}

func (svc *service) regServiceLAN(port string) error {
	c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(svc.s.lg)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
	if conn == nil {
		return errors.New("LANRegistry not found")
	}
	defer conn.Close()
	return conn.SendRecv(&registerServiceForLAN{svc.publisherName, svc.serviceName, port}, nil)
}

// support wildcard
func queryServiceLAN(publisherName, serviceName string, lg Logger) (serviceInfos []*ServiceInfo) {
	c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(lg)).SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
	if conn == nil {
		return
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
	table map[string]*providerInfo // {"providerID1":{time, "192.168.0.11:12345"}, {time, "providerID2":"192.168.0.26:33556"}}
}

type serviceInfoTime struct {
	timeStamp time.Time // last update time
	si        []*ServiceInfo
}

type registryLAN struct {
	s          *Server
	packetConn net.PacketConn
	infoLANs   []*infoLAN
	sync.RWMutex
	serviceInfoCache map[string]*serviceInfoTime
	done             chan struct{}
	cmdChan          chan interface{}
}

func (s *Server) newLANRegistry() (*registryLAN, error) {
	packetConn, err := net.ListenPacket("udp4", ":"+s.broadcastPort)
	if err != nil {
		s.lg.Errorf("listen lan broadcast error: %v", err)
		return nil, err
	}
	s.addCloser(ioCloser(packetConn.Close))

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
		s:                s,
		packetConn:       packetConn,
		infoLANs:         infoLANs,
		serviceInfoCache: make(map[string]*serviceInfoTime),
		done:             make(chan struct{}),
		cmdChan:          make(chan interface{}),
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
	Addr       string // "192.168.0.11:12345", "192.168.0.11:12345P" if proxied
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
	r.Lock()
	if len(r.serviceInfoCache) > 1000 {
		r.serviceInfoCache = make(map[string]*serviceInfoTime)
	}
	sit := r.serviceInfoCache[name]
	r.Unlock()

	if sit != nil && time.Since(sit.timeStamp) < 15*time.Second {
		return sit.si
	}

	cmd := &cmdLANQuery{name, make(chan []*ServiceInfo, 1)}
	r.cmdChan <- cmd
	si := <-cmd.chanServiceInfo

	r.Lock()
	r.serviceInfoCache[name] = &serviceInfoTime{time.Now(), si}
	r.Unlock()

	return si
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
				if time.Since(pInfo.timeStamp) > 15*time.Minute {
					delete(prvds.table, pID)
				}
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

	tLocalServiceUpdate := time.Now()
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
				if err := r.broadcast(&queryInLAN{cmd.name}); err != nil {
					lg.Warnf("lan registry send broadcast error: %v", err)
					break
				}
				time.AfterFunc(100*time.Millisecond, func() { chanDelay <- cmd })
			default:
				lg.Warnf("LAN receiver: unknown cmd: %v", cmd)
			}
		case cmd := <-chanDelay:
			getServiceInfos(cmd)
		case packetMsg := <-packetChan:
			t := time.Now()
			if t.After(tLocalServiceUpdate.Add(15 * time.Minute)) {
				tLocalServiceUpdate = t
				for name, port := range localServiceTable {
					if err := pingService("127.0.0.1:" + port); err != nil {
						delete(localServiceTable, name)
					}
				}
			}
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
				prvds, has := serviceCache[msg.name]
				if !has {
					prvds = &providers{table: make(map[string]*providerInfo)}
					serviceCache[msg.name] = prvds
				}
				prvds.table[msg.providerID] = &providerInfo{time.Now(), rhost + ":" + msg.port, false}
			default:
				lg.Warnf("LAN receiver: unknown msg: %v", msg)
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

func (s *Server) registryCheckSaver() {
	for {
		time.Sleep(time.Minute)
		svcs := queryServiceWAN(s.registryAddr, "*", "*", s.lg)
		f, err := os.Create("services.record.updating")
		if err != nil {
			s.lg.Errorf("root registry: record file not created: %v", err)
			return
		}
		for _, si := range svcs {
			fmt.Fprintf(f, "%s %s %s %s\n", si.Publisher, si.Service, si.ProviderID, si.Addr)
		}
		f.Close()
		os.Rename("services.record.updating", "services.record")
	}
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
	f, err := os.Open("services.record")
	if err == nil {
		for {
			var publisher, service, providerID, addr string
			_, err := fmt.Fscanln(f, &publisher, &service, &providerID, &addr)
			if err != nil {
				break
			}
			name := publisher + "_" + service
			pmap := rr.serviceMap[name]
			if pmap == nil {
				pmap = &providerMap{providers: make(map[string]*providerInfo)}
				rr.serviceMap[name] = pmap
			}
			pinfo := &providerInfo{time.Now(), addr, false}
			if addr[len(addr)-1] == 'P' {
				pinfo.addr = addr[:len(addr)-1]
				pinfo.proxied = true
			}
			pmap.providers[providerID] = pinfo
		}
		f.Close()
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
