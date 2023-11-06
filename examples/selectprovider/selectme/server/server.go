package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"

	as "github.com/godevsig/adaptiveservice"
	"github.com/godevsig/adaptiveservice/examples/selectprovider/selectme"
)

func main() {
	var debug bool
	var bcast int
	var residentWorkers, qWeight int
	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.IntVar(&bcast, "b", 9923, "bcast port")
	flag.IntVar(&residentWorkers, "r", 0, "resident workers for message queue")
	flag.IntVar(&qWeight, "w", 0, "message queue weight")
	flag.Parse()

	genID := func() string {
		b := make([]byte, 6)
		rand.Read(b)

		id := hex.EncodeToString(b)
		return id
	}

	var opts []as.Option
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}
	opts = append(opts, as.WithProviderID(genID()))

	s := as.NewServer(opts...).SetBroadcastPort(fmt.Sprint(bcast)).SetScaleFactors(residentWorkers, 0, qWeight)
	err := selectme.RunService(s)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("done")
}
