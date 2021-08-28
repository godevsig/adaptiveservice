package adaptiveservice

import (
	"fmt"
	"io"
	"net"
	"reflect"
	"unsafe"

	"github.com/barkimedes/go-deepcopy"
)

type chanTransportMsg struct {
	srcChan chan *chanTransportMsg
	msg     interface{}
}

type handshake struct {
	connClose      chan struct{}
	serverChanInfo chan chan *chanTransportMsg
}

// server main receive chan transport.
type chanTransport struct {
	svc        *service
	acceptChan chan handshake
	closed     chan struct{}
}

func (svc *service) newChanTransport() (*chanTransport, error) {
	ct := &chanTransport{
		svc:        svc,
		acceptChan: make(chan handshake),
		closed:     make(chan struct{}),
	}
	go ct.receiver()
	regServiceChan(svc.publisherName, svc.serviceName, ct)
	svc.s.lg.Infof("service %s %s listening on internal channel", svc.publisherName, svc.serviceName)
	return ct, nil
}

func (ct *chanTransport) close() {
	close(ct.closed)
}

type chanServerStream struct {
	Context
	connClose   chan struct{}
	srcChan     chan *chanTransportMsg
	privateChan chan *chanTransportMsg // dedicated to the client
	rbuff       []byte
}

func (ss *chanServerStream) sendNoPrivate(msg interface{}) error {
	if ss.connClose == nil {
		return io.EOF
	}
	ss.srcChan <- &chanTransportMsg{msg: msg}
	return nil
}

func (ss *chanServerStream) Send(msg interface{}) error {
	if ss.connClose == nil {
		return io.EOF
	}
	ss.srcChan <- &chanTransportMsg{srcChan: ss.privateChan, msg: msg}
	return nil
}

func (ss *chanServerStream) Recv(msgPtr interface{}) error {
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
		rv.Set(reflect.ValueOf(tm.msg))
	}

	return nil
}

func (ss *chanServerStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := ss.Send(msgSnd); err != nil {
		return err
	}
	if err := ss.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}

func (ss *chanServerStream) Write(p []byte) (n int, err error) {
	if err := ss.Send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (ss *chanServerStream) Read(p []byte) (n int, err error) {
	if len(ss.rbuff) == 0 {
		if err := ss.Recv(&ss.rbuff); err != nil {
			return 0, err
		}
	}
	n = copy(p, ss.rbuff)
	ss.rbuff = ss.rbuff[n:]
	return
}

type chanAddr uint64

type chanconn struct {
	localAddr  chanAddr
	remoteAddr chanAddr
}

func (cc chanconn) Close() error {
	return nil
}

func (cc chanconn) LocalAddr() net.Addr {
	return cc.localAddr
}

func (cc chanconn) RemoteAddr() net.Addr {
	return cc.remoteAddr
}

func (ca chanAddr) Network() string {
	return "chan"
}

func (ca chanAddr) String() string {
	return fmt.Sprintf("0x%x", uint64(ca))
}

func (ct *chanTransport) receiver() {
	mq := ct.svc.s.mq
	lg := ct.svc.s.lg

	for {
		select {
		case <-ct.closed:
			return
		case hs := <-ct.acceptChan:
			go func() {
				connClose := hs.connClose
				recvChan := make(chan *chanTransportMsg, ct.svc.s.qsize)
				hs.serverChanInfo <- recvChan
				cc := chanconn{
					localAddr:  chanAddr(uintptr(unsafe.Pointer(&ct.closed))),
					remoteAddr: chanAddr(uintptr(unsafe.Pointer(&connClose))),
				}
				lg.Debugf("%s %s new chan connection from: %s", ct.svc.publisherName, ct.svc.serviceName, cc.RemoteAddr().String())
				if ct.svc.fnOnConnect != nil {
					lg.Debugf("%s %s on connect", ct.svc.publisherName, ct.svc.serviceName)
					if ct.svc.fnOnConnect(cc) {
						return
					}
				}
				defer func() {
					if ct.svc.fnOnDisconnect != nil {
						lg.Debugf("%s %s on disconnect", ct.svc.publisherName, ct.svc.serviceName)
						ct.svc.fnOnDisconnect(cc)
					}
					lg.Debugf("%s %s chan connection disconnected: %s", ct.svc.publisherName, ct.svc.serviceName, cc.RemoteAddr().String())
				}()
				ssMap := make(map[chan *chanTransportMsg]*chanServerStream)
				for {
					select {
					case <-connClose:
						return
					case tm := <-recvChan:
						ss := ssMap[tm.srcChan]
						if ss == nil {
							ss = &chanServerStream{
								connClose:   connClose,
								Context:     &contextImpl{},
								srcChan:     tm.srcChan,
								privateChan: make(chan *chanTransportMsg, cap(tm.srcChan)),
							}
							ssMap[tm.srcChan] = ss
							if ct.svc.fnOnNewStream != nil {
								lg.Debugf("%s %s on new stream", ct.svc.publisherName, ct.svc.serviceName)
								ct.svc.fnOnNewStream(ss)
							}
						}
						if ct.svc.canHandle(tm.msg) {
							mm := &metaKnownMsg{
								stream: ss,
								msg:    tm.msg.(KnownMessage),
							}
							mq.putMetaMsg(mm)
						} else {
							ss.Send(ErrBadMessage)
						}
					}
				}
			}()
		}
	}
}

// below for client side

type chanClientStream struct {
	conn        *chanConnection
	selfChan    chan *chanTransportMsg
	privateChan chan *chanTransportMsg // opened by server message handler
	rbuff       []byte
}

type clientChanTransport struct {
	owner *Client
	ct    *chanTransport
}

// chan connection for client.
type chanConnection struct {
	Stream
	cct        *clientChanTransport
	connClose  chan struct{}
	serverChan chan *chanTransportMsg // link to server receive chan
}

// newConnection creates a new connection to the chan transport for client.
func (cct *clientChanTransport) newConnection() *chanConnection {
	connClose := make(chan struct{})
	serverChanInfo := make(chan chan *chanTransportMsg)

	cct.ct.acceptChan <- handshake{connClose, serverChanInfo}
	conn := &chanConnection{
		cct:        cct,
		connClose:  connClose,
		serverChan: <-serverChanInfo,
	}
	conn.Stream = conn.NewStream()
	return conn
}

// NewStream creates a new client stream.
func (conn *chanConnection) NewStream() Stream {
	cs := &chanClientStream{
		conn:     conn,
		selfChan: make(chan *chanTransportMsg, cap(conn.serverChan)),
	}
	return cs
}

func (conn *chanConnection) Close() {
	close(conn.connClose)
}

func (cs *chanClientStream) Send(msg interface{}) error {
	if cs.selfChan == nil {
		return io.EOF
	}
	if _, ok := msg.(KnownMessage); ok {
		if cs.conn.cct.owner.deepCopy {
			msg = deepcopy.MustAnything(msg)
		}
		cs.conn.serverChan <- &chanTransportMsg{srcChan: cs.selfChan, msg: msg}
		return nil
	}

	if cs.privateChan != nil {
		if cs.conn.cct.owner.deepCopy {
			msg = deepcopy.MustAnything(msg)
		}
		cs.privateChan <- &chanTransportMsg{msg: msg}
		return nil
	}

	return ErrBadMessage
}

func (cs *chanClientStream) Recv(msgPtr interface{}) error {
	if cs.selfChan == nil {
		return io.EOF
	}
	tm := <-cs.selfChan
	if err, ok := tm.msg.(error); ok { // message handler returned error
		if err == io.EOF {
			cs.selfChan = nil
		}
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
	mrv := reflect.ValueOf(tm.msg)
	if rv.Kind() != reflect.Ptr && mrv.Kind() == reflect.Ptr {
		mrv = mrv.Elem()
	}
	rv.Set(mrv)

	if tm.srcChan != nil {
		if cs.privateChan != nil && cs.privateChan != tm.srcChan {
			panic("private chan changed")
		}
		cs.privateChan = tm.srcChan
	}

	return nil
}

func (cs *chanClientStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := cs.Send(msgSnd); err != nil {
		return err
	}
	if err := cs.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}

func (cs *chanClientStream) Write(p []byte) (n int, err error) {
	if err := cs.Send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (cs *chanClientStream) Read(p []byte) (n int, err error) {
	if len(cs.rbuff) == 0 {
		if err := cs.Recv(&cs.rbuff); err != nil {
			return 0, err
		}
	}
	n = copy(p, cs.rbuff)
	cs.rbuff = cs.rbuff[n:]
	return
}
