package adaptiveservice

import (
	"io"
	"net"
)

// SrvReverseProxy : service reverseProxy
const SrvReverseProxy = "reverseProxy"

type proxyRegServiceInWAN struct {
	publisher  string
	service    string
	providerID string
}

func (msg *proxyRegServiceInWAN) Handle(stream ContextStream) (reply interface{}) {
	s := stream.GetContext().(*Server)
	chanServerConn := make(chan net.Conn)

	onServerConnection := func(netconn Netconn) bool {
		if chanServerConn != nil {
			chanServerConn <- netconn.(net.Conn)
		}
		return true
	}

	reversesvc := &service{
		s:           s,
		fnOnConnect: onServerConnection,
	}
	reversetran, err := reversesvc.newTCPTransport("")
	if err != nil {
		return err
	}
	s.addCloser(reversetran)
	_, port, _ := net.SplitHostPort(reversetran.lnr.Addr().String()) // from [::]:43807

	var proxytran *streamTransport
	go func() {
		if err := stream.Recv(nil); err != nil {
			s.lg.Infof("service cmdconn read lost: %v, closing its proxy", err)
			reversetran.close()
			close(chanServerConn)
			chanServerConn = nil
			if proxytran != nil {
				proxytran.close()
			}
		}
	}()

	onClientConnection := func(netconn Netconn) bool {
		clientConn := netconn.(net.Conn)
		s.lg.Debugf("reverse proxy: starting for client: %s", clientConn.RemoteAddr().String())
		if err := stream.Send(port); err != nil {
			s.lg.Infof("service cmdconn write lost: %v, closing its proxy", err)
			reversetran.close()
			proxytran.close()
			clientConn.Close()
			return true
		}
		if chanServerConn == nil {
			clientConn.Close()
			return true
		}
		serverConn := <-chanServerConn
		if serverConn == nil {
			clientConn.Close()
			return true
		}
		go func() {
			io.Copy(serverConn, clientConn)
			serverConn.Close()
			s.lg.Debugf("io copy client => server done")
		}()
		go func() {
			// acknowledge client real server connected
			clientConn.Write([]byte{0})
			io.Copy(clientConn, serverConn)
			clientConn.Close()
			s.lg.Debugf("io copy server => client done")
		}()
		return true
	}

	proxysvc := &service{
		publisherName: msg.publisher,
		serviceName:   msg.service,
		providerID:    msg.providerID,
		s:             s,
		scope:         ScopeWAN,
		fnOnConnect:   onClientConnection,
	}

	proxytran, err = proxysvc.newTCPTransport("")
	if err != nil {
		return err
	}
	s.addCloser(proxytran)

	return OK
}

// publishReverseProxyService declares the reverse proxy service.
func (s *Server) publishReverseProxyService(scope Scope) error {
	knownMsgs := []KnownMessage{(*proxyRegServiceInWAN)(nil)}
	return s.publish(scope, BuiltinPublisher, SrvReverseProxy,
		knownMsgs,
		OnNewStreamFunc(func(ctx Context) {
			ctx.SetContext(s)
		}))
}

func init() {
	RegisterType((*proxyRegServiceInWAN)(nil))
}
