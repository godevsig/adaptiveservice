package selectme

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"

	as "github.com/godevsig/adaptiveservice"
)

// Request is the request from clients.
// Return *Reply
type Request struct {
	Input string
}

// Handle handles msg.
func (req *Request) Handle(stream as.ContextStream) (reply any) {
	svc := stream.GetContext().(*service)

	count := atomic.AddInt32(&svc.counter, 1)
	return &Reply{Output: fmt.Sprintf("thanks for selecting me %v@%v", count, stream.GetNetconn().LocalAddr())}
}

var knownMsgs = []as.KnownMessage{
	(*Request)(nil),
}

// Reply is the reply for Request to clients
type Reply struct {
	Output string
}

type service struct {
	sync.RWMutex
	info    string
	counter int32
}

func (svc *service) onNewStream(ctx as.Context) {
	ctx.SetContext(svc)
}

// RunService runs the service.
func RunService(s *as.Server) error {
	s.SetPublisher("example")

	genID := func() string {
		b := make([]byte, 3)
		rand.Read(b)

		id := hex.EncodeToString(b)
		return id
	}

	svc := &service{info: "It is me"}
	if err := s.PublishIn(as.ScopeLAN, fmt.Sprintf("selectme-%s", genID()),
		knownMsgs,
		as.OnNewStreamFunc(svc.onNewStream),
	); err != nil {
		return err
	}

	// ctrl+c to exit
	return s.Serve()
}

func init() {
	as.RegisterType((*Request)(nil))
	as.RegisterType((*Reply)(nil))
}
