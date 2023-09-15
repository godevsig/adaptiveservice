package svca

import (
	"fmt"
	"sync"

	as "github.com/godevsig/adaptiveservice"
	svcb "github.com/godevsig/adaptiveservice/examples/msgtracing/service_b"
	svcc "github.com/godevsig/adaptiveservice/examples/msgtracing/service_c"
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
	connb := <-c.Discover("example", "serviceB")
	if connb == nil {
		return as.ErrServiceNotFound("example", "serviceB")
	}
	defer connb.Close()

	reqb := svcb.Request{Input: "depend"}
	var repb svcb.Reply
	if err := connb.SendRecv(&reqb, &repb); err != nil {
		return err
	}

	connc := <-c.Discover("example", "serviceC")
	if connc == nil {
		return as.ErrServiceNotFound("example", "serviceC")
	}
	defer connc.Close()

	reqc := svcc.Request{Input: "depend"}
	var repc svcc.Reply
	if err := connc.SendRecv(&reqc, &repc); err != nil {
		return err
	}

	return &Reply{Output: fmt.Sprintf("%s %s service a(%s, %s)",
		req.Input, svc.info, repb.Output, repc.Output)}
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
	if err := s.Publish("serviceA",
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
