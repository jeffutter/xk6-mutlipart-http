package multipartHTTP

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"reflect"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
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

func formatMimeHeaders(headers textproto.MIMEHeader) string {
	var builder strings.Builder
	for key, values := range headers {
		for _, value := range values {
			builder.WriteString(fmt.Sprintf("%s: %s\n", key, value))
		}
	}
	return builder.String()
}

// Close the event loop
func (c *Client) Close() error {
	return c.closeResponseBody()
}

type EncodingType int

const (
	None EncodingType = iota
	Gzip
	Deflate
	Brotli
	Unknown
)

func (c *Client) readEvents(readChan chan Payload, errorChan chan error, closeChan chan int, contentType string) {
	if c.resp == nil {
		select {
		case errorChan <- errors.New("HTTP response is nil"):
			return
		case <-c.done:
			return
		}
	}

	// contentEncodingHeader := c.resp.Header.Get("Content-Encoding")

	// contentEncoding := Unknown
	contentEncoding := None

	// switch strings.ToLower(strings.TrimSpace(contentEncodingHeader)) {
	// case "gzip":
	// 	c.Logger.Debugf("[%s] Found Gzip Encoding", c.requestID)
	// 	contentEncoding = Gzip
	// case "deflate":
	// 	c.Logger.Debugf("[%s] Found Deflate Encoding", c.requestID)
	// 	contentEncoding = Deflate
	// case "br":
	// 	c.Logger.Debugf("[%s] Found Brotli Encoding", c.requestID)
	// 	contentEncoding = Brotli
	// case "":
	// 	c.Logger.Debugf("[%s] Found No Encoding", c.requestID)
	// 	contentEncoding = None
	// default:
	// 	c.Logger.Warnf("[%s] Unsupported Content-Encoding in Header: %s", c.requestID, contentEncodingHeader)
	// 	contentEncoding = Unknown
	// }

	parser, err := NewParser(c.resp.Body, contentType)
	if err != nil {
		select {
		case errorChan <- errors.New("Parser Failed: " + err.Error()):
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

		// headerString := formatMimeHeaders(part.Header)
		// fmt.Println("Part Headers: ", headerString)

		// contentEncodingHeader = part.Header.Get("Content-Type")
		// switch strings.ToLower(strings.TrimSpace(contentEncodingHeader)) {
		// case "gzip":
		// 	c.Logger.Debugf("[%s] Found Gzip Encoding", c.requestID)
		// 	contentEncoding = Gzip
		// case "deflate":
		// 	c.Logger.Debugf("[%s] Found Deflate Encoding", c.requestID)
		// 	contentEncoding = Deflate
		// case "br":
		// 	c.Logger.Debugf("[%s] Found Brotli Encoding", c.requestID)
		// 	contentEncoding = Brotli
		// case "":
		// 	c.Logger.Debugf("[%s] Found No Encoding", c.requestID)
		// 	contentEncoding = None
		// default:
		// 	c.Logger.Warnf("[%s] Unsupported Content-Encoding in message: %s", c.requestID, contentEncodingHeader)
		// 	// contentEncoding = Unknown
		// }

		if part == nil || part.Body == nil {
			break
		}

		if reflect.DeepEqual(part.Body, []byte("{}")) {
			c.Logger.Debugf("[%s] Heartbeat Received", c.requestID)
			// Do nothing (Heartbeat)
			continue
		}

		var message []byte

		switch contentEncoding {
		case Gzip:
			gzipReader, err := gzip.NewReader(bytes.NewReader(part.Body))
			if err != nil {
				select {
				case errorChan <- err:
					return
				case <-c.done:
					return
				}
			}
			defer gzipReader.Close()
			message, err = io.ReadAll(gzipReader)
			if err != nil {
				select {
				case errorChan <- err:
					return
				case <-c.done:
					return
				}
			}
		case Deflate:
			deflateReader := flate.NewReader(bytes.NewReader(part.Body))
			defer deflateReader.Close()
			message, err = io.ReadAll(deflateReader)
			if err != nil {
				select {
				case errorChan <- err:
					return
				case <-c.done:
					return
				}
			}
		case Brotli:
			brReader := brotli.NewReader(bytes.NewReader(part.Body))
			message, err = io.ReadAll(brReader)
			if err != nil {
				select {
				case errorChan <- err:
					return
				case <-c.done:
					return
				}
			}
		case None:
			// No additional processing needed for plain text
			message = part.Body
		default:
			c.Logger.Warnf("[%s] Unsupported Content-Encoding: %s", c.requestID, contentEncoding)
		}

		payload := Payload{}

		switch {
		case hasPrefix(message, "{\"payload\""):
			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, message)
			payload.payload = string(message)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}

		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
		case hasPrefix(message, "{\"data\""):
			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, message)
			payload.payload = string(message)
			select {
			case readChan <- payload:
			case <-c.done:
				return
			}

		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
		case hasPrefix(message, "{\"incremental\""):
			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, message)
			payload.payload = string(message)
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

	// contentEncodingHeader := c.resp.Header.Get("Content-Encoding")
	//
	// contentEncoding := Unknown
	// switch contentEncodingHeader {
	// case "gzip":
	// 	c.Logger.Debugf("[%s] Found Gzip Encoding", c.requestID)
	// 	contentEncoding = Gzip
	// case "deflate":
	// 	c.Logger.Debugf("[%s] Found Deflate Encoding", c.requestID)
	// 	contentEncoding = Deflate
	// case "br":
	// 	c.Logger.Debugf("[%s] Found Brotli Encoding", c.requestID)
	// 	contentEncoding = Brotli
	// case "":
	// 	c.Logger.Debugf("[%s] Found No Encoding", c.requestID)
	// 	contentEncoding = None
	// default:
	// 	c.Logger.Warnf("[%s] Unsupported Content-Encoding: %s", c.requestID, contentEncodingHeader)
	// 	contentEncoding = Unknown
	// }
	//
	// // Check for Content-Encoding header and create appropriate reader
	// var bodyReader io.Reader = c.resp.Body
	//
	// // if contentEncoding != "" {
	// // 	c.Logger.Debugf("[%s] Content-Encoding detected: %s", c.requestID, contentEncoding)
	// //
	// // 	switch strings.ToLower(contentEncoding) {
	// // 	case "gzip":
	// // 		gzipReader, err := gzip.NewReader(c.resp.Body)
	// // 		if err != nil {
	// // 			select {
	// // 			case errorChan <- err:
	// // 				return
	// // 			case <-c.done:
	// // 				return
	// // 			}
	// // 		}
	// // 		defer gzipReader.Close()
	// // 		bodyReader = gzipReader
	// //
	// // 	case "deflate":
	// // 		deflateReader := flate.NewReader(c.resp.Body)
	// // 		defer deflateReader.Close()
	// // 		bodyReader = deflateReader
	// //
	// // 	case "br":
	// // 		brReader := brotli.NewReader(c.resp.Body)
	// // 		bodyReader = brReader
	// //
	// // 	default:
	// // 		c.Logger.Warnf("[%s] Unsupported Content-Encoding: %s", c.requestID, contentEncoding)
	// // 	}
	// // }
	//
	// reader := bufio.NewReader(bodyReader)
	// inMessage := false
	// payload := Payload{}
	//
	// for {
	// 	if contentEncoding == Unknown {
	// 		c.Logger.Warnf("[%s] Unknown Content-Encoding, defaulting to plain text", c.requestID)
	//
	// 		select {
	// 		case errorChan <- errors.New("Unknown Content-Encoding Cannot Proceed"):
	// 		case <-c.done:
	// 			return
	// 		}
	// 	}
	//
	// 	line, err := reader.ReadBytes('\n')
	//
	// 	if err != nil {
	// 		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
	// 			select {
	// 			case closeChan <- -1:
	// 				return
	// 			case <-c.done:
	// 				return
	// 			}
	// 		} else {
	// 			select {
	// 			case errorChan <- err:
	// 				return
	// 			case <-c.done:
	// 				return
	// 			}
	// 		}
	// 	}
	//
	// 	// Follows Multipart HTTP Protocol: https://www.w3.org/Protocols/rfc1341/7_2_Multipart.html
	// 	switch {
	// 	case hasPrefix(line, "--graphql"):
	// 		c.Logger.Debugf("[%s] Boundary Received", c.requestID)
	// 		inMessage = !inMessage
	// 		// Do nothing (Boundary)
	// 	case hasPrefix(line, "content-type"):
	// 		// TODO: Parse out content-type and set contentType var
	// 		c.Logger.Debugf("[%s] Content-Type Received: %s", c.requestID, string(line))
	// 		// Do Nothing (ContentType)
	// 	case hasPrefix(line, "Content-Type"):
	// 		// TODO: Parse out content-type and set contentType var
	// 		c.Logger.Debugf("[%s] Content-Type Received: %s", c.requestID, string(line))
	// 		// Do Nothing (ContentType)
	// 	case bytes.Equal(line, []byte("\r\n")):
	// 		c.Logger.Debugf("[%s] CRLF Received", c.requestID)
	// 		// Do Nothing (CRLF is Always after ContentType and Before Payload)
	// 	case hasPrefix(line, "{}"):
	// 		c.Logger.Debugf("[%s] Heartbeat Received", c.requestID)
	// 		// Do nothing (Heartbeat)
	//
	// 	default:
	// 		switch contentEncoding {
	// 		case Gzip:
	// 			gzipReader, err := gzip.NewReader(bytes.NewReader(line))
	// 			if err != nil {
	// 				select {
	// 				case errorChan <- err:
	// 					return
	// 				case <-c.done:
	// 					return
	// 				}
	// 			}
	// 			defer gzipReader.Close()
	// 			line, err = io.ReadAll(gzipReader)
	// 			if err != nil {
	// 				select {
	// 				case errorChan <- err:
	// 					return
	// 				case <-c.done:
	// 					return
	// 				}
	// 			}
	// 		case Deflate:
	// 			deflateReader := flate.NewReader(bytes.NewReader(line))
	// 			defer deflateReader.Close()
	// 			line, err = io.ReadAll(deflateReader)
	// 			if err != nil {
	// 				select {
	// 				case errorChan <- err:
	// 					return
	// 				case <-c.done:
	// 					return
	// 				}
	// 			}
	// 		case Brotli:
	// 			brReader := brotli.NewReader(bytes.NewReader(line))
	// 			line, err = io.ReadAll(brReader)
	// 			if err != nil {
	// 				select {
	// 				case errorChan <- err:
	// 					return
	// 				case <-c.done:
	// 					return
	// 				}
	// 			}
	// 		case None:
	// 			// No additional processing needed for plain text
	// 		default:
	// 			c.Logger.Warnf("[%s] Unsupported Content-Encoding: %s", c.requestID, contentEncodingHeader)
	// 		}
	//
	// 		switch {
	// 		case hasPrefix(line, "{\"payload\""):
	// 			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, line)
	// 			payload.payload = string(line)
	// 			select {
	// 			case readChan <- payload:
	// 			case <-c.done:
	// 				return
	// 			}
	//
	// 		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
	// 		case hasPrefix(line, "{\"data\""):
	// 			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, line)
	// 			payload.payload = string(line)
	// 			select {
	// 			case readChan <- payload:
	// 			case <-c.done:
	// 				return
	// 			}
	//
	// 		// I think this is a slightly different protocol used by apollo server? (and the go gqlgen)
	// 		case hasPrefix(line, "{\"incremental\""):
	// 			c.Logger.Debugf("[%s] Received payload: %s", c.requestID, line)
	// 			payload.payload = string(line)
	// 			select {
	// 			case readChan <- payload:
	// 			case <-c.done:
	// 				return
	// 			}
	// 		default:
	// 			c.Logger.Debugf("[%s] HTTPMultipart - Received unknown: %s", c.requestID, line)
	// 			c.Logger.Debugf("[%s] HTTPMultipart - Received unknown: %s", c.requestID, string(line[:]))
	//
	// 			select {
	// 			case errorChan <- errors.New("Unknown event: " + string(line)):
	// 			case <-c.done:
	// 				return
	// 			}
	// 		}
	// 	}
	//
	// }
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
