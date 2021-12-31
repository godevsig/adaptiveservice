package client

import (
	"fmt"
	"os"
	"sync"
	"time"

	as "github.com/godevsig/adaptiveservice"
	echo "github.com/godevsig/adaptiveservice/examples/echo/server"
)

// Run runs the client.
func Run(cmd string, opts []as.Option) {
	c := as.NewClient(opts...).SetDeepCopy()
	conn := <-c.Discover(echo.Publisher, echo.ServiceEcho)
	if conn == nil {
		fmt.Println(as.ErrServiceNotFound(echo.Publisher, echo.ServiceEcho))
		os.Exit(1)
	}
	defer conn.Close()

	if cmd == "timeout" {
		stream := conn.NewStream()
		fmt.Println("No timeout by default")
		if err := stream.SendRecv(echo.MessageTimeout{}, nil); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("Recv OK")

		fmt.Println("Set timeout to 15s")
		stream.SetRecvTimeout(15 * time.Second)
		if err := stream.SendRecv(echo.MessageTimeout{}, nil); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("Recv OK")

		fmt.Println("Set timeout to 3s")
		stream.SetRecvTimeout(3 * time.Second)
		if err := stream.SendRecv(echo.MessageTimeout{}, nil); err != nil {
			fmt.Println(err)
		} else {
			fmt.Println("Some error happend")
		}
		if err := stream.SendRecv(echo.MessageTimeout{}, nil); err != nil {
			fmt.Println(err)
		} else {
			fmt.Println("Some error happend")
		}

		fmt.Println("Set back to no timeout")
		stream.SetRecvTimeout(0)
		if err := stream.SendRecv(echo.MessageTimeout{}, nil); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("Recv OK")

		return
	}

	if cmd == "whoelse" {
		go func() {
			eventStream := conn.NewStream()
			if err := eventStream.Send(echo.SubWhoElseEvent{}); err != nil {
				fmt.Println(err)
				return
			}
			for {
				var addr string
				if err := eventStream.Recv(&addr); err != nil {
					fmt.Println(err)
					return
				}
				fmt.Printf("event: new client %s\n", addr)
			}
		}()

		for i := 0; i < 200; i++ {
			var whoelse string
			if err := conn.SendRecv(echo.WhoElse{}, &whoelse); err != nil {
				fmt.Println(err)
				return
			}
			fmt.Printf("clients: %s\n", whoelse)
			time.Sleep(3 * time.Second)
		}
		return
	}

	var rep echo.MessageReply
	req := echo.MessageRequest{
		Msg: "ni hao",
		Num: 0,
	}
	for i := 0; i < 9; i++ {
		req.Num += 100
		if err := conn.SendRecv(&req, &rep); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("%v ==> %v, %s\n", req, rep.MessageRequest, rep.Signature)
		//time.Sleep(time.Second)
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stream := conn.NewStream()
			req := echo.MessageRequest{
				Msg: "ni hao",
				Num: 100 * int32(i),
			}
			var rep echo.MessageReply
			for i := 0; i < 9; i++ {
				req.Num += 10
				if err := stream.SendRecv(&req, &rep); err != nil {
					fmt.Println(err)
					return
				}
				if req.Num+1 != rep.Num {
					panic("wrong number")
				}
				fmt.Printf("%v ==> %v, %s\n", req, rep.MessageRequest, rep.Signature)
				//time.Sleep(time.Second)
			}
		}(i)
	}
	wg.Wait()
}
