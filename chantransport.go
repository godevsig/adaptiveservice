package adaptiveservice

import (
	"reflect"

	"github.com/barkimedes/go-deepcopy"
)

type chanTransportMsg struct {
	ctx     Context
	srcChan chan *chanTransportMsg
	msg     interface{}
}

// server main receive chan transport.
type chanTransport struct {
	svc      *service
	recvChan chan *chanTransportMsg
	done     chan struct{}
}

func (svc *service) newChanTransport() (*chanTransport, error) {
	ct := &chanTransport{
		svc:      svc,
		recvChan: make(chan *chanTransportMsg, svc.s.qsize),
		done:     make(chan struct{}),
	}
	go ct.receiver()
	regServiceChan(svc.publisherName, svc.serviceName, ct)
	svc.s.lg.Infof("service %s %s listening on internal channel", svc.publisherName, svc.serviceName)
	return ct, nil
}

func (ct *chanTransport) close() {
	close(ct.done)
}

type chanServerStream struct {
	Context
	srcChan     chan *chanTransportMsg
	privateChan chan *chanTransportMsg // dedicated to the client
}

func (ss *chanServerStream) sendNoPrivate(msg interface{}) error {
	ss.srcChan <- &chanTransportMsg{msg: msg}
	return nil
}

func (ss *chanServerStream) Send(msg interface{}) error {
	if ss.privateChan == nil {
		ss.privateChan = make(chan *chanTransportMsg, cap(ss.srcChan))
		ss.srcChan <- &chanTransportMsg{srcChan: ss.privateChan, msg: msg}
	} else {
		ss.srcChan <- &chanTransportMsg{msg: msg}
	}
	return nil
}

func (ss *chanServerStream) Recv(msgPtr interface{}) error {
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

func (ss *chanServerStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := ss.Send(msgSnd); err != nil {
		return err
	}
	if err := ss.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}

func (ct *chanTransport) receiver() {
	mq := ct.svc.s.mq
	for {
		select {
		case <-ct.done:
			return
		case tm := <-ct.recvChan:
			ss := &chanServerStream{
				Context: tm.ctx,
				srcChan: tm.srcChan,
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
}

// below for client side

type chanClientStream struct {
	conn        *chanConnection
	ctx         Context
	selfChan    chan *chanTransportMsg
	privateChan chan *chanTransportMsg // opened by server message handler
}

type clientChanTransport struct {
	owner *Client
	ct    *chanTransport
}

// chan connection for client.
type chanConnection struct {
	Stream
	cct        *clientChanTransport
	serverChan chan *chanTransportMsg // link to server receive chan
}

// newConnection creates a new connection to the chan transport for client.
func (cct *clientChanTransport) newConnection() *chanConnection {
	conn := &chanConnection{
		cct:        cct,
		serverChan: cct.ct.recvChan,
	}

	conn.Stream = conn.NewStream()
	return conn
}

// NewStream creates a new client stream.
func (conn *chanConnection) NewStream() Stream {
	cs := &chanClientStream{
		conn:     conn,
		ctx:      &contextImpl{},
		selfChan: make(chan *chanTransportMsg, cap(conn.serverChan)),
	}
	fnOnStream := conn.cct.ct.svc.fnOnNewStream
	if fnOnStream != nil {
		fnOnStream(cs.ctx)
	}
	return cs
}

func (conn *chanConnection) Close() {}

func (cs *chanClientStream) Send(msg interface{}) error {
	if _, ok := msg.(KnownMessage); ok {
		if cs.conn.cct.owner.deepCopy {
			msg = deepcopy.MustAnything(msg)
		}
		cs.conn.serverChan <- &chanTransportMsg{ctx: cs.ctx, srcChan: cs.selfChan, msg: msg}
		return nil
	}

	if cs.privateChan != nil {
		cs.privateChan <- &chanTransportMsg{msg: msg}
		return nil
	}

	return ErrBadMessage
}

func (cs *chanClientStream) Recv(msgPtr interface{}) error {
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
