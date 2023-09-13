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
	cap int
	lru *list.List // with element Value being *tracingSession
	lut map[uuid.UUID]*list.Element
}

func newMessageTracer(cap int) *messageTracer {
	return &messageTracer{
		cap: cap,
		lru: list.New(),
		lut: make(map[uuid.UUID]*list.Element, cap),
	}
}

type record struct {
	timeStamp time.Time
	msgRecord *tracedMessageRecord
}

type tracingSession struct {
	sync.Mutex
	tracingID uuidptr
	records   []*record
}

type tracedMessageRecord struct {
	msg       any
	tracingID uuidptr
	tag       string
	connInfo  string
}

func (tmrcd *tracedMessageRecord) Handle(stream ContextStream) (reply any) {
	mTracer := stream.GetContext().(*messageTracer)
	newrcd := &record{time.Now(), tmrcd}

	mTracer.RLock()
	elem, has := mTracer.lut[*tmrcd.tracingID]
	mTracer.RUnlock()

	if has {
		tSession := elem.Value.(*tracingSession)
		tSession.Lock()
		tSession.records = append(tSession.records, newrcd)
		tSession.Unlock()
		mTracer.Lock()
		mTracer.lru.MoveToFront(elem)
		mTracer.Unlock()
	} else {
		records := make([]*record, 0, 32)
		records = append(records, newrcd)
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

// reply with []*record
type readTracedMsg struct {
	tracingID uuidptr
}

func (rtmsg *readTracedMsg) Handle(stream ContextStream) (reply any) {
	mTracer := stream.GetContext().(*messageTracer)

	mTracer.RLock()
	elem, has := mTracer.lut[*rtmsg.tracingID]
	mTracer.RUnlock()
	records := []*record{}
	if has {
		newRecords := make([]*record, 0, 32)
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
	mTracer := newMessageTracer(1024)
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
}
