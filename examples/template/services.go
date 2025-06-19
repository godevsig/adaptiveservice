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

// PublishTemplateService publishes the service
func PublishTemplateService(s *as.Server) error {
	svc := &service{info: "Hello"}
	if err := s.Publish("template",
		knownMsgs,
		as.OnNewStreamFunc(svc.onNewStream),
	); err != nil {
		return err
	}
	return nil
}
