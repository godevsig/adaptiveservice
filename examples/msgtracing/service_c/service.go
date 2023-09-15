package svcc

import (
	"fmt"
	"sync"

	as "github.com/godevsig/adaptiveservice"
	svcd "github.com/godevsig/adaptiveservice/examples/msgtracing/service_d"
)

// Request is the request from clients.
// Return *Reply
type Request struct {
	Input string
}

// Handle handles msg.
func (req *Request) Handle(stream as.ContextStream) (reply any) {
	svc := stream.GetContext().(*service)
	c := as.NewClient()
	connd := <-c.Discover("example", "serviceD")
	if connd == nil {
		return as.ErrServiceNotFound("example", "serviceD")
	}
	defer connd.Close()

	reqd := svcd.Request{Input: "depend"}
	var repd svcd.Reply
	if err := connd.SendRecv(&reqd, &repd); err != nil {
		return err
	}
	return &Reply{Output: fmt.Sprintf("%s %s service c(%s)", req.Input, svc.info, repd.Output)}
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
	info string
}

func (svc *service) onNewStream(ctx as.Context) {
	ctx.SetContext(svc)
}

// RunService runs the service.
func RunService(opts []as.Option) error {
	s := as.NewServer(opts...).SetPublisher("example")

	svc := &service{info: "Hello"}
	if err := s.Publish("serviceC",
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
