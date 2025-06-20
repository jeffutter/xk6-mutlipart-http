package multipartHTTP

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grafana/sobek"
	"github.com/jeffutter/xk6-mutlipart-http/events"
	"github.com/mstoykov/k6-taskqueue-lib/taskqueue"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/metrics"
)

type MultipartSubscriptionAPI struct { //nolint:revive
	vu modules.VU

	// exports is the exported type.
	// Exports is the type for our custom API.
	exports *sobek.Object

	httpMultipartMetrics *HTTPMultipartMetrics

	holder *ObjectHolder `js:"holder"` // used to keep the object alive
}

// MultipartSubscription represents an instance of the JS module for every VU.
type MultipartSubscription struct {
	// vu provides methods for accessing internal k6 objects for a VU
	vu modules.VU

	url *url.URL
	tq  *taskqueue.TaskQueue
	obj *sobek.Object // the object that is given to js to interact with the Request

	tagsAndMeta          *metrics.TagsAndMeta
	builtinMetrics       *metrics.BuiltinMetrics
	httpMultipartMetrics *HTTPMultipartMetrics
	started              time.Time

	done chan struct{}

	eventListeners *eventListeners

	// fields that should be seen by js only be updated on the event loop
	readyState      ReadyState
	readyStateMutex *sync.Mutex // mutex to protect the readyState field

	requestID uuid.UUID

	holder *ObjectHolder // used to keep the object alive
}

type ReadyState uint8

const (
	// CONNECTING is the state while the web socket is connecting
	CONNECTING ReadyState = iota
	// OPEN is the state after the connection is established and before it starts closing
	OPEN
	// CLOSING is while the connection is closing but is *not* closed yet
	CLOSING
	// CLOSED is when the connection is finally closed
	CLOSED
)

type Payload struct {
	// data string `json:"data"`
	data string
}

// Exports implements the modules.Instance interface and returns the exported types for the JS module.
func (r *MultipartSubscriptionAPI) Exports() modules.Exports {
	return modules.Exports{
		Default: r.exports,
	}
}

// Hold MultipartSubscription JS objects so they don't get GC'd until Closed
type ObjectHolder struct {
	objects map[uuid.UUID]sobek.Value
}

// InternalState holds basic metadata from the runtime state.
type InternalState struct {
	ActiveVUs       int64 `js:"activeVUs"`
	Iteration       int64
	VUID            uint64      `js:"vuID"`
	VUIDFromRuntime sobek.Value `js:"vuIDFromRuntime"`
}

// GetInternalState interrogates the current virtual user for state information.
func (r *MultipartSubscriptionAPI) GetInternalState() *InternalState {
	state := r.vu.State()
	ctx := r.vu.Context()
	es := lib.GetExecutionState(ctx)
	rt := r.vu.Runtime()

	return &InternalState{
		VUID:            state.VUID,
		VUIDFromRuntime: rt.Get("__VU"),
		Iteration:       state.Iteration,
		ActiveVUs:       es.GetCurrentlyActiveVUsCount(),
	}
}

func (r *MultipartSubscriptionAPI) multipartSubscription(c sobek.ConstructorCall) *sobek.Object {
	rt := r.vu.Runtime()

	url, err := parseURL(c.Argument(0))
	if err != nil {
		common.Throw(rt, err)
	}

	params := c.Argument(1)
	if err != nil {
		common.Throw(rt, err)
	}

	tagsAndMeta := r.vu.State().Tags.GetCurrentValues()

	s := &MultipartSubscription{
		vu:                   r.vu,
		url:                  url,
		tq:                   taskqueue.New(r.vu.RegisterCallback),
		readyState:           CONNECTING,
		readyStateMutex:      &sync.Mutex{},
		done:                 make(chan struct{}),
		obj:                  rt.NewObject(),
		eventListeners:       newEventListeners(),
		tagsAndMeta:          &tagsAndMeta,
		builtinMetrics:       r.vu.State().BuiltinMetrics,
		httpMultipartMetrics: r.httpMultipartMetrics,
		requestID:            uuid.New(),
		holder:               r.holder,
	}

	r.holder.objects[s.requestID] = s.obj

	s.defineMultipartHttp(rt)
	go s.establishConnection(*url, params)
	return s.obj
}

// Open establishes a http client connection based on the parameters provided.
func (ms *MultipartSubscription) establishConnection(url url.URL, args ...sobek.Value) {
	rt := ms.vu.Runtime()
	state := ms.vu.State()
	ms.started = time.Now()
	if state == nil {
		ms.tq.Close()
	}

	parsedArgs, err := parseConnectArgs(state, rt, args...)
	if err != nil {
		fmt.Println("Parse Connection Error: ", err, ". Args: ", args)
		ms.queueError(err)
		ms.tq.Close()
		return
	}

	if parsedArgs == nil {
		err := errors.New("failed to parse connection arguments")
		fmt.Println("Parse Connection Error: ", err, ". Args: ", args)
		ms.queueError(err)
		ms.tq.Close()
		return
	}

	urlString := url.String()
	client, err := ms.request(state, rt, urlString, parsedArgs)

	if err != nil {
		// Pass the error to the user script before exiting immediately
		if state.Options.Throw.Bool {
			// Pass the error to the user script before exiting immediately
			ms.tq.Queue(func() error {
				return ms.connectionClosedWithError(err)
			})
			ms.tq.Close()
			return
		}
		// If we don't throw but still have an error, don't continue with nil client
		ms.queueError(err)
		ms.tq.Close()
		return
	}

	// Additional safety check
	if client == nil {
		err := errors.New("client is nil after request")
		ms.queueError(err)
		ms.tq.Close()
		return
	}

	{
		ms.readyStateMutex.Lock()
		defer ms.readyStateMutex.Unlock()
		ms.readyState = OPEN
	}
	ms.queueOpen(time.Now())

	readEventChan := make(chan Payload)
	readErrChan := make(chan error)
	readCloseChan := make(chan int)

	// Wraps a couple of channels
	go client.readEvents(readEventChan, readErrChan, readCloseChan, client.resp.Header.Get("Content-Type"))

	// This is the main control loop. All JS code (including error handlers)
	// should only be executed by this thread to avoid race conditions
	go ms.loop(client, readEventChan, readErrChan, readCloseChan)
}

type message struct {
	// mtype int // message type consts as defined in gorilla/websocket/conn.go
	// data []byte
	data string
	t    time.Time
}

func (ms *MultipartSubscription) queueOpen(timestamp time.Time) {
	ms.tq.Queue(func() error {
		for _, openListener := range ms.eventListeners.all(events.OPEN) {
			if _, err := openListener(ms.newEvent(events.OPEN, timestamp)); err != nil {
				// _ = ms.conn.Close()                   // TODO log it?
				_ = ms.connectionClosedWithError(err) // TODO log it?
				return err
			}
		}
		return nil
	})
}

func (ms *MultipartSubscription) queueClose(timestamp time.Time) {
	ms.tq.Queue(func() error {
		for _, openListener := range ms.eventListeners.all(events.CLOSE) {
			if _, err := openListener(ms.newEvent(events.CLOSE, timestamp)); err != nil {
				// _ = ms.conn.Close()                   // TODO log it?
				_ = ms.connectionClosedWithError(err) // TODO log it?
				return err
			}
		}
		return nil
	})
}

func (ms *MultipartSubscription) queueMessage(msg *message) {
	if msg == nil {
		return
	}

	ms.tq.Queue(func() error {
		if ms.readyState != OPEN {
			return nil // TODO maybe still emit
		}
		// TODO maybe emit after all the listeners have fired and skip it if defaultPrevent was called?!?
		metrics.PushIfNotDone(ms.vu.Context(), ms.vu.State().Samples, metrics.Sample{
			TimeSeries: metrics.TimeSeries{
				Metric: ms.httpMultipartMetrics.HTTPMultipartMessagesReceived,
				Tags:   ms.tagsAndMeta.Tags,
			},
			Time:     msg.t,
			Metadata: ms.tagsAndMeta.Metadata,
			Value:    1,
		})

		rt := ms.vu.Runtime()
		ev := ms.newEvent(events.MESSAGE, msg.t)

		// data := rt.NewArrayBuffer(msg.data)
		must(rt, ev.DefineDataProperty("data", rt.ToValue(msg.data), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
		must(rt, ev.DefineDataProperty("origin", rt.ToValue(ms.url.String()), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))

		for _, messageListener := range ms.eventListeners.all(events.MESSAGE) {

			if _, err := messageListener(ev); err != nil {
				// _ = ms.conn.Close()                   // TODO log it?
				_ = ms.connectionClosedWithError(err) // TODO log it?
				return err
			}
		}
		return nil
	})
}

func (ms *MultipartSubscription) callEventListeners(eventType string) error {
	for _, listener := range ms.eventListeners.all(eventType) {
		// TODO the event here needs to be different and have an error (figure out it was for the close listeners)
		if _, err := listener(ms.newEvent(eventType, time.Now())); err != nil { // TODO fix timestamp
			return err
		}
	}
	return nil
}

func (ms *MultipartSubscription) callErrorListeners(e error) error {
	rt := ms.vu.Runtime()
	ev := ms.newEvent(events.ERROR, time.Now())

	must(rt, ev.DefineDataProperty("error", rt.ToValue(e.Error()), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	must(rt, ev.DefineDataProperty("origin", rt.ToValue(ms.url.String()), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))

	for _, errorListener := range ms.eventListeners.all(events.ERROR) {
		if _, err := errorListener(ev); err != nil { // TODO fix timestamp
			return err
		}
	}
	return nil
}
func (ms *MultipartSubscription) queueError(e error) {
	ms.tq.Queue(func() error {
		return ms.callErrorListeners(e)
	})
}

func (ms MultipartSubscription) loop(client *Client, readEventChan chan Payload, readErrChan chan error, readCloseChan chan int) {
	ctx := ms.vu.Context()

	defer func() {
		if client != nil {
			_ = client.closeResponseBody()
		}

		metrics.PushIfNotDone(ctx, ms.vu.State().Samples, metrics.Sample{
			TimeSeries: metrics.TimeSeries{
				Metric: ms.httpMultipartMetrics.HTTPMultipartSessionDuration,
				Tags:   ms.tagsAndMeta.Tags,
			},
			Time:     time.Now(),
			Metadata: ms.tagsAndMeta.Metadata,
			Value:    metrics.D(time.Since(ms.started)),
		})

		// Needed to allow the VU to quit when done
		ms.tq.Close()
	}()

	ctxDone := ctx.Done()
	for {
		select {
		case event := <-readEventChan:
			ms.vu.State().Logger.Debugf("[%s] Subscription message received: %s", ms.requestID, event.data)
			ms.queueMessage(&message{
				data: event.data,
				t:    time.Now(),
			})

		case readErr := <-readErrChan:
			ms.vu.State().Logger.Errorf("[%s] Subscription read error: %s", ms.requestID, readErr)
			ms.queueError(readErr)

		case <-ctxDone:
			ms.vu.State().Logger.Debugf("[%s] VU Shutting down, subscription messages will not be forwarded to VU", ms.requestID)
			if client != nil {
				_ = client.closeResponseBody()
			}
			ctxDone = nil

		case <-readCloseChan:
			ms.vu.State().Logger.Debugf("[%s] Subscription closing", ms.requestID)
			if client != nil {
				_ = client.closeResponseBody()
			}
			return

		case <-ms.done:
			ms.vu.State().Logger.Debugf("[%s] MultipartSubscription closed", ms.requestID)
			if client != nil {
				_ = client.closeResponseBody()
			}
			return

		case <-func() <-chan struct{} {
			if client != nil && client.done != nil {
				return client.done
			}
			// Return a channel that will never be ready if client.done is nil
			ch := make(chan struct{})
			return ch
		}():
			ms.vu.State().Logger.Debugf("[%s] Subscription closed", ms.requestID)
			return
		}
	}
}

func (ms *MultipartSubscription) request(state *lib.State, rt *sobek.Runtime, url string, args *httpOpenArgs) (*Client, error) {
	ctx := ms.vu.Context()

	subscriptionClient := Client{
		rt:        rt,
		ctx:       ctx,
		url:       url,
		done:      make(chan struct{}),
		Logger:    ms.vu.State().Logger,
		requestID: ms.requestID,
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			Dial: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).Dial,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DialContext:           state.Dialer.DialContext,
			Proxy:                 http.ProxyFromEnvironment,
			DisableCompression:    false, // Enable gzip compression support
			// TLSClientConfig: tlsConfig,
		},
	}

	httpMethod := http.MethodPost
	if args.method != "" {
		httpMethod = args.method
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, strings.NewReader(args.body))
	if err != nil {
		return &subscriptionClient, err
	}

	if args.headers != nil {
		for headerName, headerValues := range args.headers {
			for _, headerValue := range headerValues {
				req.Header.Set(headerName, headerValue)
				// fmt.Println("Setting Input header:", headerName, "=", headerValue)
			}
		}
	}
	// if the Accept header is not set, we set it to the default
	if _, ok := req.Header["Accept"]; !ok {
		req.Header.Set("Accept", `multipart/mixed; boundary="graphql"; subscriptionSpec=1.0, application/json`)
	}
	// if the Accept-Encoding header is not set, we set it to the default
	if _, ok := req.Header["Accept-Encoding"]; !ok {
		req.Header.Set("Accept-Encoding", "gzip, deflate")
	}

	// Remove the Accept-Encoding header. Only support utf-8 responses
	req.Header.Del("Accept-Encoding")

	resp, err := httpClient.Do(req)

	if resp != nil {
		subscriptionClient.resp = resp
	}

	metrics.PushIfNotDone(ctx, state.Samples, metrics.ConnectedSamples{
		Samples: []metrics.Sample{
			{
				TimeSeries: metrics.TimeSeries{Metric: ms.httpMultipartMetrics.HTTPMultipartSessions, Tags: ms.tagsAndMeta.Tags},
				Time:       time.Now(),
				Metadata:   ms.tagsAndMeta.Metadata,
				Value:      1,
			},
		},
		Tags: ms.tagsAndMeta.Tags,
		Time: time.Now(),
	})

	return &subscriptionClient, err
}

// to be run only on the eventloop
func (ms *MultipartSubscription) connectionClosedWithError(err error) error {
	ms.vu.State().Logger.Errorf("MultipartSubscription connection closed with error: %s, requestID: %s", err, ms.requestID)

	ms.close()

	if err != nil {
		if errList := ms.callErrorListeners(err); errList != nil {
			return errList // TODO ... still call the close listeners ?!?
		}
	}
	return ms.callEventListeners(events.CLOSE)
}

func (ms *MultipartSubscription) close() {
	ms.readyStateMutex.Lock()
	defer ms.readyStateMutex.Unlock()
	if ms.readyState != CLOSED {
		if ms.holder != nil && ms.holder.objects != nil {
			delete(ms.holder.objects, ms.requestID)
		}

		ms.vu.State().Logger.Info("Closing MultipartSubscription, requestID: ", ms.requestID)
		ms.readyState = CLOSED

		// Close the done channel only if it's not already closed
		select {
		case <-ms.done:
			// Channel is already closed
		default:
			close(ms.done)
		}
	}
}

// parseURL parses the url from the first constructor calls argument or returns an error
func parseURL(urlValue sobek.Value) (*url.URL, error) {
	if urlValue == nil || sobek.IsUndefined(urlValue) {
		return nil, errors.New("MutlipartHTTP Request requires a url")
	}

	urlString := urlValue.String()
	url, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("MutlipartHTTP Request requires valid url, but got %q which resulted in %w", urlString, err)
	}
	if url.Scheme != "http" && url.Scheme != "https" {
		return nil, fmt.Errorf("MutlipartHTTP Request requires url with scheme http or https, but got %q", url.Scheme)
	}
	if url.Fragment != "" {
		return nil, fmt.Errorf("MutlipartHTTP Request requires no url fragment, but got %q", url.Fragment)
	}

	return url, nil
}

// defineMultipartHttp defines all properties and methods for the MultipartHttp
func (ms *MultipartSubscription) defineMultipartHttp(rt *sobek.Runtime) {
	must(rt, ms.obj.DefineDataProperty("close", rt.ToValue(ms.close), sobek.FLAG_FALSE, sobek.FLAG_TRUE, sobek.FLAG_TRUE))
	must(rt, ms.obj.DefineDataProperty("url", rt.ToValue(ms.url.String()), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	must(rt, ms.obj.DefineDataProperty("requestID", rt.ToValue(ms.requestID.String()), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	must(rt, ms.obj.DefineDataProperty("addEventListener", rt.ToValue(ms.addEventListener), sobek.FLAG_FALSE, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	must(rt, ms.obj.DefineAccessorProperty("readyState", rt.ToValue(func() sobek.Value { return rt.ToValue((uint)(ms.readyState)) }), nil, sobek.FLAG_FALSE, sobek.FLAG_TRUE))

	setOn := func(property string, el *eventListener) {
		if el == nil {
			// this is generally should not happen, but we're being defensive
			common.Throw(rt, fmt.Errorf("not supported on-handler '%s'", property))
		}

		must(rt, ms.obj.DefineAccessorProperty(
			property, rt.ToValue(func() sobek.Value {
				return rt.ToValue(el.getOn)
			}), rt.ToValue(func(call sobek.FunctionCall) sobek.Value {
				arg := call.Argument(0)

				// it's possible to unset handlers by setting them to null
				if arg == nil || sobek.IsUndefined(arg) || sobek.IsNull(arg) {
					el.setOn(nil)

					return nil
				}

				fn, isFunc := sobek.AssertFunction(arg)
				if !isFunc {
					common.Throw(rt, fmt.Errorf("a value for '%s' should be callable", property))
				}

				el.setOn(func(v sobek.Value) (sobek.Value, error) { return fn(sobek.Undefined(), v) })

				return nil
			}), sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	}

	setOn("onmessage", ms.eventListeners.getType(events.MESSAGE))
	setOn("onerror", ms.eventListeners.getType(events.ERROR))
	setOn("onopen", ms.eventListeners.getType(events.OPEN))
	setOn("onclose", ms.eventListeners.getType(events.CLOSE))
}

// newEvent return an event implementing "implements" https://dom.spec.whatwg.org/#event
// needs to be called on the event loop
// TODO: move to events
func (ms *MultipartSubscription) newEvent(eventType string, t time.Time) *sobek.Object {
	rt := ms.vu.Runtime()
	o := rt.NewObject()

	must(rt, o.DefineAccessorProperty("type", rt.ToValue(func() string {
		return eventType
	}), nil, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	must(rt, o.DefineAccessorProperty("target", rt.ToValue(func() interface{} {
		return ms.obj
	}), nil, sobek.FLAG_FALSE, sobek.FLAG_TRUE))
	// skip srcElement
	// skip currentTarget ??!!
	// skip eventPhase ??!!
	// skip stopPropagation
	// skip cancelBubble
	// skip stopImmediatePropagation
	// skip a bunch more

	must(rt, o.DefineAccessorProperty("timestamp", rt.ToValue(func() float64 {
		return float64(t.UnixNano()) / 1_000_000 // mslliseconds as double as per the spec
		// https://w3c.github.io/hr-time/#dom-domhighrestimestamp
	}), nil, sobek.FLAG_FALSE, sobek.FLAG_TRUE))

	return o
}

func (ms *MultipartSubscription) addEventListener(event string, handler func(sobek.Value) (sobek.Value, error)) {
	// TODO support options https://developer.mozilla.org/en-US/docs/Web/API/EventTarget/addEventListener#parameters

	if handler == nil {
		common.Throw(ms.vu.Runtime(), fmt.Errorf("handler for event type %q isn't a callable function", event))
	}

	if err := ms.eventListeners.add(event, handler); err != nil {
		ms.vu.State().Logger.Warnf("can't add event handler: %s", err)
	}
}

func must(rt *sobek.Runtime, err error) {
	if err != nil {
		common.Throw(rt, err)
	}
}
