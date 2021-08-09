package adaptiveservice

import (
	"encoding/binary"
	"io"
	"net"
	"reflect"
	"unsafe"

	"github.com/niubaoshu/gotiny"
)

type streamTransport struct {
	svc  *service
	done chan struct{}
	lnr  net.Listener
}

func (svc *service) newUDSTransport() (*streamTransport, error) {
	addr := lookupServiceUDS(svc.publisherName, svc.serviceName)
	if len(addr) != 0 {
		panic(addr + " already exists")
	}
	lnr, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}

	st := &streamTransport{
		svc:  svc,
		done: make(chan struct{}),
		lnr:  lnr,
	}
	go st.receiver()
	return st, nil
}

func (svc *service) newTCPTransport() (*streamTransport, error) {
	lnr, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, err
	}

	st := &streamTransport{
		svc:  svc,
		done: make(chan struct{}),
		lnr:  lnr,
	}
	go st.receiver()
	_, port, _ := net.SplitHostPort(lnr.Addr().String()) // from [::]:43807
	if svc.scope&ScopeLAN == ScopeLAN {
		if err := regServiceLAN(svc.publisherName, svc.serviceName, svc.s.providerID, port); err != nil {
			svc.s.lg.Warnf("service %s %s with provider %s register to LAN failed: %v", svc.publisherName, svc.serviceName, svc.s.providerID, err)
		}
	}

	if svc.scope&ScopeWAN == ScopeWAN {
		if err := regServiceWAN(svc.publisherName, svc.serviceName, svc.s.providerID, port); err != nil {
			svc.s.lg.Warnf("service %s %s with provider %s register to WAN failed: %v", svc.publisherName, svc.serviceName, svc.s.providerID, err)
		}
	}
	return st, nil
}

func (st *streamTransport) close() {
	st.lnr.Close()
	close(st.done)
	st.done = nil
}

type streamTransportMsg struct {
	srcChan uint64
	dstChan uint64
	msg     interface{}
}

type streamServerStream struct {
	Context
	netconn     net.Conn
	privateChan chan *streamTransportMsg // dedicated to the client
	qsize       int
	dstChan     uint64
	enc         *gotiny.Encoder
}

func (ss *streamServerStream) send(tm *streamTransportMsg) error {
	buf := net.Buffers{}
	// ToDo: use sync.Pool for encoder buf
	bufMsg := ss.enc.Encode(tm)
	bufSize := make([]byte, 4)
	binary.BigEndian.PutUint32(bufSize, uint32(len(bufMsg)))
	buf = append(buf, bufSize, bufMsg)
	if _, err := buf.WriteTo(ss.netconn); err != nil {
		return err
	}
	return nil
}

func (ss *streamServerStream) sendNoPrivate(msg interface{}) error {
	tm := &streamTransportMsg{dstChan: ss.dstChan, msg: msg}
	return ss.send(tm)
}

func (ss *streamServerStream) Send(msg interface{}) error {
	if ss.privateChan == nil {
		ss.privateChan = make(chan *streamTransportMsg, ss.qsize)
	}
	srcChan := uint64(uintptr(unsafe.Pointer(&ss.privateChan)))
	tm := &streamTransportMsg{srcChan: srcChan, dstChan: ss.dstChan, msg: msg}

	return ss.send(tm)
}

func (ss *streamServerStream) Recv(msgPtr interface{}) error {
	if ss.privateChan == nil {
		panic("private chan not established")
	}

	rptr := reflect.ValueOf(msgPtr)
	if rptr.Kind() != reflect.Ptr || rptr.IsNil() {
		panic("not a pointer or nil pointer")
	}

	rv := rptr.Elem()
	tm := <-ss.privateChan
	rv.Set(reflect.ValueOf(tm.msg))
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

func (st *streamTransport) receiver() {
	lg := st.svc.s.lg
	mq := st.svc.s.mq
	lnr := st.lnr
	for {
		netconn, err := lnr.Accept()
		if err != nil {
			st.svc.s.errRecovers <- unrecoverableError{err}
			break
		}
		go func() {
			defer netconn.Close()
			ssMap := make(map[uint64]*streamServerStream)
			dec := gotiny.NewDecoderWithPtr((*streamTransportMsg)(nil))
			bufSize := make([]byte, 4)
			bufMsg := make([]byte, 512)
			for {
				if st.done == nil {
					return
				}
				if _, err := io.ReadFull(netconn, bufSize); err != nil {
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
					return
				}

				var tm streamTransportMsg
				func() {
					defer func() {
						if err := recover(); err != nil {
							lg.Errorln("unknown message: ", err)
						}
					}()
					dec.Decode(bufMsg, &tm)
				}()

				ss := ssMap[tm.srcChan]
				if ss == nil {
					ss = &streamServerStream{
						Context: &contextImpl{},
						netconn: netconn,
						qsize:   st.svc.s.qsize,
						dstChan: tm.srcChan,
						enc:     gotiny.NewEncoderWithPtr((*streamTransportMsg)(nil)),
					}
					ssMap[tm.srcChan] = ss
					if st.svc.fnOnNewStream != nil {
						if err := st.svc.fnOnNewStream(ss); err != nil {
							lg.Errorln("server failed to init stream context: ", err)
						}
					}
				}

				if knownMsg, ok := tm.msg.(KnownMessage); ok {
					mm := &metaKnownMsg{
						stream: ss,
						msg:    knownMsg,
					}
					mq.putMetaMsg(mm)
				} else if tm.dstChan != 0 {
					func() {
						defer func() {
							if err := recover(); err != nil {
								lg.Errorln("broken private chan: ", err)
							}
						}()
						privateChan := *(*chan *streamTransportMsg)(unsafe.Pointer(uintptr(tm.dstChan)))
						privateChan <- &streamTransportMsg{msg: tm.msg}
					}()
				} else {
					if err := ss.Send(ErrBadMessage); err != nil {
						lg.Errorln("send ErrBadMessage failed: ", err)
					}
				}
			}
		}()
	}
}

// below for client side

type streamClientStream struct {
	conn     *streamConnection
	selfChan chan *streamTransportMsg
	dstChan  uint64
	enc      *gotiny.Encoder
}

// stream connection for client.
type streamConnection struct {
	Stream
	owner   *Client
	netconn net.Conn
}

func (c *Client) newStreamConnection(network string, addr string) (*streamConnection, error) {
	netconn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	conn := &streamConnection{
		owner:   c,
		netconn: netconn,
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
	lg := conn.owner.lg
	netconn := conn.netconn
	dec := gotiny.NewDecoderWithPtr((*streamTransportMsg)(nil))
	bufSize := make([]byte, 4)
	bufMsg := make([]byte, 512)
	for {
		if _, err := io.ReadFull(netconn, bufSize); err != nil {
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
			return
		}

		var tm streamTransportMsg
		dec.Decode(bufMsg, &tm)

		if tm.dstChan != 0 {
			func() {
				defer func() {
					if err := recover(); err != nil {
						lg.Errorln("broken stream chan: ", err)
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
	_, ok := msg.(KnownMessage)
	if !ok && cs.dstChan == 0 {
		return ErrBadMessage
	}

	srcChan := uint64(uintptr(unsafe.Pointer(&cs.selfChan)))
	tm := &streamTransportMsg{srcChan: srcChan, dstChan: cs.dstChan, msg: msg}

	buf := net.Buffers{}
	// ToDo: use sync.Pool for encoder buf
	bufMsg := cs.enc.Encode(tm)
	bufSize := make([]byte, 4)
	binary.BigEndian.PutUint32(bufSize, uint32(len(bufMsg)))
	buf = append(buf, bufSize, bufMsg)
	if _, err := buf.WriteTo(cs.conn.netconn); err != nil {
		return err
	}
	return nil
}

func (cs *streamClientStream) Recv(msgPtr interface{}) error {
	tm := <-cs.selfChan
	if err, ok := tm.msg.(error); ok { // message handler returned error
		return err
	}
	rptr := reflect.ValueOf(msgPtr)
	kind := rptr.Kind()
	if kind == reflect.Invalid { // msgPtr is nil
		return nil // user just looks at error, no error here
	}
	if kind != reflect.Ptr || rptr.IsNil() {
		panic("not a pointer or nil pointer")
	}
	rv := rptr.Elem()
	rv.Set(reflect.ValueOf(tm.msg))

	if tm.dstChan != 0 {
		if tm.dstChan != cs.dstChan {
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
