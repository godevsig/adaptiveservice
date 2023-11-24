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

type traceFilter struct {
	field   string
	pattern string
}

type traceConf struct {
	id      uuid.UUID
	count   uint32
	seqNum  uint32
	filters []traceFilter
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
	types map[reflect.Type]*traceConf
}{
	types: make(map[reflect.Type]*traceConf),
}

// TraceMsgByType traces the message type specified by 'msg' 'count' times repeatedly.
// Each matching of the 'msg' type starts a message tracing session and has a unique session token.
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
// A call to TraceMsgByType() with 'count' equals 1 only starts a one time tracing session.
// The subsequent messages with the same type will not be traced unless another call
// to TraceMsgByType() is made with the same type.
// TraceMsgByType can work with different input message types at the same time.
//
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
func TraceMsgByType(msg any, count uint32) (token string, err error) {
	rtype := reflect.TypeOf(msg)
	return TraceMsgByName(rtype.String(), count)
}

// TraceMsgByTypeWithFilters is like TraceMsgByType but takes extra filters.
// Filter should be in the form of "field=pattern", in which pattern supports simple wildcard.
// Tracing sessions start only if all filters match their patterns.
func TraceMsgByTypeWithFilters(msg any, count uint32, filters []string) (token string, err error) {
	rtype := reflect.TypeOf(msg)
	return TraceMsgByNameWithFilters(rtype.String(), count, filters)
}

// TraceMsgByName is like TraceMsgByType but takes the message type name
func TraceMsgByName(name string, count uint32) (token string, err error) {
	return TraceMsgByNameWithFilters(name, count, nil)
}

type msgTracingFilter struct {
	field   string
	pattern string
}

// TraceMsgByNameWithFilters is like TraceMsgByTypeWithFilters but takes the message type name
func TraceMsgByNameWithFilters(name string, count uint32, filters []string) (token string, err error) {
	if count > MaxTracingSessions {
		count = MaxTracingSessions
	}
	rtype := GetRegisteredTypeByName(name)
	if rtype == nil {
		return "", fmt.Errorf("%s not traceable", name)
	}

	var traceFilters []traceFilter
	if len(filters) != 0 {
		rt := rtype
		if rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct {
			return "", fmt.Errorf("type %s not supported", rt.Kind())
		}
		for _, filter := range filters {
			strs := strings.Split(filter, "=")
			if len(strs) != 2 {
				return "", fmt.Errorf("%s format error", filter)
			}
			field, pattern := strs[0], strs[1]
			if _, has := rt.FieldByName(field); !has {
				return "", fmt.Errorf("no field %s found", field)
			}
			traceFilters = append(traceFilters, traceFilter{field, pattern})
		}
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
	if tcptr, has := tracedMsgList.types[rtype]; has {
		if count < tcptr.seqNum {
			count = tcptr.seqNum + 1
		}
		tcptr.count = count
		tcptr.filters = traceFilters
		id = tcptr.id
		return
	}
	id, err = uuid.NewRandom()
	if err != nil {
		return
	}
	tracedMsgList.types[rtype] = &traceConf{id, count, 0, traceFilters}
	return
}

// UnTraceMsgAll untags all message types that have been tagged by TraceMsgBy* functions
func UnTraceMsgAll() {
	tracedMsgList.Lock()
	tracedMsgList.types = make(map[reflect.Type]*traceConf)
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
		return reEmptyStruct.ReplaceAllLiteralString(msg, "{}")
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

	getTraceConf := func() *traceConf {
		rtype := reflect.TypeOf(msg)
		tracedMsgList.Lock()
		defer tracedMsgList.Unlock()

		tcptr := tracedMsgList.types[rtype]
		if tcptr == nil {
			return nil
		}
		if tcptr.seqNum >= tcptr.count {
			delete(tracedMsgList.types, rtype)
			return nil
		}
		return tcptr
	}

	tcptr := getTraceConf()
	if tcptr == nil {
		return nil
	}

	if len(tcptr.filters) != 0 {
		rv := reflect.ValueOf(msg)
		if rv.Kind() == reflect.Pointer {
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			return nil
		}
		for _, filter := range tcptr.filters {
			value := rv.FieldByName(filter.field)
			if !wildcardMatch(filter.pattern, fmt.Sprintf("%v", value)) {
				return nil
			}
		}
	}

	tracingID = &uuidInfo{tcptr.id, tcptr.seqNum}
	atomic.AddUint32(&tcptr.seqNum, 1)
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
	closed        bool
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
		tracedMsgRaw := <-mth.tracedMsgChan
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

func (mth *msgTraceHelper) runOnce() {
	mth.once.Do(func() {
		mth.tracedMsgChan = make(chan *tracedMsgRaw, 128)
		go func() {
			defer func() {
				close(mth.done)
				mth.closed = true
			}()
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
	if mth.closed {
		return errors.New("Message trace helper closed")
	}

	select {
	case mth.tracedMsgChan <- &tracedMsgRaw{time.Now(), msg, tracingID, tag, netconn}:
	default:
		return errors.New("Message trace helper buffer full")
	}

	return nil
}
