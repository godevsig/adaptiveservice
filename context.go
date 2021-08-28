package adaptiveservice

import (
	"reflect"
)

// Context represents a context.
type Context interface {
	// PutVar puts value v to the underlying map overriding the old value of the same type.
	PutVar(v interface{})

	// GetVar gets value that v points to from the underlying map, it panics if v
	// is not a non-nil pointer.
	// The value that v points to will be set to the value in the context if value
	// of the same type has been putted to the map,
	// otherwise zero value will be set.
	GetVar(v interface{})

	// SetContext sets the context with value v which supposedly is a pointer to
	// an instance of the struct associated to the connection.
	// It panics if v is not a non-nil pointer.
	// It is supposed to be called only once upon a new connection is connected.
	SetContext(v interface{})
	// GetContext gets the context that has been set by SetContext.
	GetContext() interface{}
}

type contextImpl struct {
	kv  map[reflect.Type]interface{}
	ctx interface{}
}

func (c *contextImpl) PutVar(v interface{}) {
	if c.kv == nil {
		c.kv = make(map[reflect.Type]interface{})
	}
	c.kv[reflect.TypeOf(v)] = v
}

func (c *contextImpl) GetVar(v interface{}) {
	rptr := reflect.ValueOf(v)
	if rptr.Kind() != reflect.Ptr || rptr.IsNil() {
		panic("not a pointer or nil pointer")
	}
	rv := rptr.Elem()
	tp := rv.Type()
	if c.kv != nil {
		if i, ok := c.kv[tp]; ok {
			rv.Set(reflect.ValueOf(i))
			return
		}
	}

	rv.Set(reflect.Zero(tp))
}

func (c *contextImpl) SetContext(v interface{}) {
	rptr := reflect.ValueOf(v)
	if rptr.Kind() != reflect.Ptr || rptr.IsNil() {
		panic("not a pointer or nil pointer")
	}
	c.ctx = v
}

func (c *contextImpl) GetContext() interface{} {
	return c.ctx
}
