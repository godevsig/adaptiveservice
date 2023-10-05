package adaptiveservice

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/timandy/routine"
)

type uuidConf struct {
	id     uuid.UUID
	count  uint32
	seqNum uint32
}

type uuidInfo struct {
	id     uuid.UUID
	seqNum uint32
}

type uuidInfoPtr = *uuidInfo

type infoPerRoutine struct {
	tracingID uuidInfoPtr
}

var routineLocal = routine.NewThreadLocalWithInitial(func() any {
	return &infoPerRoutine{}
})

func getRoutineLocal() *infoPerRoutine {
	return routineLocal.Get().(*infoPerRoutine)
}

var tracedMsgList = struct {
	sync.Mutex
	types map[reflect.Type]*uuidConf
}{
	types: make(map[reflect.Type]*uuidConf),
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
	return TraceMsgByNameWithCount(rtype.String(), 1)
}

// TraceMsgByTypeWithCount traces the message type specified by 'msg' 'count' times repeatedly.
//  msg: any value with the same type of the message to be traced
//  count: the maximum number is the value of MaxTracingSessions
//   >0: trace count times
//    0: stop tracing the specified message type
// The returned token has below forms:
//  if count > 1, for example count = 100
//   30180061-1044-4b9e-a8ee-174806afe058.0..99
//  if count = 1:
//   30180061-1044-4b9e-a8ee-174806afe058.0
//  if count = 0:
//   NA
func TraceMsgByTypeWithCount(msg any, count uint32) (token string, err error) {
	rtype := reflect.TypeOf(msg)
	return TraceMsgByNameWithCount(rtype.String(), count)
}

// TraceMsgByName is like TraceMsgByType but take the message type name
func TraceMsgByName(name string) (token string, err error) {
	return TraceMsgByNameWithCount(name, 1)
}

// TraceMsgByNameWithCount is like TraceMsgByTypeWithCount but take the message type name
func TraceMsgByNameWithCount(name string, count uint32) (token string, err error) {
	if count > MaxTracingSessions {
		count = MaxTracingSessions
	}
	rtype := GetRegisteredTypeByName(name)
	if rtype == nil {
		return "", errors.New(name + " not traceable")
	}

	var id uuid.UUID
	defer func() {
		if err == nil {
			idstr := id.String()
			if count < 1 {
				token = "NA"
			} else if count > 1 {
				token = fmt.Sprintf("%s.0..%d", idstr, count-1)
			} else {
				token = fmt.Sprintf("%s.0", idstr)
			}
		}
	}()

	tracedMsgList.Lock()
	defer tracedMsgList.Unlock()
	if count < 1 {
		delete(tracedMsgList.types, rtype)
		return
	}
	if ucptr, has := tracedMsgList.types[rtype]; has {
		if count < ucptr.seqNum {
			count = ucptr.seqNum + 1
		}
		ucptr.count = count
		id = ucptr.id
		return
	}
	id, err = uuid.NewRandom()
	if err != nil {
		return
	}
	tracedMsgList.types[rtype] = &uuidConf{id, count, 0}
	return
}

// UnTraceMsgAll untags all message types that have been tagged by TraceMsgBy* functions
func UnTraceMsgAll() {
	tracedMsgList.Lock()
	tracedMsgList.types = make(map[reflect.Type]*uuidConf)
	tracedMsgList.Unlock()
}

// ReadAllTracedMsg is equivalent to ReadTracedMsg("00000000-0000-0000-0000-000000000000.0")
func ReadAllTracedMsg() (string, error) {
	return ReadTracedMsg("00000000-0000-0000-0000-000000000000.0")
}

// ReadTracedMsg reads all the collected traced messages by the token returned
// by TraceMsgByType().
//  token: should be in the format like 30180061-1044-4b9e-a8ee-174806afe058.0
//   Special tokens starting with 00000000-0000-0000-0000-000000000000
//   are used to retrieve all records on behalf of all previous tokens.
func ReadTracedMsg(token string) (string, error) {
	fields := strings.Split(token, ".")
	if len(fields) != 2 {
		return "", fmt.Errorf("%s format error", token)
	}
	id, err := uuid.Parse(fields[0])
	if err != nil {
		return "", fmt.Errorf("parse UUID error: %v", err)
	}
	seqNum, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil {
		return "", fmt.Errorf("parse sequence number error: %v", err)
	}

	c := NewClient().SetDiscoverTimeout(0)
	connChan := c.Discover(BuiltinPublisher, SrvMessageTracing, "*")
	var allRecord []*tracedMessageRecord
	for conn := range connChan {
		records := []*tracedMessageRecord{}
		conn.SendRecv(&readTracedMsg{&uuidInfo{id, uint32(seqNum)}}, &records)
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
	if sb.Len() == 0 {
		return "", nil
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

func getTracingID(msg any) uuidInfoPtr {
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
	defer tracedMsgList.Unlock()

	ucptr := tracedMsgList.types[rtype]
	if ucptr == nil {
		return nil
	}
	if ucptr.seqNum >= ucptr.count {
		delete(tracedMsgList.types, rtype)
		return nil
	}
	tracingID = &uuidInfo{ucptr.id, ucptr.seqNum}
	atomic.AddUint32(&ucptr.seqNum, 1)
	return tracingID
}

type tracedMsgRaw struct {
	timeStamp time.Time
	msg       any
	tracingID uuidInfoPtr
	tag       string
	netconn   Netconn
}

type msgTraceHelper struct {
	tracedMsgChan chan *tracedMsgRaw
	lg            Logger
	once          sync.Once
	undelivered   *tracedMessageRecord
	done          chan struct{}
	failedCount   int
}

var mTraceHelper = &msgTraceHelper{done: make(chan struct{})}

func (mth *msgTraceHelper) init(lg Logger) {
	if mth.lg == nil {
		mth.lg = lg
	}
}

func (mth *msgTraceHelper) len() int {
	len := len(mth.tracedMsgChan)
	if mth.undelivered != nil {
		len++
	}
	return len
}

func (mth *msgTraceHelper) tryWait() {
	wait := mth.len()
	for wait != 0 {
		runtime.Gosched()
		wait--
	}
}

// It should be called from atexit() like function, but in go there is
// no such mechanism, so probably no one is proper enough to call it.
func (mth *msgTraceHelper) close() {
	close(mth.tracedMsgChan)
	<-mth.done
}

func (mth *msgTraceHelper) run() error {
	c := NewClient(WithLogger(mth.lg)).SetDiscoverTimeout(3)
	conn := <-c.Discover(BuiltinPublisher, SrvMessageTracing)
	if conn == nil {
		return ErrServiceNotFound(BuiltinPublisher, SrvMessageTracing)
	}
	defer conn.Close()

	deliver := func() error {
		// one way send
		if err := conn.Send(mth.undelivered); err != nil {
			return fmt.Errorf("record message %v error: %v", *mth.undelivered, err)
		}
		mth.undelivered = nil
		mth.failedCount = 0
		return nil
	}

	if mth.undelivered != nil {
		if err := deliver(); err != nil {
			return err
		}
	}
	for {
		select {
		case tracedMsgRaw := <-mth.tracedMsgChan:
			if tracedMsgRaw == nil {
				return nil
			}
			local := tracedMsgRaw.netconn.LocalAddr()
			remote := tracedMsgRaw.netconn.RemoteAddr()
			tracedMsg := tracedMessageRecord{
				tracedMsgRaw.timeStamp,
				fmt.Sprintf("%#v", tracedMsgRaw.msg),
				tracedMsgRaw.tracingID,
				tracedMsgRaw.tag,
				fmt.Sprintf("%s: %s <--> %s", local.Network(), local.String(), remote.String()),
			}
			mth.undelivered = &tracedMsg
			if err := deliver(); err != nil {
				return err
			}
		}
	}
}

func (mth *msgTraceHelper) runOnce() {
	mth.once.Do(func() {
		mth.tracedMsgChan = make(chan *tracedMsgRaw, 64)
		go func() {
			defer close(mth.done)
			for {
				err := mth.run()
				if err == nil {
					return
				}
				mth.failedCount++
				if mth.failedCount >= 3 {
					mth.lg.Errorf("shutdown msgTraceHelper after %d continouse errors", mth.failedCount)
					return
				}
				mth.lg.Warnf("msgTraceHelper error: %v", err)
			}
		}()
	})
}

func (mth *msgTraceHelper) traceMsg(msg any, tracingID uuidInfoPtr, tag string, netconn Netconn) error {
	if _, ok := msg.(*tracedMessageRecord); ok {
		return nil // do not trace *tracedMessageRecord itself
	}

	mth.runOnce()
	mth.tracedMsgChan <- &tracedMsgRaw{time.Now(), msg, tracingID, tag, netconn}
	return nil
}
