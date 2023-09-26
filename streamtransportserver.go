package adaptiveservice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/niubaoshu/gotiny"
)

type streamTransport struct {
	svc              *service
	closed           chan struct{}
	lnr              net.Listener
	reverseProxyConn Connection
	chanNetConn      chan net.Conn
}

func makeStreamTransport(svc *service, lnr net.Listener) *streamTransport {
	return &streamTransport{
		svc:         svc,
		closed:      make(chan struct{}),
		lnr:         lnr,
		chanNetConn: make(chan net.Conn, 8),
	}
}

func (svc *service) newUDSTransport() (*streamTransport, error) {
	addr := toUDSAddr(svc.publisherName, svc.serviceName)
	lnr, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}

	st := makeStreamTransport(svc, lnr)
	go st.receiver()
	svc.s.lg.Infof("service %s %s listening on %s", svc.publisherName, svc.serviceName, addr)
	return st, nil
}

func connectReverseProxy(svc *service) Connection {
	c := NewClient(WithScope(ScopeLAN|ScopeWAN),
		WithLogger(svc.s.lg),
		WithRegistryAddr(svc.s.registryAddr),
		WithProviderID(svc.s.providerID),
	).SetDiscoverTimeout(3)
	connChan := c.Discover(BuiltinPublisher, SrvReverseProxy, "*")
	defer func() {
		for conn := range connChan {
			conn.Close()
		}
	}()
	for conn := range connChan {
		err := conn.SendRecv(&proxyRegServiceInWAN{svc.publisherName, svc.serviceName, svc.s.providerID}, nil)
		if err == nil {
			return conn
		}
		conn.Close()
	}
	return nil
}

func (svc *service) newTCPTransport(onPort string) (*streamTransport, error) {
	if len(onPort) == 0 {
		onPort = "0"
	}
	lnr, err := net.Listen("tcp", ":"+onPort)
	if err != nil {
		return nil, err
	}

	st := makeStreamTransport(svc, lnr)
	go st.receiver()
	addr := lnr.Addr().String()
	_, port, _ := net.SplitHostPort(addr) // from [::]:43807
	svc.s.lg.Infof("service %s %s listening on %s", svc.publisherName, svc.serviceName, addr)

	if svc.scope&ScopeLAN == ScopeLAN {
		if err := svc.regServiceLAN(port); err != nil {
			svc.s.lg.Warnf("service %s %s register to LAN failed: %v", svc.publisherName, svc.serviceName, err)
			st.close()
			return nil, err
		}
		svc.s.lg.Infof("service %s %s registered to LAN", svc.publisherName, svc.serviceName)
	}

	if svc.scope&ScopeWAN == ScopeWAN {
		if err := svc.regServiceWAN(port); err != nil {
			svc.s.lg.Infof("service %s %s can not register to WAN directly: %v", svc.publisherName, svc.serviceName, err)
			st.reverseProxyConn = connectReverseProxy(svc)
			if st.reverseProxyConn == nil {
				svc.s.lg.Warnf("service %s %s register to proxy failed", svc.publisherName, svc.serviceName)
				st.close()
				return nil, err
			}
			svc.s.lg.Infof("reverse proxy connected")
			go st.reverseReceiver()
		}
		svc.s.lg.Infof("service %s %s registered to WAN", svc.publisherName, svc.serviceName)
	}
	return st, nil
}

func (st *streamTransport) close() {
	closed := st.closed
	if st.closed == nil {
		return
	}
	st.closed = nil
	close(closed)
	svc := st.svc
	svc.s.lg.Debugf("stream transport %s closing", st.lnr.Addr().String())
	st.lnr.Close()
	if st.reverseProxyConn != nil {
		st.reverseProxyConn.Close()
	}
	if st.lnr.Addr().Network() == "unix" {
		return
	}
	if svc.scope&ScopeLAN == ScopeLAN {
		if err := svc.delServiceLAN(); err != nil {
			svc.s.lg.Warnf("del service in lan failed: %v", err)
		}
	}
	if svc.scope&ScopeWAN == ScopeWAN {
		if err := svc.delServiceWAN(); err != nil {
			svc.s.lg.Warnf("del service in wan failed: %v", err)
		}
	}
}

type streamTransportMsg struct {
	chanID    uint64 // client stream channel ID
	msg       interface{}
	tracingID uuidInfoPtr
}

type streamServerStream struct {
	Context
	mtx         *sync.Mutex
	svcInfo     *serviceInfo
	lg          Logger
	netconn     net.Conn
	connClose   *chan struct{}
	privateChan chan *metaMsg // dedicated to the client
	chanID      uint64        // client stream channel ID, taken from transport msg
	enc         *gotiny.Encoder
	encMainCopy int32
	timeouter
}

func (ss *streamServerStream) GetNetconn() Netconn {
	return ss.netconn
}

func (ss *streamServerStream) Close() {}

func (ss *streamServerStream) send(tm *streamTransportMsg) error {
	buf := net.Buffers{}
	mainCopy := false
	if atomic.CompareAndSwapInt32(&ss.encMainCopy, 0, 1) {
		ss.lg.Debugf("enc main copy")
		mainCopy = true
	}
	enc := ss.enc
	if !mainCopy {
		enc = enc.Copy()
	}
	bufMsg := enc.Encode(tm)
	bufSize := make([]byte, 4)
	binary.BigEndian.PutUint32(bufSize, uint32(len(bufMsg)))
	buf = append(buf, bufSize, bufMsg)
	ss.lg.Debugf("stream server send: tm: %#v", tm)
	ss.mtx.Lock()
	defer func() {
		if mainCopy {
			atomic.StoreInt32(&ss.encMainCopy, 0)
		}
		ss.mtx.Unlock()
	}()
	if _, err := buf.WriteTo(ss.netconn); err != nil {
		return err
	}
	return nil
}

func (ss *streamServerStream) Send(msg interface{}) error {
	if *ss.connClose == nil {
		return io.EOF
	}
	tracingID := getTracingID(msg)
	tm := streamTransportMsg{chanID: ss.chanID, msg: msg, tracingID: tracingID}
	if err := ss.send(&tm); err != nil {
		return err
	}

	if tracingID != nil {
		tag := fmt.Sprintf("%s/%s@%s send", ss.svcInfo.publisherName, ss.svcInfo.serviceName, ss.svcInfo.providerID)
		if err := traceMsg(msg, tracingID, tag, ss.netconn); err != nil {
			ss.lg.Warnf("message tracing on server send error: %v", err)
		}
	}
	return nil
}

func (ss *streamServerStream) Recv(msgPtr interface{}) (err error) {
	connClose := *ss.connClose
	if *ss.connClose == nil {
		return io.EOF
	}
	rptr := reflect.ValueOf(msgPtr)
	if msgPtr != nil && (rptr.Kind() != reflect.Ptr || rptr.IsNil()) {
		panic("not a pointer or nil pointer")
	}

	select {
	case <-connClose:
		return io.EOF
	case <-ss.timeouter.timeoutChan():
		return ErrRecvTimeout
	case mm := <-ss.privateChan:
		msg := mm.msg
		if mm.tracingID != nil {
			tag := fmt.Sprintf("%s/%s@%s recv", ss.svcInfo.publisherName, ss.svcInfo.serviceName, ss.svcInfo.providerID)
			if err := traceMsg(msg, mm.tracingID, tag, ss.netconn); err != nil {
				ss.lg.Warnf("message tracing on server recv error: %v", err)
			}
		}
		getRoutineLocal().tracingID = mm.tracingID

		if err, ok := msg.(error); ok {
			return err
		}
		if msgPtr == nil { // msgPtr is nil
			return nil // user just looks at error, no error here
		}

		rv := rptr.Elem()
		mrv := reflect.ValueOf(msg)
		if rv.Kind() != reflect.Ptr && mrv.Kind() == reflect.Ptr {
			mrv = mrv.Elem()
		}
		defer func() {
			if e := recover(); e != nil {
				err = fmt.Errorf("message type mismatch: %v", e)
			}
		}()
		rv.Set(mrv)
	}

	return
}

func (ss *streamServerStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := ss.Send(msgSnd); err != nil {
		return err
	}
	if err := ss.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}

func (st *streamTransport) reverseReceiver() {
	lg := st.svc.s.lg
	cmdConn := st.reverseProxyConn
	defer cmdConn.Close()
	conn := cmdConn.(*streamConnection)
	host, _, _ := net.SplitHostPort(conn.netconn.RemoteAddr().String()) // from [::]:43807

	for {
		var port string
		if err := cmdConn.Recv(&port); err != nil {
			lg.Warnf("reverseReceiver: cmd connection broken: %v", err)
			break
		}
		lg.Debugf("reverseReceiver: new reverse connection request")
		addr := host + ":" + port
		netconn, err := net.Dial("tcp", addr)
		if err != nil {
			lg.Warnf("reverseReceiver: reverse connection failed: %v", err)
			break
		}
		st.chanNetConn <- netconn
	}
	lg.Infof("reverse proxy lost, reconnecting")
	st.reverseProxyConn = connectReverseProxy(st.svc)
	if st.reverseProxyConn == nil {
		lg.Errorf("service %s %s lost connection to reverse proxy", st.svc.publisherName, st.svc.serviceName)
		return
	}
	lg.Infof("reverse proxy reconnected")
	go st.reverseReceiver()
}

func (st *streamTransport) receiver() {
	svc := st.svc
	lnr := st.lnr
	lg := svc.s.lg
	mq := svc.s.mq
	svcInfo := &serviceInfo{svc.providerID, svc.publisherName, svc.serviceName}

	go func() {
		rootRegistryIP, _, _ := net.SplitHostPort(svc.s.registryAddr)
		pinged := false
		if lnr.Addr().Network() == "unix" {
			pinged = true
		}
		if svc.publisherName == BuiltinPublisher && svc.serviceName == "rootRegistry" {
			pinged = true
		}
		for {
			netconn, err := lnr.Accept()
			if err != nil {
				lg.Debugf("stream transport listener: %v", err)
				// the streamTransport has been closed
				if st.closed == nil {
					return
				}
				continue
			}
			if !pinged {
				host, _, _ := net.SplitHostPort(netconn.RemoteAddr().String())
				if host == rootRegistryIP {
					netconn.Close()
					pinged = true
					lg.Debugf("ping from root registry")
					continue
				}
			}
			st.chanNetConn <- netconn
		}
	}()

	handleConn := func(netconn net.Conn) {
		lg.Debugf("%s %s new stream connection from: %s", svc.publisherName, svc.serviceName, netconn.RemoteAddr().String())
		if svc.fnOnConnect != nil {
			lg.Debugf("%s %s on connect", svc.publisherName, svc.serviceName)
			if svc.fnOnConnect(netconn) {
				return
			}
		}

		connClose := make(chan struct{})
		defer func() {
			if svc.fnOnDisconnect != nil {
				lg.Debugf("%s %s on disconnect", svc.publisherName, svc.serviceName)
				svc.fnOnDisconnect(netconn)
			}
			lg.Debugf("%s %s stream connection disconnected: %s", svc.publisherName, svc.serviceName, netconn.RemoteAddr().String())
			close(connClose)
			connClose = nil
			netconn.Close()
		}()
		var mtx sync.Mutex
		ssMap := make(map[uint64]*streamServerStream)
		dec := gotiny.NewDecoderWithPtr((*streamTransportMsg)(nil))
		dec.SetCopyMode()
		bufSize := make([]byte, 4)
		bufMsg := make([]byte, 512)
		for {
			if st.closed == nil {
				return
			}
			if _, err := io.ReadFull(netconn, bufSize); err != nil {
				if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "use of closed network connection") {
					lg.Debugf("stream sever receiver: connection closed: %v", err)
				} else {
					lg.Warnf("stream sever receiver: from %s read size error: %v", netconn.RemoteAddr().String(), err)
				}
				return
			}

			size := binary.BigEndian.Uint32(bufSize)
			bufCap := uint32(cap(bufMsg))
			if size <= bufCap {
				bufMsg = bufMsg[:size]
			} else {
				bufMsg = make([]byte, size)
			}
			if _, err := io.ReadFull(netconn, bufMsg); err != nil {
				lg.Warnf("stream sever receiver: from %s read buf error: %v", netconn.RemoteAddr().String(), err)
				return
			}

			var decErr error
			var tm streamTransportMsg
			func() {
				defer func() {
					if e := recover(); e != nil {
						decErr = fmt.Errorf("unknown message: %v", e)
						lg.Errorf("%v", decErr)
						tm.msg = decErr
					}
				}()
				dec.Decode(bufMsg, &tm)
			}()
			lg.Debugf("stream server receiver: tm: %#v", &tm)

			ss := ssMap[tm.chanID]
			if ss == nil {
				ss = &streamServerStream{
					Context:     &contextImpl{},
					mtx:         &mtx,
					svcInfo:     svcInfo,
					lg:          lg,
					netconn:     netconn,
					connClose:   &connClose,
					privateChan: make(chan *metaMsg, svc.s.qsize),
					chanID:      tm.chanID,
					enc:         gotiny.NewEncoderWithPtr((*streamTransportMsg)(nil)),
				}
				ssMap[tm.chanID] = ss
				if svc.fnOnNewStream != nil {
					lg.Debugf("%s %s on new stream %v", svc.publisherName, svc.serviceName, tm.chanID)
					svc.fnOnNewStream(ss)
				}
			}

			if _, ok := tm.msg.(streamCloseMsg); ok { // check if stream close was sent
				if svc.fnOnStreamClose != nil {
					svc.fnOnStreamClose(ss)
					lg.Debugf("%s %s on stream %v close", svc.publisherName, svc.serviceName, tm.chanID)
				}
				delete(ssMap, tm.chanID)
				continue
			}

			if decErr != nil {
				if err := ss.Send(decErr); err != nil {
					lg.Errorf("send decode error failed: %v", err)
				}
				continue
			}

			msg := tm.msg
			tracingID := tm.tracingID
			if svc.canHandle(msg) {
				mm := &metaKnownMsg{
					stream:    ss,
					msg:       msg.(KnownMessage),
					tracingID: tracingID,
					svcInfo:   svcInfo,
				}
				lg.Debugf("stream enqueue message <%#v>", mm.msg)
				mq.putMetaMsg(mm)
			} else {
				ss.privateChan <- &metaMsg{msg, tracingID}
			}
		}
	}

	defer func() {
		for netconn := range st.chanNetConn {
			netconn.Close()
		}
	}()

	closed := st.closed
	for {
		select {
		case <-closed:
			return
		case netconn := <-st.chanNetConn:
			go handleConn(netconn)
		}
	}
}
