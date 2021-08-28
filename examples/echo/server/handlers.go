package server

import (
	"fmt"
	"sync/atomic"
	"time"

	as "github.com/godevsig/adaptiveservice"
)

// MessageRequest is the message sent by client.
// Return MessageReply.
type MessageRequest struct {
	Msg string
	Num int32
}

// MessageReply is the message replied by server,
// with Num+1 and a signature of "yours echo.v1.0".
type MessageReply struct {
	*MessageRequest
	Signature string
}

// Handle handles msg.
func (msg *MessageRequest) Handle(stream as.ContextStream) (reply interface{}) {
	si := stream.GetContext().(*sessionInfo)
	msg.Msg = msg.Msg + "!"
	msg.Num++
	atomic.AddInt64(&si.mgr.counter, 1)
	time.Sleep(time.Second / 2)
	return &MessageReply{msg, si.sessionName}
}

// SubWhoElseEvent is used for clients to subscribe who else event which
// reports new incoming connection to the server.
// Return string.
type SubWhoElseEvent struct{}

// Handle handles msg.
func (msg SubWhoElseEvent) Handle(stream as.ContextStream) (reply interface{}) {
	si := stream.GetContext().(*sessionInfo)
	ch := make(chan string, 1)
	si.mgr.Lock()
	si.mgr.subscribers[ch] = struct{}{}
	si.mgr.Unlock()
	go func() {
		for {
			addr := <-ch
			if err := stream.Send(addr); err != nil {
				si.mgr.Lock()
				delete(si.mgr.subscribers, ch)
				si.mgr.Unlock()
				fmt.Println("channel deleted")
				return
			}
		}
	}()
	return
}

// WhoElse reports all client addresses the server has so far.
// Return string.
type WhoElse struct{}

// Handle handles msg.
func (msg WhoElse) Handle(stream as.ContextStream) (reply interface{}) {
	si := stream.GetContext().(*sessionInfo)
	var addrs string
	si.mgr.RLock()
	for client := range si.mgr.clients {
		addrs += " " + client
	}
	si.mgr.RUnlock()
	return addrs
}

type sessionInfo struct {
	sessionName string
	mgr         *statMgr
}

func (mgr *statMgr) onConnect(netconn as.Netconn) bool {
	raddr := netconn.RemoteAddr().String()
	fmt.Println("on connect from:", raddr)
	mgr.Lock()
	mgr.clients[raddr] = struct{}{}
	mgr.Unlock()

	go func() {
		mgr.RLock()
		for ch := range mgr.subscribers {
			ch <- raddr
		}
		mgr.RUnlock()
	}()

	return false
}

func (mgr *statMgr) onDisconnect(netconn as.Netconn) {
	raddr := netconn.RemoteAddr().String()
	fmt.Println("on disconnect from:", raddr)
	mgr.Lock()
	delete(mgr.clients, raddr)
	mgr.Unlock()
}

func (mgr *statMgr) onNewStream(ctx as.Context) {
	fmt.Println("on new stream")
	sessionName := fmt.Sprintf("yours echo.v1.0 from %d", atomic.AddInt32(&mgr.sessionNum, 1))
	ctx.SetContext(&sessionInfo{sessionName, mgr})
}

var echoKnownMsgs = []as.KnownMessage{
	(*MessageRequest)(nil),
	SubWhoElseEvent{},
	WhoElse{},
}

func init() {
	as.RegisterType((*MessageRequest)(nil))
	as.RegisterType((*MessageReply)(nil))
	as.RegisterType(SubWhoElseEvent{})
	as.RegisterType(WhoElse{})
}
