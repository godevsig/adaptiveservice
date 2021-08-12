// Package adaptiveservice is a message oriented micro service framework.
//
// Servers define micro services identified as name of "publisher_service" and
// publish them to all available scopes:
// in same process, and further in same OS, and then further in same network.
// In process and OS scope, one service name can only be announced once,
// duplicated service name is treated as error.
// In network scope, there can be multiple services with the same name,
// in this case, each service provider publishes the service "publisher_service"
// along with an unique provider ID.
//
// Clients then discover wanted micro services in a way that shortest scope comes
// first. The discover() API returns a connection channel, reading the channel the
// client will get one or more connections, with each connection represents a connection
// to one of the service providers providing the wanted micro service.
// The connection then can be used to send/receive messages to/from the service provider.
//
// Connections can be multiplexed on client side: NewStream() API creates a new
// context in which the messages are transferred independently from other contexts
// over the same underlying connection. The intention of the multiplexer is to have
// scalability on client side: users use this mechanism to send parallel request
// messages towards the same service provider to increase execution concurrency.
//
// For server side, the incoming messages are handled in auto-scaled worker pool,
// so the multiplexer used on client side is not needed on server side.
// Servers listen to different transports for all available scopes:
//  process scope, go channels are used
//  OS scope, unix domain socket is used
//  network scope, tcp socket is used
//
// Messages that satisfy Handle() interface are known messages. Typically
// server defines Handle() method for every message type it can handle,
// then when the known message arrived on one of the transports it is
// listening, the message is delivered to one of the workers in which
// the message is then being handled.
// Clients do not define Handle() method, they just send and receive message
// in a natural synchronized fashion.
package adaptiveservice

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niubaoshu/gotiny"
)

// Scope is publishing and discovering scope
type Scope int

const (
	// ScopeProcess is a scope where publishing and discovering services
	// only happen in same process.
	ScopeProcess Scope = 1 << iota
	// ScopeOS is a scope where publishing and discovering services
	// only happen in same OS.
	ScopeOS
	// ScopeLAN is a scope where publishing and discovering services
	// only happen in same local network.
	ScopeLAN
	// ScopeWAN is a scope where publishing and discovering services
	// only happen in same reachable public network.
	ScopeWAN
	// ScopeAll includes all scopes.
	ScopeAll = ScopeProcess | ScopeOS | ScopeLAN | ScopeWAN

	// OK can be returned by known messages as reply to indicate
	// everything is OK. Client should use type int to receive it.
	OK = 0
)

var (
	// ErrServiceNotFound is an error where no wanted service found
	ErrServiceNotFound = errors.New("service not found")
	// ErrServiceNotReachable is an error where the service exists
	// but somehow can not be reached, e.g. the service is behind NAT.
	ErrServiceNotReachable = errors.New("service not reachable")
	// ErrBadMessage is an error that the message can not be handled.
	// It is either an unwanted known message or an unknown message
	// with no route to message handler.
	ErrBadMessage = errors.New("message not supported")
)

// Logger is the logger interface.
type Logger interface {
	Debugf(format string, args ...interface{})
	Debugln(args ...interface{})
	Infof(format string, args ...interface{})
	Infoln(args ...interface{})
	Warnf(format string, args ...interface{})
	Warnln(args ...interface{})
	Errorf(format string, args ...interface{})
	Errorln(args ...interface{})
}

type loggerNull struct{}

func (loggerNull) Debugf(format string, args ...interface{}) {}
func (loggerNull) Debugln(args ...interface{})               {}
func (loggerNull) Infof(format string, args ...interface{})  {}
func (loggerNull) Infoln(args ...interface{})                {}
func (loggerNull) Warnf(format string, args ...interface{})  {}
func (loggerNull) Warnln(args ...interface{})                {}
func (loggerNull) Errorf(format string, args ...interface{}) {}
func (loggerNull) Errorln(args ...interface{})               {}

// LoggerAll prints all regardless of loglevel
type LoggerAll struct{}

// Debugf is Debugf
func (LoggerAll) Debugf(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }

// Debugln is Debugln
func (LoggerAll) Debugln(args ...interface{}) { fmt.Println(args...) }

// Infof is Infof
func (LoggerAll) Infof(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }

// Infoln is Infoln
func (LoggerAll) Infoln(args ...interface{}) { fmt.Println(args...) }

// Warnf is Warnf
func (LoggerAll) Warnf(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }

// Warnln is Warnln
func (LoggerAll) Warnln(args ...interface{}) { fmt.Println(args...) }

// Errorf is Errorf
func (LoggerAll) Errorf(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }

// Errorln is Errorln
func (LoggerAll) Errorln(args ...interface{}) { fmt.Println(args...) }

type errorRecover interface {
	Error() error
	String() string
	Recover() (recovered bool) // return true if the error has been recovered.
}

type unrecoverableError struct {
	err error
}

func (e unrecoverableError) Error() error {
	return e.err
}

func (e unrecoverableError) String() string {
	return "unrecoverable error"
}

func (e unrecoverableError) Recover() bool {
	return false
}

type customErrorRecover struct {
	err         error
	str         string
	recoverFunc func() bool
}

func (e customErrorRecover) Error() error {
	return e.err
}

func (e customErrorRecover) String() string {
	return e.str
}

func (e customErrorRecover) Recover() bool {
	return e.recoverFunc()
}

var (
	sigCleaner struct {
		sync.Mutex
		closers []closer
	}

	sigOnce sync.Once
)

type closer interface {
	close()
}

func addSigCloser(c closer) {
	sigCleaner.Lock()
	sigCleaner.closers = append(sigCleaner.closers, c)
	sigCleaner.Unlock()
}

func initSigCleaner(lg Logger) {
	sigOnce.Do(func() {
		// handle signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM)
		go func() {
			sig := <-sigChan
			lg.Warnln("signal: ", sig.String())
			sigCleaner.Lock()
			for _, c := range sigCleaner.closers {
				c.close()
			}
			sigCleaner.Unlock()
		}()
	})
}

// RegisterType registers the type infomation to encoding sub system.
func RegisterType(i interface{}) {
	gotiny.Register(i)
}

func init() {
	rand.Seed(time.Now().UnixNano())
	RegisterType(errors.New(""))
	RegisterType(fmt.Errorf("%w", io.EOF))
}

// test if pattern matches str
//   "*" matches all
//  "*bar*" matches bar, foobar, or foobarabc
//  "foo*abc*" matches foobarabc, foobarabc123, or fooabc
func wildcardMatch(pattern, str string) bool {
	if len(pattern) == 0 {
		return false
	}
	strs := strings.Split(pattern, "*")
	var pos, index int
	if index = strings.Index(str, strs[0]); index != 0 {
		return false
	}
	end := strs[len(strs)-1]
	if index = strings.LastIndex(str, end); index+len(end) != len(str) {
		return false
	}
	for i, substr := range strs {
		if i == 0 || i == len(strs)-1 || len(substr) == 0 {
			continue
		}
		index = strings.Index(str[pos:], substr)
		if index == -1 {
			return false
		}
		pos += index + len(substr)
	}
	return true
}
