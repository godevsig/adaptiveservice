package main

import (
	"flag"

	as "github.com/godevsig/adaptiveservice"
)

func main() {
	var (
		debug            bool
		rootRegistry     bool
		registryAddr     string
		lanBroadcastPort string
		reverseProxy     bool
		serviceLister    bool
	)

	flag.BoolVar(&debug, "d", false, "enable debug")
	flag.BoolVar(&rootRegistry, "root", false, "enable root registry service")
	flag.BoolVar(&reverseProxy, "proxy", false, "enable reverse proxy service")
	flag.BoolVar(&serviceLister, "lister", false, "enable service lister service")
	flag.StringVar(&registryAddr, "registry", "", "root registry address")
	flag.StringVar(&lanBroadcastPort, "bcast", "", "broadcast port for LAN")

	flag.Parse()

	if len(registryAddr) == 0 {
		panic("root registry address not set")
	}
	if len(lanBroadcastPort) == 0 {
		panic("lan broadcast port not set")
	}

	var opts []as.Option
	opts = append(opts, as.WithRegistryAddr(registryAddr))
	if debug {
		opts = append(opts, as.WithLogger(as.LoggerAll{}))
	}

	s := as.NewServer(opts...)
	s.SetBroadcastPort(lanBroadcastPort)

	if rootRegistry {
		s.EnableRootRegistry()
	}
	if reverseProxy {
		s.EnableReverseProxy()
	}
	if serviceLister {
		s.EnableServiceLister()
	}

	s.Serve()
}
