package adaptiveservice

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

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
	addrs := lookupServiceUDS(svc.publisherName, svc.serviceName)
	if len(addrs) != 0 {
		return nil, fmt.Errorf("socket already exists: %v", addrs)
	}
	addr := addrUDS(svc.publisherName, svc.serviceName)
	lnr, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}

	st := makeStreamTransport(svc, lnr)
	go st.receiver()
	svc.s.lg.Infof("service %s %s listening on %s", svc.publisherName, svc.serviceName, addr)
	return st, nil
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
			c := NewClient(WithScope(ScopeLAN|ScopeWAN),
				WithLogger(svc.s.lg),
				WithRegistryAddr(svc.s.registryAddr),
				WithProviderID(svc.s.providerID),
			).SetDiscoverTimeout(0)
			connChan := c.Discover(BuiltinPublisher, "reverseProxy", "*")
			for conn := range connChan {
				err := conn.SendRecv(&proxyRegServiceInWAN{svc.publisherName, svc.serviceName, svc.s.providerID}, nil)
				if err == nil {
					svc.s.lg.Infof("reverse proxy connected")
					st.reverseProxyConn = conn
					go st.reverseReceiver()
					break
				}
				conn.Close()
			}
			for conn := range connChan {
				conn.Close()
			}

			if st.reverseProxyConn == nil {
				svc.s.lg.Warnf("service %s %s register to proxy failed", svc.publisherName, svc.serviceName)
				st.close()
				return nil, err
			}
		}
		svc.s.lg.Infof("service %s %s registered to WAN", svc.publisherName, svc.serviceName)
	}
	return st, nil
}

func (st *streamTransport) close() {
	if st.closed != nil {
		st.svc.s.lg.Debugf("stream transport %s closing", st.lnr.Addr().String())
		st.lnr.Close()
		if st.reverseProxyConn != nil {
			st.reverseProxyConn.Close()
		}
		close(st.closed)
		st.closed = nil
	}
}

type streamTransportMsg struct {
	srcChan uint64
	dstChan uint64
	msg     interface{}
}

type streamServerStream struct {
	Context
	mtx         *sync.Mutex
	lg          Logger
	netconn     net.Conn
	connClose   chan struct{}
	privateChan chan *streamTransportMsg // dedicated to the client
	qsize       int
	dstChan     uint64
	enc         *gotiny.Encoder
	encMainCopy int32
	rbuff       []byte
}

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
	ss.lg.Debugf("stream server send: tm: %#v ==> size %d, buf %v <%s>", tm, len(bufMsg), bufMsg, bufMsg)
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

func (ss *streamServerStream) sendNoPrivate(msg interface{}) error {
	if ss.connClose == nil {
		return io.EOF
	}
	tm := streamTransportMsg{dstChan: ss.dstChan, msg: msg}
	return ss.send(&tm)
}

func (ss *streamServerStream) Send(msg interface{}) error {
	if ss.connClose == nil {
		return io.EOF
	}
	srcChan := uint64(uintptr(unsafe.Pointer(&ss.privateChan)))
	tm := streamTransportMsg{srcChan: srcChan, dstChan: ss.dstChan, msg: msg}
	return ss.send(&tm)
}

func (ss *streamServerStream) Recv(msgPtr interface{}) error {
	if ss.connClose == nil {
		return io.EOF
	}
	rptr := reflect.ValueOf(msgPtr)
	if rptr.Kind() != reflect.Ptr || rptr.IsNil() {
		panic("not a pointer or nil pointer")
	}

	rv := rptr.Elem()
	select {
	case <-ss.connClose:
		ss.connClose = nil
		return io.EOF
	case tm := <-ss.privateChan:
		ss.lg.Debugf("stream server recv: tm: %#v", tm)
		mrv := reflect.ValueOf(tm.msg)
		if rv.Kind() != reflect.Ptr && mrv.Kind() == reflect.Ptr {
			mrv = mrv.Elem()
		}
		rv.Set(mrv)
	}

	return nil
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

func (ss *streamServerStream) Write(p []byte) (n int, err error) {
	if err := ss.Send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (ss *streamServerStream) Read(p []byte) (n int, err error) {
	if len(ss.rbuff) == 0 {
		if err := ss.Recv(&ss.rbuff); err != nil {
			return 0, err
		}
	}
	n = copy(p, ss.rbuff)
	ss.rbuff = ss.rbuff[n:]
	return
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
}

func (st *streamTransport) receiver() {
	lg := st.svc.s.lg
	mq := st.svc.s.mq
	lnr := st.lnr

	go func() {
		rootRegistryIP, _, _ := net.SplitHostPort(st.svc.s.registryAddr)
		pinged := false
		if lnr.Addr().Network() == "unix" {
			pinged = true
		}
		if st.svc.publisherName == BuiltinPublisher && st.svc.serviceName == "rootRegistry" {
			pinged = true
		}
		for {
			netconn, err := lnr.Accept()
			if err != nil {
				lg.Debugf("stream transport listener closed: %v", err)
				return
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
		lg.Debugf("%s %s new stream connection from: %s", st.svc.publisherName, st.svc.serviceName, netconn.RemoteAddr().String())
		if st.svc.fnOnConnect != nil {
			lg.Debugf("%s %s on connect", st.svc.publisherName, st.svc.serviceName)
			if st.svc.fnOnConnect(netconn) {
				return
			}
		}

		connClose := make(chan struct{})
		defer func() {
			if st.svc.fnOnDisconnect != nil {
				lg.Debugf("%s %s on disconnect", st.svc.publisherName, st.svc.serviceName)
				st.svc.fnOnDisconnect(netconn)
			}
			lg.Debugf("%s %s stream connection disconnected: %s", st.svc.publisherName, st.svc.serviceName, netconn.RemoteAddr().String())
			close(connClose)
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
				lg.Warnf("stream sever receiver: from %s read size error: %v", netconn.RemoteAddr().String(), err)
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
			lg.Debugf("stream server receiver: size: %d, bufMsg: %v <%s>", size, bufMsg, bufMsg)

			var tm streamTransportMsg
			func() {
				defer func() {
					if err := recover(); err != nil {
						lg.Errorf("unknown message: %v", err)
					}
				}()
				dec.Decode(bufMsg, &tm)
			}()
			lg.Debugf("stream server receiver: tm src 0x%x, dst 0x%x", tm.srcChan, tm.dstChan)

			ss := ssMap[tm.srcChan]
			if ss == nil {
				qsize := st.svc.s.qsize
				ss = &streamServerStream{
					Context:     &contextImpl{},
					mtx:         &mtx,
					lg:          lg,
					netconn:     netconn,
					connClose:   connClose,
					privateChan: make(chan *streamTransportMsg, qsize),
					qsize:       qsize,
					dstChan:     tm.srcChan,
					enc:         gotiny.NewEncoderWithPtr((*streamTransportMsg)(nil)),
				}
				ssMap[tm.srcChan] = ss
				if st.svc.fnOnNewStream != nil {
					lg.Debugf("%s %s on new stream", st.svc.publisherName, st.svc.serviceName)
					st.svc.fnOnNewStream(ss)
				}
			}

			if st.svc.canHandle(tm.msg) {
				mm := &metaKnownMsg{
					stream: ss,
					msg:    tm.msg.(KnownMessage),
				}
				mq.putMetaMsg(mm)
			} else if tm.dstChan != 0 {
				func() {
					defer func() {
						if err := recover(); err != nil {
							lg.Errorf("broken private chan: %v", err)
						}
					}()
					privateChan := *(*chan *streamTransportMsg)(unsafe.Pointer(uintptr(tm.dstChan)))
					privateChan <- &streamTransportMsg{msg: tm.msg}
				}()
				lg.Debugf("sent to private channel of msg handler")
			} else {
				if err := ss.Send(ErrBadMessage); err != nil {
					lg.Errorf("send ErrBadMessage failed: %v", err)
				}
			}
		}
	}

	for {
		select {
		case <-st.closed:
			return
		case netconn := <-st.chanNetConn:
			go handleConn(netconn)
		}
	}
}

// below for client side

type streamClientStream struct {
	conn        *streamConnection
	selfChan    chan *streamTransportMsg
	dstChan     uint64
	encMainCopy int32
	enc         *gotiny.Encoder
	rbuff       []byte
}

// stream connection for client.
type streamConnection struct {
	Stream
	sync.Mutex
	owner   *Client
	netconn net.Conn
	closed  chan struct{}
}

func (c *Client) newStreamConnection(network string, addr string) (*streamConnection, error) {
	proxied := false
	if addr[len(addr)-1] == 'P' {
		c.lg.Debugf("%s is proxied", addr)
		addr = addr[:len(addr)-1]
		proxied = true
	}
	netconn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	if proxied {
		if _, err := netconn.Read([]byte{0}); err != nil {
			return nil, err
		}
	}
	c.lg.Debugf("stream connection established: %s -> %s", netconn.LocalAddr().String(), addr)

	conn := &streamConnection{
		owner:   c,
		netconn: netconn,
		closed:  make(chan struct{}),
	}

	conn.Stream = conn.NewStream()
	go conn.receiver()
	return conn, nil
}

func (c *Client) newUDSConnection(addr string) (*streamConnection, error) {
	return c.newStreamConnection("unix", addr)
}
func (c *Client) newTCPConnection(addr string) (*streamConnection, error) {
	return c.newStreamConnection("tcp", addr)
}

func (conn *streamConnection) receiver() {
	defer close(conn.closed)
	lg := conn.owner.lg
	netconn := conn.netconn
	dec := gotiny.NewDecoderWithPtr((*streamTransportMsg)(nil))
	dec.SetCopyMode()
	bufSize := make([]byte, 4)
	bufMsg := make([]byte, 512)
	for {
		if _, err := io.ReadFull(netconn, bufSize); err != nil {
			lg.Warnf("stream client receiver: read size error: %v", err)
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
			lg.Warnf("stream client receiver: read buf error: %v", err)
			return
		}
		lg.Debugf("stream client receiver: size: %d, bufMsg: %v <%s>", size, bufMsg, bufMsg)

		var tm streamTransportMsg
		//escapes(&tm)
		func() {
			defer func() {
				if err := recover(); err != nil {
					lg.Errorf("unknown message: %v", err)
				}
			}()
			dec.Decode(bufMsg, &tm)
		}()
		lg.Debugf("stream client receiver: tm src 0x%x, dst 0x%x", tm.srcChan, tm.dstChan)

		if tm.dstChan != 0 {
			func() {
				defer func() {
					if err := recover(); err != nil {
						lg.Errorf("broken stream chan: %v", err)
					}
				}()
				streamChan := *(*chan *streamTransportMsg)(unsafe.Pointer(uintptr(tm.dstChan)))
				// swap src and dst
				streamChan <- &streamTransportMsg{srcChan: tm.dstChan, dstChan: tm.srcChan, msg: tm.msg}
			}()
		} else {
			panic("destination channel not specified")
		}
	}
}

func (conn *streamConnection) NewStream() Stream {
	return &streamClientStream{
		conn:     conn,
		selfChan: make(chan *streamTransportMsg, conn.owner.qsize),
		enc:      gotiny.NewEncoderWithPtr((*streamTransportMsg)(nil)),
	}
}
func (conn *streamConnection) Close() {
	conn.netconn.Close()
}

func (cs *streamClientStream) Send(msg interface{}) error {
	if cs.selfChan == nil {
		return io.EOF
	}
	if _, ok := msg.([]byte); !ok {
		if _, ok := msg.(KnownMessage); !ok {
			if cs.dstChan == 0 {
				return ErrBadMessage
			}
		}
	}

	srcChan := uint64(uintptr(unsafe.Pointer(&cs.selfChan)))
	tm := streamTransportMsg{srcChan: srcChan, dstChan: cs.dstChan, msg: msg}

	lg := cs.conn.owner.lg
	buf := net.Buffers{}
	mainCopy := false
	if atomic.CompareAndSwapInt32(&cs.encMainCopy, 0, 1) {
		lg.Debugf("enc main copy")
		mainCopy = true
	}
	enc := cs.enc
	if !mainCopy {
		enc = enc.Copy()
	}
	bufMsg := enc.Encode(&tm)
	bufSize := make([]byte, 4)
	binary.BigEndian.PutUint32(bufSize, uint32(len(bufMsg)))
	buf = append(buf, bufSize, bufMsg)
	lg.Debugf("stream client send: tm: %#v ==> size %d, buf %v <%s>", tm, len(bufMsg), bufMsg, bufMsg)
	cs.conn.Lock()
	defer func() {
		if mainCopy {
			atomic.StoreInt32(&cs.encMainCopy, 0)
		}
		cs.conn.Unlock()
	}()
	if _, err := buf.WriteTo(cs.conn.netconn); err != nil {
		return err
	}
	return nil
}

func (cs *streamClientStream) Recv(msgPtr interface{}) error {
	if cs.selfChan == nil {
		return io.EOF
	}
	var tm *streamTransportMsg
	select {
	case <-cs.conn.closed:
		return ErrConnReset
	case tm = <-cs.selfChan:
	}
	if err, ok := tm.msg.(error); ok { // message handler returned error
		if err == io.EOF {
			cs.selfChan = nil
		}
		return err
	}

	cs.conn.owner.lg.Debugf("stream client recv: tm: %#v", tm)
	rptr := reflect.ValueOf(msgPtr)
	kind := rptr.Kind()
	if kind == reflect.Invalid { // msgPtr is nil
		return nil // user just looks at error, no error here
	}
	if kind != reflect.Ptr || rptr.IsNil() {
		panic("not a pointer or nil pointer")
	}
	rv := rptr.Elem()
	mrv := reflect.ValueOf(tm.msg)
	if rv.Kind() != reflect.Ptr && mrv.Kind() == reflect.Ptr {
		mrv = mrv.Elem()
	}
	rv.Set(mrv)

	if tm.dstChan != 0 {
		if cs.dstChan != 0 && cs.dstChan != tm.dstChan {
			panic("private chan changed")
		}
		cs.dstChan = tm.dstChan
	}

	return nil
}

func (cs *streamClientStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := cs.Send(msgSnd); err != nil {
		return err
	}
	if err := cs.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}

func (cs *streamClientStream) Write(p []byte) (n int, err error) {
	if err := cs.Send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (cs *streamClientStream) Read(p []byte) (n int, err error) {
	if len(cs.rbuff) == 0 {
		if err := cs.Recv(&cs.rbuff); err != nil {
			return 0, err
		}
	}
	n = copy(p, cs.rbuff)
	cs.rbuff = cs.rbuff[n:]
	return
}
