package adaptiveservice

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

type msgQ struct {
	sync.Mutex
	wp                *workerPool
	lg                Logger
	residentWorkers   int
	qSize             int
	qWeight           int
	ingressHighChan   chan *metaKnownMsg
	ingressNormalChan chan *metaKnownMsg
	ingressLowChan    chan *metaKnownMsg
	egressChan        chan *metaKnownMsg
	changeConfirmed   bool
	done              chan struct{}
}

func newMsgQ(residentWorkers, qSizePerCore, qWeight int, lg Logger) *msgQ {
	qSize := qSizePerCore * runtime.NumCPU()
	mq := &msgQ{
		wp:                newWorkerPool(),
		lg:                lg,
		residentWorkers:   residentWorkers,
		qSize:             qSize,
		qWeight:           qWeight,
		ingressNormalChan: make(chan *metaKnownMsg, qSize),
		done:              make(chan struct{}),
	}

	for i := 0; i < mq.residentWorkers; i++ {
		mq.wp.addWorker(mq.worker)
	}
	if qWeight > 0 {
		go mq.autoScaler()
	}

	lg.Infof("msgq created with qSize %v", qSize)
	return mq
}

func (mq *msgQ) close() {
	close(mq.done)
	mq.done = nil
	mq.wp.close()
	mq.lg.Infof("msgq closed")
}

func (mq *msgQ) reorder() {
	mq.lg.Infof("msgq reorder started")
	defer mq.lg.Infof("msgq reorder stopped")
	for {
		var msgH, msgN, msgL *metaKnownMsg
		select {
		case <-mq.done:
			return
		case msgH = <-mq.ingressHighChan:
		case msgN = <-mq.ingressNormalChan:
		case msgL = <-mq.ingressLowChan:
		}

		lenL := len(mq.ingressLowChan)
		lenN := len(mq.ingressNormalChan)
		lenH := len(mq.ingressHighChan)
		mq.lg.Debugf("msgq lenH: %d, lenN: %d, lenL: %d", lenH, lenN, lenL)

		if msgH != nil {
			mq.egressChan <- msgH
		}
		for lenH != 0 {
			mq.egressChan <- <-mq.ingressHighChan
			lenH--
		}

		if msgN != nil {
			mq.egressChan <- msgN
		}
		for lenN != 0 {
			mq.egressChan <- <-mq.ingressNormalChan
			lenN--
		}

		if msgL != nil {
			mq.egressChan <- msgL
		}
		for lenL != 0 {
			mq.egressChan <- <-mq.ingressLowChan
			lenL--
		}
	}
}

func (mq *msgQ) worker(done <-chan struct{}, st status) {
	for {
		select {
		case <-done:
			return
		case mm := <-mq.getEgressChan():
			st.working()
			if mm.tracingID != nil {
				tag := fmt.Sprintf("%s/%s@%s handler", mm.svcInfo.publisherName, mm.svcInfo.serviceName, mm.svcInfo.providerID)
				err := traceMsg(mm.msg, mm.tracingID, tag, mm.stream.GetNetconn())
				if err != nil {
					mq.lg.Warnf("message tracing on server handler error: %v", err)
				}
			}
			getRoutineLocal().tracingID = mm.tracingID
			reply := mm.msg.Handle(mm.stream)
			//mq.lg.Debugf("message: %#v handled, reply: %#v", mm.msg, reply)
			if reply != nil {
				mm.stream.Send(reply)
			}
			st.idle()
		}
	}
}

func (mq *msgQ) autoScaler() {
	for {
		if mq.done == nil {
			return
		}
		egressChan := mq.getEgressChan()
		should := len(egressChan)/mq.qWeight + mq.residentWorkers
		now := mq.wp.len() // idle workers

		k := 1
		switch {
		case should > now:
			k = should - now
			mq.wp.addWorker(mq.worker)
			mq.lg.Debugf("msgq autoScaler: now %d -> should %d: worker added", now, should)
		case should < now:
			k = now - should
			mq.wp.rmWorker()
			mq.lg.Debugf("msgq autoScaler: now %d -> should %d: worker removed", now, should)
		}
		time.Sleep(time.Second / time.Duration(k))
	}
}

func (mq *msgQ) putMetaMsg(mm *metaKnownMsg) {
	initChan := func(ch *chan *metaKnownMsg) {
		mq.Lock()
		if *ch == nil {
			*ch = make(chan *metaKnownMsg, mq.qSize)
		}
		if mq.egressChan == nil {
			mq.egressChan = make(chan *metaKnownMsg, mq.qSize)
			mq.lg.Infof("msgq egressChan created")
			go mq.reorder()
		}
		mq.Unlock()
	}

	switch mm.msg.(type) {
	case HighPriorityMessage:
		//mq.lg.Debugf("msgq high priority message received: %#v", mm.msg)
		if mq.ingressHighChan == nil {
			initChan(&mq.ingressHighChan)
			mq.lg.Infof("msgq ingress high priority chan initialized")
		}
		mq.ingressHighChan <- mm
	case LowPriorityMessage:
		//mq.lg.Debugf("msgq low priority message received: %#v", mm.msg)
		if mq.ingressLowChan == nil {
			initChan(&mq.ingressLowChan)
			mq.lg.Infof("msgq ingress low priority chan initialized")
		}
		mq.ingressLowChan <- mm
	default:
		//mq.lg.Debugf("msgq normal priority message received: %#v", mm.msg)
		mq.ingressNormalChan <- mm
	}
}

func (mq *msgQ) getEgressChan() chan *metaKnownMsg {
	if mq.egressChan == nil {
		return mq.ingressNormalChan
	}

	// protect reading mq.egressChan while writing it by putMetaMsg
	if !mq.changeConfirmed {
		mq.Lock()
		mq.changeConfirmed = true
		mq.lg.Infof("msgq egressChan change confirmed")
		mq.Unlock()
	}

	return mq.egressChan
}
