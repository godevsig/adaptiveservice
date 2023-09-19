package adaptiveservice

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
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
	return TraceMsgByName(rtype.String())
}

// TraceMsgByName is like TraceMsgByType but take the message type name
func TraceMsgByName(name string) (token string, err error) {
	rtype := GetRegisteredTypeByName(name)
	if rtype == nil {
		return "", errors.New(name + " not traceable")
	}
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
	indent := 0
	for _, rcd := range allRecord {
		if rcd.tag == "client recv" {
			indent--
		}
		if indent < 0 {
			indent = 0
		}
		fmt.Fprintf(&sb, "%v %s[%s] |%s| <%s>\n",
			rcd.timeStamp.Format(timeMicro),
			strings.Repeat("\t", indent),
			rcd.tag,
			rcd.connInfo,
			rcd.msg)
		if rcd.tag == "client send" {
			indent++
		}
	}

	reEmptyStruct := regexp.MustCompile(`\{.*\}`)
	shortenMsg := func(msg string) string {
		return reEmptyStruct.ReplaceAllString(msg, "") + "{}"
	}

	fmt.Fprintln(&sb, "\nSummary(paste below content to http://www.plantuml.com/plantuml):")
	var svcs []string
	for _, rcd := range allRecord {
		fields := strings.Fields(rcd.tag)
		if len(fields) != 2 {
			continue
		}

		who := fields[0]
		action := fields[1]
		switch action {
		case "send":
			ln := len(svcs)
			if ln != 0 {
				if who == "client" {
					who = svcs[ln-1]
				} else {
					svcs = svcs[:ln-1]
				}
			}
			fmt.Fprintf(&sb, "\"%s\" -> ", who)
		case "handler":
			svcs = append(svcs, who)
			fmt.Fprintf(&sb, "\"%s\": %v %s\n", who, rcd.timeStamp.Format(timeMicro), shortenMsg(rcd.msg))
		case "recv":
			ln := len(svcs)
			if ln != 0 {
				if who == "client" {
					who = svcs[ln-1]
				}
			}
			fmt.Fprintf(&sb, "\"%s\": %v %s\n", who, rcd.timeStamp.Format(timeMicro), shortenMsg(rcd.msg))
		}
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
