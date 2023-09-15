package adaptiveservice

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/timandy/routine"
)

type uuidptr = *uuid.UUID

type infoPerRoutine struct {
	tracingID uuidptr
}

var routineLocal = routine.NewThreadLocalWithInitial(func() any {
	return &infoPerRoutine{}
})

func getRoutineLocal() *infoPerRoutine {
	return routineLocal.Get().(*infoPerRoutine)
}

var tracedMsgList = struct {
	sync.Mutex
	types map[reflect.Type]uuidptr
}{
	types: make(map[reflect.Type]uuidptr),
}

// TraceMsgByType starts a message tracing session and returns the session token.
//
// Tracing is type based and always starts from the client side.
// If a message to be sent by client matches the specified type, it is marked as traced message,
// a special traced flag will be carried along the entire path across all the service nodes
// that are involved to handle this message.
//
// When such messages with traced flag are being handled in
// `Handle(ContextStream) any` on server side, all subsequent messages
// under the same stream context are related messages. All related messages will also carry
// the same traced flag and propagate the flag further to next hop and next next hop...
// All related messages with traced flag will be recorded by built-in service "messageTracing".
//  msg: any value with the same type of the message to be traced
// A call to TraceMsgByType() only starts a one time tracing session.
// The subsequent messages with the same type will not be traced unless another call
// to TraceMsgByType() is made with the same type.
// TraceMsgByType can work with different input message types at the same time.
func TraceMsgByType(msg any) (token string, err error) {
	rtype := reflect.TypeOf(msg)
	tracedMsgList.Lock()
	defer tracedMsgList.Unlock()
	if uuid, has := tracedMsgList.types[rtype]; has {
		return uuid.String(), nil
	}
	uuid, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate uuid error: %v", err)
	}
	tracedMsgList.types[rtype] = &uuid
	return uuid.String(), nil
}

// ReadTracedMsg reads all the collected traced messages by the token returned
// by TraceMsgByType().
func ReadTracedMsg(token string) (string, error) {
	uuid, err := uuid.Parse(token)
	if err != nil {
		return "", err
	}

	c := NewClient().SetDiscoverTimeout(0)
	connChan := c.Discover(BuiltinPublisher, SrvMessageTracing, "*")
	var allRecord []*tracedMessageRecord
	for conn := range connChan {
		records := []*tracedMessageRecord{}
		conn.SendRecv(&readTracedMsg{&uuid}, &records)
		if len(records) != 0 {
			allRecord = append(allRecord, records...)
		}
		conn.Close()
	}

	sort.Slice(allRecord, func(i, j int) bool {
		return allRecord[i].timeStamp.Before(allRecord[j].timeStamp)
	})

	var sb strings.Builder
	for _, rcd := range allRecord {
		fmt.Fprintf(&sb, "%v [%s] |%s| <%s>\n",
			rcd.timeStamp.Format(timeNano),
			rcd.tag,
			rcd.connInfo,
			rcd.msg)
	}
	return sb.String(), nil
}

func getTracingID(msg any) uuidptr {
	// try to carry on with the tracingID from current goroutine context
	tracingID := getRoutineLocal().tracingID
	if tracingID != nil {
		return tracingID
	}

	// try if it is one of the target msg types
	if len(tracedMsgList.types) == 0 {
		return nil
	}
	rtype := reflect.TypeOf(msg)
	tracedMsgList.Lock()
	tracingID = tracedMsgList.types[rtype]
	delete(tracedMsgList.types, rtype)
	tracedMsgList.Unlock()
	return tracingID
}

func traceMsg(msg any, tracingID uuidptr, tag string, netconn Netconn) error {
	if _, ok := msg.(*tracedMessageRecord); ok {
		return nil // do not trace *tracedMessageRecord itself
	}
	c := NewClient().SetDiscoverTimeout(0)
	conn := <-c.Discover(BuiltinPublisher, SrvMessageTracing)
	if conn == nil {
		return ErrServiceNotFound(BuiltinPublisher, SrvMessageTracing)
	}
	defer conn.Close()

	local := netconn.LocalAddr()
	connInfo := fmt.Sprintf("%s: %s <--> %s", local.Network(), local.String(), netconn.RemoteAddr().String())
	tracedMsg := tracedMessageRecord{
		time.Now(),
		fmt.Sprintf("%#v", msg),
		tracingID,
		tag,
		connInfo,
	}
	// one way send
	if err := conn.Send(&tracedMsg); err != nil {
		return fmt.Errorf("record message %v error: %v", tracedMsg, err)
	}
	return nil
}
