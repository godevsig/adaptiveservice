package adaptiveservice

import (
	"container/list"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SrvMessageTracing : service messageTracing
const (
	SrvMessageTracing  = "messageTracing"
	MaxTracingSessions = 4096
)

type messageTracer struct {
	sync.RWMutex
	lg  Logger
	cap int
	lru *list.List // with element Value being *tracingSession
	lut map[uuidInfo]*list.Element
}

func newMessageTracer(cap int, lg Logger) *messageTracer {
	return &messageTracer{
		lg:  lg,
		cap: cap,
		lru: list.New(),
		lut: make(map[uuidInfo]*list.Element, cap),
	}
}

type tracingSession struct {
	sync.Mutex
	tracingID uuidInfoPtr
	records   []*tracedMessageRecord
}

type tracedMessageRecord struct {
	timeStamp time.Time
	msg       string
	tracingID uuidInfoPtr
	tag       string
	connInfo  string
}

func (tmrcd *tracedMessageRecord) Handle(stream ContextStream) (reply any) {
	mTracer := stream.GetContext().(*messageTracer)
	mTracer.lg.Debugf("tracedMessageRecord handler: %v [%s] |%s| <%s>",
		tmrcd.timeStamp, tmrcd.tag, tmrcd.connInfo, tmrcd.msg)

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
		records := make([]*tracedMessageRecord, 0, 16)
		records = append(records, tmrcd)
		tSession := &tracingSession{tracingID: tmrcd.tracingID}
		tSession.records = records
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
	tracingID uuidInfoPtr
}

func (rtmsg *readTracedMsg) Handle(stream ContextStream) (reply any) {
	mTracer := stream.GetContext().(*messageTracer)
	tracingID := rtmsg.tracingID
	mTracer.lg.Debugf("readTracedMsg Handler: %#v", tracingID)

	records := []*tracedMessageRecord{}
	// zero uuid matches all
	if rtmsg.tracingID.id == uuid.Nil {
		mixedRecords := make([][]*tracedMessageRecord, mTracer.lru.Len())
		mTracer.Lock()
		lru := mTracer.lru
		mTracer.lru = list.New()
		mTracer.lut = make(map[uuidInfo]*list.Element, mTracer.cap)
		mTracer.Unlock()
		for e := lru.Front(); e != nil; e = e.Next() {
			tSession := e.Value.(*tracingSession)
			mixedRecords = append(mixedRecords, tSession.records)
		}
		for _, rcds := range mixedRecords {
			records = append(records, rcds...)
		}
		return records
	}

	mTracer.Lock()
	elem, has := mTracer.lut[*tracingID]
	if has {
		delete(mTracer.lut, *tracingID)
		mTracer.lru.Remove(elem)
	}
	mTracer.Unlock()
	if has {
		tSession := elem.Value.(*tracingSession)
		records = tSession.records
	}
	return records
}

// publishMessageTracingService declares the message tracing service.
func (s *Server) publishMessageTracingService() error {
	mTracer := newMessageTracer(MaxTracingSessions, s.lg)
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
