package multipartHTTP

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

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
}

// Close the event loop
func (c *Client) Close() error {
	return c.closeResponseBody()
}

func (c *Client) readEvents(readChan chan Payload, errorChan chan error, closeChan chan int) {
	if c.resp == nil {
		select {
		case errorChan <- errors.New("HTTP response is nil"):
			return
		case <-c.done:
			return
		}
	}
	
	reader := bufio.NewReader(c.resp.Body)
	payload := Payload{}

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				select {
				case closeChan <- -1:
					return
				case <-c.done:
					return
				}
			} else {
				select {
				case errorChan <- err:
					return
				case <-c.done:
					return
				}
			}
		}

		// Follows Multipart HTTP Protocol: https://www.w3.org/Protocols/rfc1341/7_2_Multipart.html
		switch {
		case hasPrefix(line, "--graphql"):
			// Do nothing (Boundary)
		case hasPrefix(line, "content-type: application/json"):
			// Do Nothing (ContentType)
		case hasPrefix(line, "Content-Type: application/json"):
			// Do Nothing (ContentType)
		case bytes.Equal(line, []byte("\r\n")):
			// Do Nothing (CRLF is Always after ContentType and Before Payload)
		case hasPrefix(line, "{}"):
			c.Logger.Debug("Heartbeat Received")
			// Do nothing (Heartbeat)

		case hasPrefix(line, "{\"payload\""):
			c.Logger.Debugf("Received payload: %s", line)
			payload.payload = string(line)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}

		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
		case hasPrefix(line, "{\"data\""):
			c.Logger.Debugf("Received payload: %s", line)
			payload.payload = string(line)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}

		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
		case hasPrefix(line, "{\"incremental\""):
			c.Logger.Debugf("Received payload: %s", line)
			payload.payload = string(line)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}
		default:
			select {
			case errorChan <- errors.New("Unknown event: " + string(line)):
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
