// Package adaptiveservice is a message oriented micro service framework.
//
// Servers define micro services identified as name of "publisher_service" and
// publish them to all available scopes:
// in same process, and further in same OS, and then further in same local
// network, and then public network where a public root registry address needs
// to be configured.
// In process and OS scope, one service name can only be announced once,
// duplicated service name is treated as error.
// In network scope, there can be multiple services with the same name,
// in this case, each service provider publishes the service "publisher_service"
// along with an unique provider ID.
//
// Clients then discover wanted micro services in a way that shortest scope comes
// first. The discover() API returns a connection channel, reading the channel the
// client will get one or more connections, with each represents a connection
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
// the message's Handle() is called.
// Clients do not define Handle() method, they just send and receive message
// in a natural synchronized fashion.
//
// Services that are behind NAT can be auto proxied by the builtin reverseProxy
// service provided by the daemon server in the local network or by the root
// registry.
package adaptiveservice

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niubaoshu/gotiny"
)

// History:
// "1.0": add client and server handshake immediately after netconnection is established.
//        This will be incompatible with previous versions, servers will not decline and
//        close the connection with client if the handshake can not be made.
const specVersion = "1.0"

// Scope is publishing and discovering scope
type Scope uint16

const (
	// BuiltinPublisher name
	BuiltinPublisher = "builtin"
	// OK can be returned by known messages as reply to indicate
	// everything is OK. Client should use type int to receive it.
	OK            = 0
	asTmpDir      = "/tmp/adaptiveservice"
	timeMicro     = "15:04:05.000000"
	dateTimeMicro = "2006-01-02 15:04:05.000000"
	timeNano      = "15:04:05.000000000"
	dateTimeNano  = "2006-01-02 15:04:05.000000000"
)

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
	// ScopeNetwork is a shortcut for ScopeLAN and ScopeWAN
	ScopeNetwork = ScopeLAN | ScopeWAN
	// ScopeAll includes all scopes, this is the default value if
	// no other Scope specified.
	ScopeAll = ScopeProcess | ScopeOS | ScopeLAN | ScopeWAN
)

var (
	// ErrServiceNotReachable is an error where the service exists
	// but somehow can not be reached, e.g. the service is behind NAT.
	ErrServiceNotReachable = errors.New("service not reachable")
	// ErrConnReset is an error where the connection was forced closed
	// by peer.
	ErrConnReset = errors.New("connection reset by peer")
	// ErrServerClosed is an error where the server was closed by signal.
	ErrServerClosed = errors.New("server closed by signal")
	// ErrRecvTimeout is an error where no data was received within
	// specified duration.
	ErrRecvTimeout = errors.New("receive timeout")
	// ErrMsgTooLarge is an error when sending too large message.
	ErrMsgTooLarge = errors.New("message size exceeds uint32")
)

type errServiceNotFound struct {
	publisher string
	service   string
}

func (e errServiceNotFound) Error() string {
	return "service not found: " + toPublisherServiceName(e.publisher,e.service)
}

// ErrServiceNotFound returns an error that no wanted service was found
func ErrServiceNotFound(publisher, service string) error {
	return errServiceNotFound{publisher, service}
}

type streamIO struct {
	stream Stream
	rbuff  []byte
}

func (sio *streamIO) Read(p []byte) (n int, err error) {
	if len(sio.rbuff) == 0 {
		if err := sio.stream.Recv(&sio.rbuff); err != nil {
			return 0, err
		}
	}
	n = copy(p, sio.rbuff)
	sio.rbuff = sio.rbuff[n:]
	return
}

func (sio *streamIO) Write(p []byte) (n int, err error) {
	if err := sio.stream.Send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (sio *streamIO) Close() error {
	return sio.stream.Send(io.EOF)
}

// NewStreamIO wraps the stream to be an io.ReadWriteCloser in which
// Read() is a Stream.Recv() that only receives []byte,
// Write is a Stream.Send() that only sends []byte.
// Use Read() Write() in pair on the client/server peer, don't mix use
// them with Send() or Recv().
func NewStreamIO(stream Stream) io.ReadWriteCloser {
	return &streamIO{stream: stream}
}

// Logger is the logger interface.
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// LoggerNull prints no log
type LoggerNull struct{}

// Debugf is Debugf
func (LoggerNull) Debugf(format string, args ...interface{}) {}

// Infof is Infof
func (LoggerNull) Infof(format string, args ...interface{}) {}

// Warnf is Warnf
func (LoggerNull) Warnf(format string, args ...interface{}) {}

// Errorf is Errorf
func (LoggerNull) Errorf(format string, args ...interface{}) {}

// LoggerAll prints all regardless of loglevel
type LoggerAll struct{}

// Debugf is Debugf
func (LoggerAll) Debugf(format string, args ...interface{}) {
	h := fmt.Sprintf("%v <<%v>> ", time.Now().Format(timeNano), goID())
	fmt.Printf(h+"[AS DEBUG] "+format+"\n", args...)
}

// Infof is Infof
func (LoggerAll) Infof(format string, args ...interface{}) {
	h := fmt.Sprintf("%v <<%v>> ", time.Now().Format(timeNano), goID())
	fmt.Printf(h+"[AS INFO] "+format+"\n", args...)
}

// Warnf is Warnf
func (LoggerAll) Warnf(format string, args ...interface{}) {
	h := fmt.Sprintf("%v <<%v>> ", time.Now().Format(timeNano), goID())
	fmt.Printf(h+"[AS WARN] "+format+"\n", args...)
}

// Errorf is Errorf
func (LoggerAll) Errorf(format string, args ...interface{}) {
	h := fmt.Sprintf("%v <<%v>> ", time.Now().Format(timeNano), goID())
	fmt.Printf(h+"[AS ERROR] "+format+"\n", args...)
}

type errorRecover interface {
	Error() error
	String() string
	Recover() (recovered bool) // return true if the error has been recovered.
}

type noError struct{}

func (e noError) Error() error {
	return nil
}

func (e noError) String() string {
	return "no error"
}

func (e noError) Recover() bool {
	return false
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

// allows io.Closer to be closer
type ioCloser func() error

func (c ioCloser) close() {
	c()
}

func addSigCloser(c closer) {
	sigCleaner.Lock()
	sigCleaner.closers = append(sigCleaner.closers, c)
	sigCleaner.Unlock()
}

func initSigCleaner(lg Logger) {
	sigOnce.Do(func() {
		// handle signal
		signal.Ignore(syscall.SIGHUP)
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			sig := <-sigChan
			lg.Warnf("signal: %s", sig.String())
			sigCleaner.Lock()
			for _, c := range sigCleaner.closers {
				c.close()
			}
			sigCleaner.Unlock()
		}()
	})
}

var name2Type = map[string]reflect.Type{}

// RegisterType registers the type infomation to encoding sub system.
func RegisterType(i interface{}) {
	//gotiny.Register(i)
	rt := reflect.TypeOf(i)
	name := rt.String()
	gotiny.RegisterName(name, rt)
	name2Type[name] = rt
}

// RegisterTypeNoPanic is like RegisterType but recovers from panic.
func RegisterTypeNoPanic(i interface{}) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	RegisterType(i)
	return
}

// GetKnownMessageTypes returns all KnownMessage types
func GetKnownMessageTypes() []string {
	var registered []string
	for name, rt := range name2Type {
		if m, ok := rt.MethodByName("Handle"); ok {
			if strings.Contains(fmt.Sprintf("%v", m), "adaptiveservice.ContextStream") {
				registered = append(registered, name)
			}
		}
	}
	return registered
}

// GetRegisteredTypeByName returns a reflect.Type if it has been registered
func GetRegisteredTypeByName(name string) reflect.Type {
	if rt, ok := name2Type[name]; ok {
		return rt
	}
	return nil
}

// WildcardMatch checks if str matches the pattern with "*" as a wildcard
//   "*" matches all
//  "*bar*" matches bar, foobar, or foobarabc
//  "foo*abc*" matches foobarabc, foobarabc123, or fooabc
func WildcardMatch(pattern, str string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == "" {
		return str == ""
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == str
	}

	// Match the first segment (if not empty)
	begin := parts[0]
	if begin != "" {
		if !strings.HasPrefix(str, begin) {
			return false
		}
		str = str[len(begin):]
	}

	// Match the last segment (if not empty)
	end := parts[len(parts)-1]
	if end != "" {
		if !strings.HasSuffix(str, end) {
			return false
		}
		str = str[:len(str)-len(end)]
	}

	// Match the middle segments
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			continue
		}
		index := strings.Index(str, part)
		if index == -1 {
			return false
		}
		str = str[index+len(part):]
	}
	return true
}

func init() {
	rand.Seed(time.Now().UnixNano())
	// basic types
	RegisterType(int(0))
	RegisterType(int8(0))
	RegisterType(int16(0))
	RegisterType(int32(0))
	RegisterType(int64(0))
	RegisterType(uint(0))
	RegisterType(uint8(0))
	RegisterType(uint16(0))
	RegisterType(uint32(0))
	RegisterType(uint64(0))
	RegisterType(float32(0))
	RegisterType(float64(0))
	RegisterType(complex64(0i))
	RegisterType(complex128(0i))
	RegisterType(uintptr(0))
	RegisterType(false)
	RegisterType("")
	//RegisterType([]byte(nil))
	RegisterType([]int(nil))
	RegisterType([]int8(nil))
	RegisterType([]int16(nil))
	RegisterType([]int32(nil))
	RegisterType([]int64(nil))
	RegisterType([]uint(nil))
	RegisterType([]uint8(nil))
	RegisterType([]uint16(nil))
	RegisterType([]uint32(nil))
	RegisterType([]uint64(nil))
	RegisterType([]float32(nil))
	RegisterType([]float64(nil))
	RegisterType([]complex64(nil))
	RegisterType([]complex128(nil))
	RegisterType([]uintptr(nil))
	RegisterType([]bool(nil))
	RegisterType([]string(nil))
	// error types
	RegisterType(errors.New(""))
	RegisterType(fmt.Errorf("%w", io.EOF))
}

// Dummy annotation marking that the value x escapes,
// for use in cases where the reflect code is so clever that
// the compiler cannot follow.
func escapes(x interface{}) {
	if dummy.b {
		dummy.x = x
	}
}

var dummy struct {
	b bool
	x interface{}
}
