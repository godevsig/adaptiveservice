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
	"unsafe"

	"github.com/niubaoshu/gotiny"
)

type streamClientStream struct {
	conn        *streamConnection
	msgChan     chan *metaMsg
	encMainCopy int32
	enc         *gotiny.Encoder
	timeouter
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
		// wait real server to connect
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
	defer func() {
		close(conn.closed)
		conn.closed = nil
	}()
	lg := conn.owner.lg
	netconn := conn.netconn
	dec := gotiny.NewDecoderWithPtr((*streamTransportMsg)(nil))
	dec.SetCopyMode()
	bufSize := make([]byte, 4)
	bufMsg := make([]byte, 512)
	for {
		if _, err := io.ReadFull(netconn, bufSize); err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "use of closed network connection") {
				lg.Debugf("stream client receiver: connection closed: %v", err)
			} else {
				lg.Warnf("stream client receiver: read size error: %v", err)
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
			lg.Warnf("stream client receiver: read buf error: %v", err)
			return
		}

		var tm streamTransportMsg
		//escapes(&tm)
		func() {
			defer func() {
				if e := recover(); e != nil {
					err := fmt.Errorf("unknown message: %v", e)
					lg.Errorf("%v", err)
					tm.msg = err
				}
			}()
			dec.Decode(bufMsg, &tm)
		}()
		//lg.Debugf("stream client receiver: tm: %#v", &tm)

		if tm.chanID != 0 {
			func() {
				defer func() {
					if err := recover(); err != nil {
						lg.Errorf("broken stream chan: %v", err)
					}
				}()
				msgChan := *(*chan *metaMsg)(unsafe.Pointer(uintptr(tm.chanID)))
				msgChan <- &metaMsg{tm.msg, tm.transportFeats}
			}()
		} else {
			panic("msg channel not specified")
		}
	}
}

func (conn *streamConnection) NewStream() Stream {
	return &streamClientStream{
		conn:    conn,
		msgChan: make(chan *metaMsg, conn.owner.qsize),
		enc:     gotiny.NewEncoderWithPtr((*streamTransportMsg)(nil)),
	}
}
func (conn *streamConnection) Close() {
	conn.netconn.Close()
	mTraceHelper.tryWait()
}

func (cs *streamClientStream) GetNetconn() Netconn {
	return cs.conn.netconn
}

func (cs *streamClientStream) Close() {
	lg := cs.conn.owner.lg
	err := cs.Send(streamCloseMsg{})
	if err != nil {
		lg.Errorf("closing stream transport stream error: %v", err)
	}
}

func (cs *streamClientStream) Send(msg interface{}) error {
	if cs.msgChan == nil || cs.conn.closed == nil {
		return io.EOF
	}
	lg := cs.conn.owner.lg

	cid := uint64(uintptr(unsafe.Pointer(&cs.msgChan)))
	var tfs transportFeats
	tracingID := getTracingID(msg)
	if tracingID != nil {
		tfs = append(tfs, tracingID)
	}
	tm := streamTransportMsg{chanID: cid, msg: msg, transportFeats: tfs}

	buf := net.Buffers{}
	mainCopy := false
	if atomic.CompareAndSwapInt32(&cs.encMainCopy, 0, 1) {
		//lg.Debugf("enc main copy")
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
	//lg.Debugf("stream client send: tm: %#v", &tm)
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

	if tracingID != nil {
		if err := mTraceHelper.traceMsg(msg, tracingID, "client send", cs.conn.netconn); err != nil {
			lg.Warnf("message tracing on client send error: %v", err)
		}
	}
	return nil
}

func (cs *streamClientStream) Recv(msgPtr interface{}) (err error) {
	connClosed := cs.conn.closed
	if cs.msgChan == nil || cs.conn.closed == nil {
		return io.EOF
	}

	rptr := reflect.ValueOf(msgPtr)
	if msgPtr != nil && (rptr.Kind() != reflect.Ptr || rptr.IsNil()) {
		panic("not a pointer or nil pointer")
	}

	lg := cs.conn.owner.lg
	select {
	case <-connClosed:
		return ErrConnReset
	case <-cs.timeouter.timeoutChan():
		return ErrRecvTimeout
	case mm := <-cs.msgChan:
		msg := mm.msg
		tracingID := mm.getTracingID()
		if tracingID != nil {
			if err := mTraceHelper.traceMsg(msg, tracingID, "client recv", cs.conn.netconn); err != nil {
				lg.Warnf("message tracing on client recv error: %v", err)
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

func (cs *streamClientStream) SendRecv(msgSnd interface{}, msgRcvPtr interface{}) error {
	if err := cs.Send(msgSnd); err != nil {
		return err
	}
	if err := cs.Recv(msgRcvPtr); err != nil {
		return err
	}
	return nil
}
