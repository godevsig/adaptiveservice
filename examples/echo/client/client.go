package client

import (
	"fmt"
	"time"

	as "github.com/godevsig/adaptiveservice"
	echo "github.com/godevsig/adaptiveservice/examples/echo/server"
)

// Run runs the client.
func Run() {
	c := as.NewClient(as.WithLogger(as.LoggerAll{}), as.WithRegistryAddr("10.182.105.138:11985"))
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
		fmt.Println(&rep)
		time.Sleep(time.Second)
	}

	for i := 0; i < 4; i++ {
		go func(i int) {
			stream := conn.NewStream()
			req := echo.MessageRequest{
				Msg: "ni hao",
				Num: int32(i),
			}
			var rep echo.MessageReply
			if err := stream.SendRecv(&req, &rep); err != nil {
				fmt.Println(err)
			}
			fmt.Println(&rep)
		}(i)
	}
}
