package adaptiveservice

import (
	"fmt"
	"io"
	"net"
	"reflect"
	"unsafe"
)

type chanTransportMsg struct {
	srcChan chan interface{}
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
	delServiceChan(ct.svc.publisherName, ct.svc.serviceName)
}

type chanServerStream struct {
	Context
	netconn     chanconn
	connClose   *chan struct{}
	srcChan     chan interface{}
	privateChan chan interface{} // dedicated to the client
	timeouter
}

func (ss *chanServerStream) GetNetconn() Netconn {
	return ss.netconn
}

func (ss *chanServerStream) Close() {}

func (ss *chanServerStream) Send(msg interface{}) error {
	if *ss.connClose == nil {
		return io.EOF
	}
	ss.srcChan <- msg
	return nil
}

func (ss *chanServerStream) Recv(msgPtr interface{}) (err error) {
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
	case msg := <-ss.privateChan:
		if err, ok := msg.(error); ok {
			return err
		}
		if msgPtr == nil { // msgPtr is nil
			return nil // user just looks at error, no error here
		}

		rv := rptr.Elem()
		defer func() {
			if e := recover(); e != nil {
				err = fmt.Errorf("message type mismatch: %v", e)
			}
		}()
		rv.Set(reflect.ValueOf(msg))
	}

	return
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
				ssMap := make(map[chan interface{}]*chanServerStream)
				for {
					select {
					case <-connClose:
						connClose = nil
						return
					case tm := <-recvChan:
						ss := ssMap[tm.srcChan]
						if ss == nil {
							ss = &chanServerStream{
								netconn:     cc,
								connClose:   &connClose,
								Context:     &contextImpl{},
								srcChan:     tm.srcChan,
								privateChan: make(chan interface{}, cap(tm.srcChan)),
							}
							ssMap[tm.srcChan] = ss
							if ct.svc.fnOnNewStream != nil {
								lg.Debugf("%s %s on new stream %v", ct.svc.publisherName, ct.svc.serviceName, tm.srcChan)
								ct.svc.fnOnNewStream(ss)
							}
						}

						if _, ok := tm.msg.(streamCloseMsg); ok { // check if stream close was sent
							if ct.svc.fnOnStreamClose != nil {
								ct.svc.fnOnStreamClose(ss)
								lg.Debugf("%s %s on stream %v close", ct.svc.publisherName, ct.svc.serviceName, tm.srcChan)
							}
							delete(ssMap, tm.srcChan)
							continue
						}

						if ct.svc.canHandle(tm.msg) {
							mm := &metaKnownMsg{
								stream: ss,
								msg:    tm.msg.(KnownMessage),
							}
							mq.putMetaMsg(mm)
						} else {
							ss.privateChan <- tm.msg
						}
					}
				}
			}()
		}
	}
}
