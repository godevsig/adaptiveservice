package adaptiveservice

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
)

// Server provides services.
type Server struct {
	*conf
	publisher        string
	providerID       string
	bcastPort        string
	rootRegistryPort string
	//sync.Mutex
	//msgType2SrvName  map[reflect.Type]string
	//srvName2MsgTypes map[string][]reflect.Type
	errRecovers  chan errorRecover
	mq           *msgQ
	qWeight      int
	qScale       int
	msgTypeCheck bool
	closers      []closer
}

// NewServer creates a server which publishes services.
func NewServer(options ...Option) *Server {
	s := &Server{
		conf:      newConf(),
		publisher: "default.org",
		//msgType2SrvName:  make(map[reflect.Type]string),
		//srvName2MsgTypes: make(map[string][]reflect.Type),
		errRecovers:  make(chan errorRecover),
		qWeight:      8,
		qScale:       8,
		msgTypeCheck: true,
	}

	for _, o := range options {
		o(s.conf)
	}

	s.lg.Debugln("new server created")
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
	initSigCleaner(s.lg)
	addSigCloser(s)
	s.mq = newMsgQ(s.qWeight, s.qScale, s.lg)
	s.addCloser(s.mq)

	if s.scope&ScopeLAN == ScopeLAN || s.scope&ScopeWAN == ScopeWAN {
		if id, err := discoverProviderID(); err != nil {
			if len(s.providerID) != 0 {
				if err := s.publishProviderInfoService(); err != nil {
					panic(err)
				}
				s.lg.Infof("user specified provider ID: %s, run provider info service", s.providerID)
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
		s.lg.Infoln("configing server in local network scope")
		func() {
			c := NewClient(WithScope(ScopeProcess | ScopeOS)).SetDiscoverTimeout(0)
			defer c.Close()

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
				s.lg.Infof("user specified broadcast port : %s, run LAN registry service", s.bcastPort)
			} else {
				s.lg.Warnf("LAN registry not configured")
			}
		}()
	}

	if s.scope&ScopeWAN == ScopeWAN {
		s.lg.Infoln("configing server in public network scope")
		if len(s.rootRegistryPort) != 0 {
			if err := s.runRootRegistry(); err != nil {
				panic(err)
			}
		}
		if addr, err := discoverRegistryAddr(); err != nil {
			if len(s.registryAddr) != 0 {
				if err := s.publishRegistryInfoService(); err != nil {
					panic(err)
				}
				s.lg.Infof("user specified registry address: %s, run registry info service", s.registryAddr)
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
	s.lg.Debugln("server initialized")
}

/*
var codingLock sync.Mutex

func (s *Server) regKnownMessage(name string, msg KnownMessage) {
	if s.scope > scopeProcess {
		codingLock.Lock()
		gotiny.Register(msg)
		codingLock.Unlock()
	}

	tp := reflect.TypeOf(msg)
	s.Lock()
	if _, has := s.msgType2SrvName[tp]; has {
		panic(fmt.Sprintf("registering duplicated type %v for %s", tp, name))
	}
	s.msgType2SrvName[tp] = name
	tps := s.srvName2MsgTypes[name]
	s.srvName2MsgTypes[name] = append(tps, tp)
	s.Unlock()
}
*/

type service struct {
	publisherName string
	serviceName   string
	knownMsgTypes map[reflect.Type]struct{}
	s             *Server
	scope         Scope
	fnOnNewStream OnNewStream
}

func (svc *service) canHandle(msg interface{}) bool {
	if svc.s.msgTypeCheck {
		_, has := svc.knownMsgTypes[reflect.TypeOf(msg)]
		return has
	}
	return true
}

// OnNewStream is called to initialize the context
// when new incoming stream is accepted.
type OnNewStream func(ctx Context) error

// ServiceOption is option for service.
type ServiceOption func(*service)

// WithOnNewStreamFunc sets OnNewStream function.
func WithOnNewStreamFunc(fn OnNewStream) ServiceOption {
	return func(svc *service) {
		svc.fnOnNewStream = fn
	}
}

func (s *Server) publish(scope Scope, publisherName, serviceName string, knownMessages []KnownMessage, options ...ServiceOption) error {
	newService := func() *service {
		svc := &service{
			publisherName: publisherName,
			serviceName:   serviceName,
			knownMsgTypes: make(map[reflect.Type]struct{}),
			s:             s,
			scope:         scope,
		}

		for _, msg := range knownMessages {
			/*
				if s.scope&ScopeProcess != ScopeProcess {
					codingLock.Lock()
					gotiny.Register(msg)
					codingLock.Unlock()
				}
			*/
			tp := reflect.TypeOf(msg)
			svc.knownMsgTypes[tp] = struct{}{}
		}
		return svc
	}

	if scope > s.scope {
		panic("publishing in larger scope than allowed")
	}
	s.once.Do(s.init)

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
	return s.publish(s.scope, s.publisher, serviceName, knownMessages, options...)
}

// Serve starts serving.
func (s *Server) Serve() error {
	for e := range s.errRecovers {
		if e.Recover() {
			s.lg.Infoln("error recovered:", e.String(), ":", e.Error())
		} else {
			s.lg.Errorln("error not recovered:", e.String(), ":", e.Error())
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
