package template

import (
	"sync"

	as "github.com/godevsig/adaptiveservice"
)

type service struct {
	sync.RWMutex
	info string
}

func (svc *service) onNewStream(ctx as.Context) {
	ctx.SetContext(svc)
}

// RunTemplateService runs the service.
func RunTemplateService(opts []as.Option) error {
	s := as.NewServer(opts...).SetPublisher("example")

	svc := &service{info: "Hello"}
	if err := s.Publish("template",
		knownMsgs,
		as.OnNewStreamFunc(svc.onNewStream),
	); err != nil {
		return err
	}

	// ctrl+c to exit
	return s.Serve()
}
