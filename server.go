package adaptiveservice

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"reflect"
	"strings"
)

// Server provides services.
type Server struct {
	*conf
	publisher        string
	providerID       string
	bcastPort        string
	rootRegistryPort string
	reverseProxy     bool
	serviceLister    bool
	errRecovers      chan errorRecover
	mq               *msgQ
	qWeight          int
	qScale           int
	msgTypeCheck     bool
	closers          []closer
	initialized      bool
}

// NewServer creates a server which publishes services.
func NewServer(options ...Option) *Server {
	s := &Server{
		conf:         newConf(),
		publisher:    "default.org",
		errRecovers:  make(chan errorRecover),
		qWeight:      8,
		qScale:       8,
		msgTypeCheck: true,
	}

	for _, o := range options {
		o(s.conf)
	}

	s.lg.Debugf("new server created")
	return s
}

// TODO: use mac to generate fixed id
func genID() string {
	b := make([]byte, 6)
	rand.Read(b)
	id := hex.EncodeToString(b)
	return id
}

func (s *Server) init() {
	if s.initialized {
		return
	}
	s.initialized = true
	initSigCleaner(s.lg)
	addSigCloser(s)
	s.mq = newMsgQ(s.qWeight, s.qScale, s.lg)
	s.addCloser(s.mq)

	if s.scope&ScopeLAN == ScopeLAN || s.scope&ScopeWAN == ScopeWAN {
		if id, err := discoverProviderID(s.lg); err != nil {
			if len(s.providerID) != 0 {
				if err := s.publishProviderInfoService(); err != nil {
					panic(err)
				}
				s.lg.Infof("user specified provider ID: %s, provider info service started", s.providerID)
			} else {
				s.providerID = genID()
				s.lg.Infof("provider ID not found, using new generated ID: %s", s.providerID)
			}
		} else {
			if len(s.providerID) != 0 && id != s.providerID {
				panic(fmt.Sprintf("conflict provider ID: %s => %s ?", id, s.providerID))
			}
			s.providerID = id
			s.lg.Infof("discovered provider ID: %s", s.providerID)
		}
	}

	if s.scope&ScopeLAN == ScopeLAN {
		s.lg.Infof("configing server in local network scope")
		func() {
			c := NewClient(WithScope(ScopeProcess|ScopeOS), WithLogger(s.lg)).SetDiscoverTimeout(0)
			conn := <-c.Discover(BuiltinPublisher, "LANRegistry")
			if conn != nil {
				conn.Close()
				s.lg.Infof("LAN registry running")
				return
			}

			if len(s.bcastPort) != 0 {
				if err := s.publishLANRegistryService(); err != nil {
					panic(err)
				}
				s.lg.Infof("user specified broadcast port: %s, LAN registry service started", s.bcastPort)
			} else {
				s.lg.Warnf("LAN registry not configured")
			}
		}()
	}

	if s.scope&ScopeWAN == ScopeWAN {
		s.lg.Infof("configing server in public network scope")
		if addr, err := discoverRegistryAddr(s.lg); err != nil {
			if len(s.registryAddr) != 0 {
				if err := s.publishRegistryInfoService(); err != nil {
					panic(err)
				}
				s.lg.Infof("user specified registry address: %s, registry info service started", s.registryAddr)
			} else {
				s.lg.Warnf("registry address not found: %s", err)
			}
		} else {
			if len(s.registryAddr) != 0 && addr != s.registryAddr {
				panic(fmt.Sprintf("conflict registry address: %s => %s ?", addr, s.registryAddr))
			}
			s.registryAddr = addr
			s.lg.Infof("discovered registry address: %s", addr)
		}
	}

	if len(s.rootRegistryPort) != 0 {
		if s.scope&ScopeWAN != ScopeWAN {
			panic("scope error")
		}
		if err := s.startRootRegistry(); err != nil {
			panic(err)
		}
		s.lg.Infof("root registry started at %s", s.rootRegistryPort)
	}

	if s.reverseProxy {
		scope := ScopeLAN
		if len(s.rootRegistryPort) != 0 {
			scope |= ScopeWAN
		}
		if s.scope&scope != scope {
			panic("scope error")
		}
		if err := s.publishReverseProxyService(scope); err != nil {
			panic(err)
		}
		s.lg.Infof("reverse proxy started")
	}
	s.lg.Debugf("server initialized")
}

type service struct {
	publisherName     string
	serviceName       string
	providerID        string
	knownMsgTypes     map[reflect.Type]struct{}
	s                 *Server
	scope             Scope
	fnOnNewStream     func(Context)
	fnOnNewConnection func(net.Conn) bool
}

func (svc *service) canHandle(msg interface{}) bool {
	if svc.s.msgTypeCheck {
		_, has := svc.knownMsgTypes[reflect.TypeOf(msg)]
		return has
	}
	return true
}

func (s *Server) publish(scope Scope, publisherName, serviceName string, knownMessages []KnownMessage, options ...ServiceOption) error {
	s.lg.Debugf("publishing %s %s in scope %b", publisherName, serviceName, scope)
	newService := func() *service {
		svc := &service{
			publisherName: publisherName,
			serviceName:   serviceName,
			providerID:    s.providerID,
			knownMsgTypes: make(map[reflect.Type]struct{}),
			s:             s,
			scope:         scope,
		}

		for _, msg := range knownMessages {
			tp := reflect.TypeOf(msg)
			svc.knownMsgTypes[tp] = struct{}{}
		}
		return svc
	}

	if scope > s.scope {
		panic("publishing in larger scope than allowed")
	}
	//s.once.Do(s.init)
	s.init()

	svc := newService()
	for _, opt := range options {
		opt(svc)
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

// Publish publishes service to all available scopes.
// knownMessages are messages that the service can handle, e.g.
// []KnownMessage{(*PublicStructA)(nil), (*PublicStructB)(nil), ...},
// where (*PublicStructA) and (*PublicStructB) are the known messages that
// have Handle(stream ContextStream) (reply interface{}, err error) method.
func (s *Server) Publish(serviceName string, knownMessages []KnownMessage, options ...ServiceOption) error {
	if strings.ContainsAny(serviceName, "_/") {
		panic("serviceName should not contain _ or /")
	}
	return s.publish(s.scope, s.publisher, serviceName, knownMessages, options...)
}

// Serve starts serving.
func (s *Server) Serve() error {
	s.lg.Infof("server in serve")
	for e := range s.errRecovers {
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

// Close closes the server.
func (s *Server) Close() {
	s.close()
}

func (s *Server) close() {
	for _, closer := range s.closers {
		closer.close()
	}
}
