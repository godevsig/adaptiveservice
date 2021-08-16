package client

import (
	"fmt"
	"sync"
	"time"

	as "github.com/godevsig/adaptiveservice"
	echo "github.com/godevsig/adaptiveservice/examples/echo/server"
)

// Run runs the client.
func Run(lg as.Logger) {
	var opts []as.Option
	opts = append(opts, as.WithLogger(lg))

	c := as.NewClient(opts...)
	conn := <-c.Discover("example.org", "echo.v1.0")
	defer conn.Close()

	var rep echo.MessageReply
	req := echo.MessageRequest{
		Msg: "ni hao",
		Num: 100,
	}
	for i := 0; i < 10; i++ {
		req.Num += 100
		if err := conn.SendRecv(&req, &rep); err != nil {
			fmt.Println(err)
		}
		fmt.Printf("%v ==> %v, %s\n", req, rep.MessageRequest, rep.Signature)
		time.Sleep(time.Second)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stream := conn.NewStream()
			req := echo.MessageRequest{
				Msg: "ni hao",
				Num: 10 * int32(i),
			}
			var rep echo.MessageReply
			if err := stream.SendRecv(&req, &rep); err != nil {
				fmt.Println(err)
			}
			fmt.Printf("%v ==> %v, %s\n", req, rep.MessageRequest, rep.Signature)
		}(i)
	}
	wg.Wait()
}
