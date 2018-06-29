package cmds

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/ipfs/go-ipfs-cmdkit"
	"github.com/ipfs/go-ipfs-cmds/debug"
)

func NewChanResponsePair(req *Request) (ResponseEmitter, Response) {
	ch := make(chan interface{})
	wait := make(chan struct{})

	r := &chanResponse{
		req:  req,
		ch:   ch,
		wait: wait,
	}

	re := (*chanResponseEmitter)(r)

	return re, r
}

// chanStream is the struct of both the Response and ResponseEmitter.
// The methods are defined on chanResponse and chanResponseEmitter, which are
// just type definitions on chanStream.
type chanStream struct {
	req *Request

	// rl is a lock for reading calls, i.e. Next.
	rl sync.Mutex

	// ch is used to send values from emitter to response.
	// When Emit received a channel close, it sets it to nil.
	// It is protected by rl.
	ch chan interface{}

	// wl is a lock for writing calls, i.e. Emit, Close(WithError) and SetLength.
	wl sync.Mutex

	// closed stores whether this stream is closed.
	// It is protected by wl.
	closed bool

	// wait is closed when the stream is closed or the first value is emitted.
	// Error and Length both wait for wait to be closed.
	// It is protected by wl.
	wait chan struct{}

	// err is the error that the stream was closed with.
	// It is written once under lock wl, but only read after wait is closed (which also happens under wl)
	err error

	// length is the length of the response.
	// It can be set by calling SetLength, but only before the first call to Emit, Close or CloseWithError.
	length uint64
}

type chanResponse chanStream

func (r *chanResponse) Request() *Request {
	return r.req
}

func (r *chanResponse) Error() *cmdkit.Error {
	<-r.wait

	if r.err == nil || r.err == io.EOF {
		return nil
	}

	if e, ok := r.err.(*cmdkit.Error); ok {
		return e
	}

	return &cmdkit.Error{Message: r.err.Error()}
}

func (r *chanResponse) Length() uint64 {
	<-r.wait

	return r.length
}

func (r *chanResponse) Next() (interface{}, error) {
	if r == nil {
		return nil, io.EOF
	}

	var ctx context.Context
	if rctx := r.req.Context; rctx != nil {
		ctx = rctx
	} else {
		ctx = context.Background()
	}

	// to avoid races by setting r.ch to nil
	r.rl.Lock()
	defer r.rl.Unlock()

	select {
	case v, ok := <-r.ch:
		if !ok {
			return nil, r.err
		}

		switch val := v.(type) {
		case Single:
			return val.Value, nil
		default:
			return v, nil
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type chanResponseEmitter chanResponse

func (re *chanResponseEmitter) Emit(v interface{}) error {
	// channel emission iteration
	if ch, ok := v.(chan interface{}); ok {
		v = (<-chan interface{})(ch)
	}
	if ch, isChan := v.(<-chan interface{}); isChan {
		return EmitChan(re, ch)
	}

	re.wl.Lock()
	defer re.wl.Unlock()

	if _, ok := v.(Single); ok {
		defer re.closeWithError(nil)
	}

	// Initially this library allowed commands to return errors by sending an
	// error value along a stream. We removed that in favour of CloseWithError,
	// so we want to make sure we catch situations where some code still uses the
	// old error emitting semantics and _panic_ in those situations.
	debug.AssertNotError(v)

	// unblock Length() and Error()
	select {
	case <-re.wait:
	default:
		close(re.wait)
	}

	// make sure we check whether the stream is closed *before accessing re.ch*!
	// re.ch is set to nil, but is not protected by a shared mutex (because that
	// wouldn't make sense).
	// re.closed is set in a critical section protected by re.wl (we also took
	// that lock), so we can be sure that this check is not racy.
	if re.closed {
		return ErrClosedEmitter
	}

	ctx := re.req.Context

	select {
	case re.ch <- v:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (re *chanResponseEmitter) Close() error {
	return re.CloseWithError(nil)
}

func (re *chanResponseEmitter) SetLength(l uint64) {
	re.wl.Lock()
	defer re.wl.Unlock()

	// don't change value after emitting or closing
	select {
	case <-re.wait:
	default:
		re.length = l
	}
}

func (re *chanResponseEmitter) CloseWithError(err error) error {
	re.wl.Lock()
	defer re.wl.Unlock()

	if re.closed {
		return errors.New("close of closed emitter")
	}

	re.closeWithError(err)
	return nil
}

func (re *chanResponseEmitter) closeWithError(err error) {
	re.closed = true

	if err == nil {
		err = io.EOF
	}

	if e, ok := err.(cmdkit.Error); ok {
		err = &e
	}

	re.err = err
	close(re.ch)

	// unblock Length() and Error()
	select {
	case <-re.wait:
	default:
		close(re.wait)
	}
}
