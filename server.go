package adaptiveservice

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
)

// Server provides services.
type Server struct {
	sync.Mutex
	*conf
	publisher        string
	broadcastPort    string
	rootRegistry     bool
	autoReverseProxy bool
	serviceLister    bool
	ipObserver       bool
	msgTracer        bool
	errRecovers      chan errorRecover
	mq               *msgQ
	residentWorkers  int
	qSizePerCore     int
	qWeight          int
	msgTypeCheck     bool
	closers          []closer
	initialized      bool
	closed           chan struct{}
}

const (
	// DefaultQSizePerCore is the default value for qSizePerCore
	DefaultQSizePerCore int = 128
	// DefaultQWeight is the default value for qWeight
	DefaultQWeight int = 8
)

// NewServer creates a server which publishes services.
func NewServer(options ...Option) *Server {
	s := &Server{
		conf:            newConf(),
		publisher:       "default.org",
		errRecovers:     make(chan errorRecover, 1),
		residentWorkers: 1,
		qSizePerCore:    DefaultQSizePerCore,
		qWeight:         DefaultQWeight,
		msgTypeCheck:    true,
		closed:          make(chan struct{}),
	}

	for _, o := range options {
		o(s.conf)
	}

	s.lg.Debugf("new server created")
	return s
}

func genID() string {
	var b []byte

	itfs, _ := net.Interfaces()
	for _, itf := range itfs {
		dev, err := os.Readlink("/sys/class/net/" + itf.Name)
		if err != nil || strings.Contains(dev, "virtual") {
			continue
		}
		if len(itf.HardwareAddr) != 0 {
			b = itf.HardwareAddr
			break
		}
	}

	if len(b) == 0 {
		b = make([]byte, 6)
		rand.Read(b)
	}

	id := hex.EncodeToString(b)
	return id
}

func (s *Server) initProviderID() error {
	if id, err := GetSelfProviderID(); err != nil {
		if len(s.providerID) == 0 {
			s.providerID = genID()
		}
		if err := s.publishProviderInfoService(); err != nil {
			return err
		}
		s.lg.Infof("provider info service started with provider ID: %s", s.providerID)
	} else {
		s.lg.Infof("discovered provider ID: %s", id)
		if len(s.providerID) == 0 {
			s.providerID = id
		}
		if s.providerID != id {
			return fmt.Errorf("provider ID mismatch: user specified %s, discovered %s", s.providerID, id)
		}
	}
	return nil
}

func (s *Server) init() error {
	if s.initialized {
		return nil
	}
	s.initialized = true
	initSigCleaner(s.lg)
	addSigCloser(s)
	s.mq = newMsgQ(s.residentWorkers, s.qSizePerCore, s.qWeight, s.lg)
	s.addCloser(s.mq)

	if s.scope&ScopeLAN == ScopeLAN || s.scope&ScopeWAN == ScopeWAN {
		if err := s.initProviderID(); err != nil {
			return err
		}
	}

	if s.scope&ScopeLAN == ScopeLAN {
		s.lg.Infof("configuring server in local network scope")
		c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(s.lg)).SetDiscoverTimeout(0)
		conn := <-c.Discover(BuiltinPublisher, SrvLANRegistry)
		if conn != nil {
			conn.Close()
			s.lg.Infof("LAN registry running")
		} else {
			if len(s.broadcastPort) == 0 {
				s.lg.Infof("LAN registry not found or configured")
				s.scope &= ^ScopeLAN // not ScopeLAN
			} else {
				if err := s.publishLANRegistryService(); err != nil {
					return err
				}
				s.lg.Infof("user specified broadcast port: %s, LAN registry service started", s.broadcastPort)
			}
		}
	}

	if s.scope&ScopeWAN == ScopeWAN {
		s.lg.Infof("configuring server in public network scope")
		if addr, err := discoverRegistryAddr(s.lg); err != nil {
			if len(s.registryAddr) == 0 {
				s.lg.Infof("root registry address not found or configured")
				s.scope &= ^ScopeWAN // not ScopeWAN
			} else {
				if err := s.publishRegistryInfoService(); err != nil {
					return err
				}
				s.lg.Infof("user specified root registry address: %s, registry info service started", s.registryAddr)
			}
		} else {
			s.registryAddr = addr
			s.lg.Infof("discovered root registry address: %s", addr)
		}
	}

	if s.rootRegistry {
		if len(s.registryAddr) == 0 {
			return errors.New("root registry address not configured")
		}
		if s.scope&ScopeWAN != ScopeWAN {
			panic("scope error")
		}

		_, port, _ := net.SplitHostPort(s.registryAddr)
		if err := s.startRootRegistry(port); err != nil {
			return err
		}

		s.lg.Infof("root registry started at %s", port)
	}

	if s.scope&ScopeWAN == ScopeWAN && s.autoReverseProxy {
		canProxy := func() bool {
			if s.rootRegistry {
				return true
			}

			network := 0
			addrs, _ := net.InterfaceAddrs()
			for _, addr := range addrs {
				ip, _, _ := net.ParseCIDR(addr.String())
				if ip.To4() != nil && !ip.IsLoopback() {
					network++
				}
			}
			if network < 2 {
				s.lg.Debugf("reverse proxy not needed in less than 2 networks")
				return false
			}

			lnr, err := net.Listen("tcp", ":0")
			if err != nil {
				s.lg.Debugf("auto reverse proxy: listen error: %v", err)
				return false
			}
			defer lnr.Close()
			go func() {
				for {
					netconn, err := lnr.Accept()
					if err != nil {
						return
					}
					netconn.Close()
				}
			}()
			addr := lnr.Addr().String()
			_, port, _ := net.SplitHostPort(addr) // from [::]:43807

			c := NewClient(WithScope(ScopeWAN), WithLogger(s.lg))
			conn, err := c.newTCPConnection(s.registryAddr)
			if err != nil {
				s.lg.Debugf("root registry not reachable: %v", err)
				return false
			}
			defer conn.Close()
			if err := conn.SendRecv(&testReverseProxy{port: port}, nil); err != nil {
				s.lg.Debugf("reverse port not reachable: %v", err)
				return false
			}
			return true
		}

		if canProxy() {
			scope := ScopeLAN
			if s.rootRegistry {
				scope |= ScopeWAN
			}
			if err := s.publishReverseProxyService(scope); err != nil {
				return err
			}
			s.lg.Infof("reverse proxy started")
		}
	}

	if s.serviceLister {
		if err := s.publishServiceListerService(ScopeProcess | ScopeOS); err != nil {
			return err
		}
		s.lg.Infof("service lister started")
	}

	if s.ipObserver {
		if err := s.publishIPObserverService(); err != nil {
			return err
		}
		s.lg.Infof("IP observer started")
	}

	if s.msgTracer {
		if err := s.publishMessageTracingService(); err != nil {
			return err
		}
		s.lg.Infof("message tracer started")
	}
	mTraceHelper.init(s.lg)

	s.lg.Debugf("server initialized")
	return nil
}

type service struct {
	publisherName   string
	serviceName     string
	providerID      string
	knownMsgTypes   map[reflect.Type]struct{}
	s               *Server
	scope           Scope
	useNamedUDS     bool
	fnOnNewStream   func(Context)                 // called on new stream accepted
	fnOnStreamClose func(Context)                 // called on stream closed
	fnOnConnect     func(Netconn) (takeOver bool) // called on new connection established
	fnOnDisconnect  func(Netconn)                 // called on connection disconnected
}

func (svc *service) canHandle(msg interface{}) bool {
	if _, ok := msg.(KnownMessage); !ok {
		return false
	}
	if svc.s.msgTypeCheck {
		_, has := svc.knownMsgTypes[reflect.TypeOf(msg)]
		return has
	}
	return true
}

func (s *Server) publish(scope Scope, publisherName, serviceName string, knownMessages []KnownMessage, options ...ServiceOption) error {
	s.lg.Debugf("publishing %s %s in scope %b", publisherName, serviceName, scope)
	if !s.initialized {
		if err := s.init(); err != nil {
			return err
		}
	}
	scope = s.scope & scope
	s.lg.Debugf("adjusted %s %s in scope %b", publisherName, serviceName, scope)

	newService := func() *service {
		svc := &service{
			publisherName: publisherName,
			serviceName:   serviceName,
			providerID:    s.providerID,
			knownMsgTypes: make(map[reflect.Type]struct{}),
			s:             s,
			scope:         scope,
		}

		knownMessages = append(knownMessages, QueryMsgQInfo{})
		for _, msg := range knownMessages {
			tp := reflect.TypeOf(msg)
			svc.knownMsgTypes[tp] = struct{}{}
		}
		return svc
	}

	svc := newService()
	for _, opt := range options {
		opt(svc)
	}

	// named uds maybe shared, we need providerID
	if svc.useNamedUDS {
		if err := s.initProviderID(); err != nil {
			return err
		}
		svc.providerID = s.providerID
	}

	if scope&ScopeProcess == ScopeProcess {
		tran, err := svc.newChanTransport()
		if err != nil {
			return err
		}
		s.addCloser(tran)
	}
	if scope&ScopeOS == ScopeOS {
		tran, err := svc.newUDSTransport()
		if err != nil {
			return err
		}
		s.addCloser(tran)
	}
	if scope&ScopeLAN == ScopeLAN || scope&ScopeWAN == ScopeWAN {
		tran, err := svc.newTCPTransport("")
		if err != nil {
			return err
		}
		s.addCloser(tran)
	}

	return nil
}

func checkNameConvention(name string) error {
	if strings.ContainsAny(name, "_/ \n\r\t") {
		return errors.New("name should not contain _ or / or whitespace")
	}
	return nil
}

// Publish publishes service to all available scope of Server s.
// knownMessages are messages that the service can handle, e.g.
// []KnownMessage{(*PublicStructA)(nil), (*PublicStructB)(nil), ...},
// where (*PublicStructA) and (*PublicStructB) are the known messages that
// have `Handle(stream ContextStream) reply interface{}` method.
//
// Publish panics if serviceName contains "_" or "/" or whitespace.
func (s *Server) Publish(serviceName string, knownMessages []KnownMessage, options ...ServiceOption) error {
	if err := checkNameConvention(serviceName); err != nil {
		panic(err)
	}
	return s.publish(s.scope, s.publisher, serviceName, knownMessages, options...)
}

// PublishIn is like Publish, but with specified scope which should be
// a subset of the scope of Server s.
func (s *Server) PublishIn(scope Scope, serviceName string, knownMessages []KnownMessage, options ...ServiceOption) error {
	if err := checkNameConvention(serviceName); err != nil {
		panic(err)
	}
	return s.publish(scope, s.publisher, serviceName, knownMessages, options...)
}

// Serve starts serving.
func (s *Server) Serve() error {
	if !s.initialized {
		if err := s.init(); err != nil {
			return err
		}
	}
	defer s.doClose()
	s.lg.Infof("server in serve")
	for e := range s.errRecovers {
		if (e == noError{}) {
			break
		}
		if e.Recover() {
			s.lg.Infof("error recovered: %s : %v", e.String(), e.Error())
		} else {
			s.lg.Errorf("error not recovered: %s : %v", e.String(), e.Error())
			return e.Error()
		}
	}
	return nil
}

func (s *Server) addCloser(closer closer) {
	s.closers = append(s.closers, closer)
}

// Close triggers the close procedure of the server.
func (s *Server) Close() {
	if s.closers == nil {
		return
	}
	mTraceHelper.tryWait()
	s.errRecovers <- noError{}
}

// CloseWait triggers the close procedure and waits
// until the server is fully closed.
func (s *Server) CloseWait() {
	s.Close()
	<-s.closed
}

func (s *Server) close() {
	if s.closers == nil {
		return
	}
	mTraceHelper.tryWait()
	s.errRecovers <- unrecoverableError{ErrServerClosed}
}

func (s *Server) doClose() {
	s.Lock()
	defer s.Unlock()
	if s.closers == nil {
		return
	}
	for _, closer := range s.closers {
		closer.close()
	}
	s.lg.Infof("server closed")
	s.closers = nil
	close(s.closed)
}
