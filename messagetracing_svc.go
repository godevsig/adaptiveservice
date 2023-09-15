package adaptiveservice

import (
	"container/list"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SrvMessageTracing : service messageTracing
const SrvMessageTracing = "messageTracing"

type messageTracer struct {
	sync.RWMutex
	lg  Logger
	cap int
	lru *list.List // with element Value being *tracingSession
	lut map[uuid.UUID]*list.Element
}

func newMessageTracer(cap int, lg Logger) *messageTracer {
	return &messageTracer{
		lg:  lg,
		cap: cap,
		lru: list.New(),
		lut: make(map[uuid.UUID]*list.Element, cap),
	}
}

type tracingSession struct {
	sync.Mutex
	tracingID uuidptr
	records   []*tracedMessageRecord
}

type tracedMessageRecord struct {
	timeStamp time.Time
	msg       string
	tracingID uuidptr
	tag       string
	connInfo  string
}

func (tmrcd *tracedMessageRecord) Handle(stream ContextStream) (reply any) {
	mTracer := stream.GetContext().(*messageTracer)
	mTracer.lg.Debugf("tracedMessage handler: <%#v>", tmrcd)

	mTracer.RLock()
	elem, has := mTracer.lut[*tmrcd.tracingID]
	mTracer.RUnlock()

	if has {
		tSession := elem.Value.(*tracingSession)
		tSession.Lock()
		tSession.records = append(tSession.records, tmrcd)
		tSession.Unlock()
		mTracer.Lock()
		mTracer.lru.MoveToFront(elem)
		mTracer.Unlock()
	} else {
		records := make([]*tracedMessageRecord, 0, 32)
		records = append(records, tmrcd)
		tSession := &tracingSession{tracingID: tmrcd.tracingID}
		tSession.Lock()
		tSession.records = records
		tSession.Unlock()
		mTracer.Lock()
		if mTracer.lru.Len() >= mTracer.cap {
			oldest := mTracer.lru.Back()
			tSession := mTracer.lru.Remove(oldest).(*tracingSession)
			delete(mTracer.lut, *tSession.tracingID)
		}
		elem := mTracer.lru.PushFront(tSession)
		mTracer.lut[*tmrcd.tracingID] = elem
		mTracer.Unlock()
	}

	return nil
}

// reply with []*tracedMessageRecord
type readTracedMsg struct {
	tracingID uuidptr
}

func (rtmsg *readTracedMsg) Handle(stream ContextStream) (reply any) {
	mTracer := stream.GetContext().(*messageTracer)

	mTracer.RLock()
	elem, has := mTracer.lut[*rtmsg.tracingID]
	mTracer.RUnlock()
	records := []*tracedMessageRecord{}
	if has {
		newRecords := make([]*tracedMessageRecord, 0, 32)
		tSession := elem.Value.(*tracingSession)
		tSession.Lock()
		records = tSession.records
		tSession.records = newRecords
		tSession.Unlock()
	}
	return records
}

// publishMessageTracingService declares the message tracing service.
func (s *Server) publishMessageTracingService() error {
	mTracer := newMessageTracer(1024, s.lg)
	knownMsgs := []KnownMessage{(*tracedMessageRecord)(nil), (*readTracedMsg)(nil)}
	return s.publish(s.scope, BuiltinPublisher, SrvMessageTracing,
		knownMsgs,
		OnNewStreamFunc(func(ctx Context) {
			ctx.SetContext(mTracer)
		}))
}

func init() {
	RegisterType((*tracedMessageRecord)(nil))
	RegisterType((*readTracedMsg)(nil))
	RegisterType(([]*tracedMessageRecord)(nil))
}
