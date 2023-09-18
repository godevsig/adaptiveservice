package adaptiveservice

import (
	"fmt"
	"io"
	"reflect"
	"unsafe"

	"github.com/barkimedes/go-deepcopy"
)

type chanClientStream struct {
	conn    *chanConnection
	msgChan chan *metaMsg
	timeouter
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
		conn:    conn,
		msgChan: make(chan *metaMsg, cap(conn.serverChan)),
	}
	return cs
}

func (conn *chanConnection) Close() {
	close(conn.connClose)
}

func (cs *chanClientStream) GetNetconn() Netconn {
	cc := chanconn{
		localAddr:  chanAddr(uintptr(unsafe.Pointer(&cs.conn.connClose))),
		remoteAddr: chanAddr(uintptr(unsafe.Pointer(&cs.conn.cct.ct.closed))),
	}
	return cc
}

func (cs *chanClientStream) Close() {
	lg := cs.conn.cct.owner.lg
	err := cs.Send(streamCloseMsg{})
	if err != nil {
		lg.Errorf("closing chan transport stream error: %v", err)
	}
}

func (cs *chanClientStream) Send(msg interface{}) error {
	if cs.msgChan == nil {
		return io.EOF
	}
	if cs.conn.cct.owner.deepCopy {
		msg = deepcopy.MustAnything(msg)
	}
	tracingID := getTracingID(msg)
	if tracingID != nil {
		err := traceMsg(msg, tracingID, "client send", cs.GetNetconn())
		if err != nil {
			cs.conn.cct.owner.lg.Warnf("message tracing on client send error: %v", err)
		}
	}
	cs.conn.serverChan <- &chanTransportMsg{srcChan: cs.msgChan, msg: msg, tracingID: tracingID}
	return nil
}

func (cs *chanClientStream) Recv(msgPtr interface{}) (err error) {
	msgChan := cs.msgChan
	if cs.msgChan == nil {
		return io.EOF
	}
	rptr := reflect.ValueOf(msgPtr)
	if msgPtr != nil && (rptr.Kind() != reflect.Ptr || rptr.IsNil()) {
		panic("not a pointer or nil pointer")
	}

	select {
	case <-cs.timeouter.timeoutChan():
		return ErrRecvTimeout
	case mm := <-msgChan:
		msg := mm.msg
		if mm.tracingID != nil {
			err := traceMsg(msg, mm.tracingID, "client recv", cs.GetNetconn())
			if err != nil {
				cs.conn.cct.owner.lg.Warnf("message tracing on client recv error: %v", err)
			}
		}

		if err, ok := msg.(error); ok { // message handler returned error
			if fmt.Sprintf("%#v", err) == fmt.Sprintf("%#v", io.EOF) {
				err = io.EOF
				cs.msgChan = nil
			}
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

func (cs *chanClientStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := cs.Send(msgSnd); err != nil {
		return err
	}
	if err := cs.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}
