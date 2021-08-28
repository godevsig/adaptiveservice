package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	as "github.com/godevsig/adaptiveservice"
)

type subCmd struct {
	*flag.FlagSet
	action func() int
}

func trimName(name string, size int) string {
	if len(name) > size {
		name = name[:size-3] + "..."
	}
	return name
}

func getSelfID(opts []as.Option) (selfID string, err error) {
	opts = append(opts, as.WithScope(as.ScopeProcess|as.ScopeOS))
	c := as.NewClient(opts...).SetDiscoverTimeout(0)
	conn := <-c.Discover(as.BuiltinPublisher, "providerInfo")
	if conn == nil {
		err = as.ErrServiceNotFound
		return
	}
	defer conn.Close()

	err = conn.SendRecv(&as.ReqProviderInfo{}, &selfID)
	return
}

func main() {
	var cmds []subCmd
	{
		cmd := flag.NewFlagSet("server", flag.ExitOnError)
		cmd.SetOutput(os.Stdout)

		debug := cmd.Bool("d", false, "enable debug")
		rootRegistry := cmd.Bool("root", false, "enable root registry service")
		reverseProxy := cmd.Bool("proxy", false, "enable reverse proxy service")
		serviceLister := cmd.Bool("lister", false, "enable service lister service")
		registryAddr := cmd.String("registry", "", "root registry address")
		lanBroadcastPort := cmd.String("bcast", "", "broadcast port for LAN")

		action := func() int {
			cmd.Parse(os.Args[2:])
			if len(*registryAddr) == 0 {
				panic("root registry address not set")
			}
			if len(*lanBroadcastPort) == 0 {
				panic("lan broadcast port not set")
			}

			var opts []as.Option
			opts = append(opts, as.WithRegistryAddr(*registryAddr))
			if *debug {
				opts = append(opts, as.WithLogger(as.LoggerAll{}))
			}

			s := as.NewServer(opts...)
			s.SetBroadcastPort(*lanBroadcastPort)

			if *rootRegistry {
				s.EnableRootRegistry()
			}
			if *reverseProxy {
				s.EnableAutoReverseProxy()
			}
			if *serviceLister {
				s.EnableServiceLister()
			}

			s.Serve()
			return 0
		}

		cmds = append(cmds, subCmd{cmd, action})
	}
	{
		cmd := flag.NewFlagSet("list", flag.ExitOnError)
		cmd.SetOutput(os.Stdout)

		debug := cmd.Bool("d", false, "enable debug")
		verbose := cmd.Bool("v", false, "show verbose info")
		publisher := cmd.String("p", "*", "publisher name, can be wildcard")
		service := cmd.String("s", "*", "service name, can be wildcard")

		action := func() int {
			cmd.Parse(os.Args[2:])
			var opts []as.Option
			if *debug {
				opts = append(opts, as.WithLogger(as.LoggerAll{}))
			}
			selfID, err := getSelfID(opts)
			if err != nil {
				fmt.Println(err)
				return 1
			}

			c := as.NewClient(opts...)
			conn := <-c.Discover(as.BuiltinPublisher, "serviceLister")
			if conn == nil {
				fmt.Println(as.ErrServiceNotFound)
				return 1
			}
			defer conn.Close()

			msg := as.ListService{TargetScope: as.ScopeAll, Publisher: *publisher, Service: *service}
			var scopes [4][]*as.ServiceInfo
			if err := conn.SendRecv(&msg, &scopes); err != nil {
				fmt.Println(err)
				return 1
			}
			if *verbose {
				for _, services := range scopes {
					for _, svc := range services {
						if svc.ProviderID == selfID {
							svc.ProviderID = "self"
						}
						fmt.Printf("PUBLISHER: %s\n", svc.Publisher)
						fmt.Printf("SERVICE  : %s\n", svc.Service)
						fmt.Printf("PROVIDER : %s\n", svc.ProviderID)
						addr := svc.Addr
						if addr[len(addr)-1] == 'P' {
							addr = addr[:len(addr)-1] + "(proxied)"
						}
						fmt.Printf("ADDRESS  : %s\n\n", addr)
					}
				}
			} else {
				list := make(map[string]*as.Scope)
				for i, services := range scopes {
					for _, svc := range services {
						if svc.ProviderID == selfID {
							svc.ProviderID = "self"
						}
						k := svc.Publisher + "_" + svc.Service + "_" + svc.ProviderID
						p, has := list[k]
						if !has {
							v := as.Scope(0)
							p = &v
							list[k] = p
						}
						*p = *p | 1<<i
					}
				}
				names := make([]string, 0, len(list))
				for name := range list {
					names = append(names, name)
				}
				sort.Strings(names)
				fmt.Println("PUBLISHER           SERVICE             PROVIDER      WLOP(SCOPE)")
				for _, svc := range names {
					p := list[svc]
					if p == nil {
						panic("nil p")
					}
					ss := strings.Split(svc, "_")
					fmt.Printf("%-18s  %-18s  %-12s  %4b\n", trimName(ss[0], 18), trimName(ss[1], 18), ss[2], *p)
				}
			}
			return 0
		}

		cmds = append(cmds, subCmd{cmd, action})
	}
	{
		cmd := flag.NewFlagSet("id", flag.ExitOnError)
		cmd.SetOutput(os.Stdout)

		debug := cmd.Bool("d", false, "enable debug")

		action := func() int {
			cmd.Parse(os.Args[2:])
			var opts []as.Option
			if *debug {
				opts = append(opts, as.WithLogger(as.LoggerAll{}))
			}
			selfID, err := getSelfID(opts)
			if err != nil {
				fmt.Println(err)
				return 1
			}
			fmt.Println(selfID)
			return 0
		}

		cmds = append(cmds, subCmd{cmd, action})
	}

	usage := func(exitCode int) {
		fmt.Println("COMMAND [OPTIONS]")
		for _, cmd := range cmds {
			fmt.Println(cmd.Name() + ":")
			cmd.PrintDefaults()
		}
		os.Exit(exitCode)
	}

	if len(os.Args) < 2 {
		usage(1)
	}

	str := os.Args[1]
CMD:
	switch str {
	case "-h", "--help":
		usage(0)
	default:
		for _, cmd := range cmds {
			if str == cmd.Name() {
				os.Exit(cmd.action())
				break CMD
			}
		}
		usage(1)
	}
}
