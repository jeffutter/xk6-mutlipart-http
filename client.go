package multipartHTTP

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"

	"github.com/google/uuid"
	"github.com/grafana/sobek"
	"github.com/sirupsen/logrus"
)

// Client is the representation of the sse returned to the js.
type Client struct {
	rt           *sobek.Runtime
	ctx          context.Context
	url          string
	resp         *http.Response
	done         chan struct{}
	shutdownOnce sync.Once
	Logger       logrus.FieldLogger
	requestID    uuid.UUID
}

// Close the event loop
func (c *Client) Close() error {
	return c.closeResponseBody()
}

func (c *Client) readEvents(readChan chan Payload, errorChan chan error, closeChan chan int, contentType string) {
	if c.resp == nil {
		select {
		case errorChan <- errors.New("HTTP response is nil"):
			return
		case <-c.done:
			return
		}
	}

	parser, err := NewParser(c.resp.Body, contentType)
	if err != nil {
		select {
		case errorChan <- errors.New("Create Parser Failed: " + err.Error()):
			return
		case <-c.done:
			return
		}
	}

	for {
		part, err := parser.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			select {
			case errorChan <- errors.New("Parser Failed: " + err.Error()):
			case <-c.done:
				break
			}
		}

		// Seems to happen if the client is closing
		if part == nil {
			break
		}

		if reflect.DeepEqual(part.Body, []byte("{}")) {
			c.Logger.Debugf("[%s] Heartbeat Received", c.requestID)
			// Do nothing (Heartbeat)
			continue
		}

		message := part.Body

		payload := Payload{}

		switch {
		case hasPrefix(message, "{\"payload\""):
			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, message)
			payload.data = string(message)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}

		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
		case hasPrefix(message, "{\"data\""):
			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, message)
			payload.data = string(message)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}

		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
		case hasPrefix(message, "{\"incremental\""):
			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, message)
			payload.data = string(message)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}
		default:
			c.Logger.Debugf("[%s] HTTPMultipart - Received unknown: %s", c.requestID, message)
			c.Logger.Debugf("[%s] HTTPMultipart - Received unknown: %s", c.requestID, string(message[:]))

			select {
			case errorChan <- errors.New("Unknown event: " + string(message)):
			case <-c.done:
				return
			}
		}

	}

}

// closeResponseBody cleanly closes the response body.
// Returns an error if sending the response body cannot be closed.
func (c *Client) closeResponseBody() error {
	var err error

	c.shutdownOnce.Do(func() {
		c.Logger.Debug("Closing client response body")

		if c.resp != nil && c.resp.Body != nil {
			err = c.resp.Body.Close()
			if err != nil {
				// Call the user-defined error handler
				// c.handleEvent("error", c.rt.ToValue(err))
			}
		}
		close(c.done)
	})

	return err
}

func hasPrefix(s []byte, prefix string) bool {
	return bytes.HasPrefix(s, []byte(prefix))
}
