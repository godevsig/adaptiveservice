package adaptiveservice

import (
	"sync"
	"sync/atomic"
)

// workerPool is a pool of workers.
type workerPool struct {
	wg sync.WaitGroup
	sync.Mutex
	workers []*chan struct{}
	cnt     int32
}

// newWorkerPool creates a new workerPool.
func newWorkerPool() *workerPool {
	return &workerPool{}
}

type status interface {
	idle()
	working()
}

func (wp *workerPool) idle() {
	atomic.AddInt32(&wp.cnt, 1)
}
func (wp *workerPool) working() {
	atomic.AddInt32(&wp.cnt, -1)
}

// worker is a function with a cancel channel it should check for exit.
type worker func(done <-chan struct{}, st status)

// addWorker adds a worker into the workerPool.
func (wp *workerPool) addWorker(w worker) {
	done := make(chan struct{})
	wp.Lock()
	defer wp.Unlock()
	added := false
	for i, pc := range wp.workers {
		if *pc == nil {
			wp.workers[i] = &done
			added = true
			break
		}
	}
	if !added {
		wp.workers = append(wp.workers, &done)
	}
	atomic.AddInt32(&wp.cnt, 1)
	wp.wg.Add(1)
	go func() {
		defer func() {
			if done != nil {
				close(done)
				done = nil
			}
			atomic.AddInt32(&wp.cnt, -1)
			wp.wg.Done()
		}()
		w(done, wp)
	}()
}

// rmWorker tries to remove a random running worker from the workerPool.
// It may do nothing if all workers in the workerPool have been removed
// or exited.
func (wp *workerPool) rmWorker() {
	wp.Lock()
	defer wp.Unlock()
	var pdone *chan struct{}
	for _, pc := range wp.workers {
		if *pc != nil {
			pdone = pc
			break
		}
	}
	if pdone == nil {
		return
	}
	done := *pdone
	*pdone = nil
	close(done)
}

// close closes and waits all the workers in the workerPool to exit.
func (wp *workerPool) close() {
	wp.Lock()
	for _, pc := range wp.workers {
		if *pc != nil {
			done := *pc
			*pc = nil
			close(done)
		}
	}
	wp.Unlock()
	wp.wg.Wait()
}

// len returns the number of idle workers in the workerPool.
func (wp *workerPool) len() int {
	cnt := atomic.LoadInt32(&wp.cnt)
	return int(cnt)
}
