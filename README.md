# adaptiveservice

adaptiveservice is a message oriented micro service framework.

[adaptiveservice go doc](https://pkg.go.dev/github.com/godevsig/adaptiveservice)

# hello example

## demo

```
# start server, waiting requests from clients, ctrl+c to exit
$ go run server/helloserver.go

# start client in another terminal, client discovers the server then sends request and prints the reply
$ go run client/helloclient.go
I am hello server, John
```

We can also start client before starting the server to get the same result.

## client programing

The client imports the server's messages definitions:

```
import msg "github.com/godevsig/adaptiveservice/examples/hello/message"
```

Then we new a client which discovers the service identified by `{"example", "hello"}` where "example" is the publisher of the service, "hello" is the name of the service. When successfully discovered, a none-nil connection towards that service is established.

```
c := as.NewClient()

conn := <-c.Discover("example", "hello")
if conn == nil {
	fmt.Println(as.ErrServiceNotFound("example", "hello"))
	return
}
defer conn.Close()
```

Then we can use the connection to send our request `msg.HelloRequest`, and optionally wait for the reply `msg.HelloReply`:

```
request := msg.HelloRequest{Who: "John", Question: "who are you"}
var reply msg.HelloReply
if err := conn.SendRecv(request, &reply); err != nil {
	fmt.Println(err)
	return
}
fmt.Println(reply.Answer)
```
