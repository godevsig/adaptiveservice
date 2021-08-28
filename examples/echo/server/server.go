package server

import (
	"fmt"
	"sync"

	as "github.com/godevsig/adaptiveservice"
)

type statMgr struct {
	sync.RWMutex
	clients     map[string]struct{}
	subscribers map[chan string]struct{}
	sessionNum  int32
	counter     int64
}

// Run runs the server.
func Run(opts []as.Option) {
	s := as.NewServer(opts...).SetPublisher("example.org")

	mgr := &statMgr{
		clients:     make(map[string]struct{}),
		subscribers: make(map[chan string]struct{}),
	}
	if err := s.Publish("echo.v1.0",
		echoKnownMsgs,
		as.OnNewStreamFunc(mgr.onNewStream),
		as.OnConnectFunc(mgr.onConnect),
		as.OnDisconnectFunc(mgr.onDisconnect),
	); err != nil {
		fmt.Println(err)
		return
	}

	if err := s.Serve(); err != nil { // ctrl+c to exit
		fmt.Println(err)
	}
	fmt.Printf("echo server has served %d requests\n", mgr.counter)
}
