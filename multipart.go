package multipartHTTP

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/textproto"
	"strings"
)

var (
	ErrNoBoundary    = errors.New("no boundary found")
	ErrInvalidFormat = errors.New("invalid multipart format")
	ErrUnexpectedEOF = errors.New("unexpected end of file")
)

// Part represents a single part in a multipart message
type Part struct {
	Header textproto.MIMEHeader
	Body   []byte
}

// Parser handles parsing of multipart/mixed content according to RFC 1341
type Parser struct {
	reader   *bufio.Reader
	boundary string
	finished bool
}

// NewParser creates a new multipart parser
// contentType should be the full Content-Type header value (e.g., "multipart/mixed; boundary=example")
func NewParser(r io.Reader, contentType string) (*Parser, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to parse content type: %w", err)
	}

	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		return nil, ErrNoBoundary
	}

	return &Parser{
		reader:   bufio.NewReader(r),
		boundary: boundary,
		finished: false,
	}, nil
}

// NextPart reads and returns the next part from the multipart stream
// Returns io.EOF when no more parts are available
func (p *Parser) NextPart() (*Part, error) {
	// fmt.Println("A")
	if p.finished {
		return nil, io.EOF
	}

	// fmt.Println("B")
	// Look for the boundary
	if err := p.findBoundary(); err != nil {
		return nil, err
	}

	// fmt.Println("C")
	// Read headers
	headers, err := p.readHeaders()
	if err != nil {
		return nil, err
	}

	// fmt.Println("D")
	// Read body until next boundary
	body, err := p.readBody()
	if err != nil {
		return nil, err
	}

	// fmt.Println("E")
	return &Part{
		Header: headers,
		Body:   body,
	}, nil
}

// findBoundary looks for the next boundary marker in the stream
func (p *Parser) findBoundary() error {
	boundaryPrefix := "--" + p.boundary

	for {
		line, err := p.readLine()
		// fmt.Printf("Find Boundary Line: %s | %s\n", line, string(line))
		if err != nil {
			if err == io.EOF {
				return ErrUnexpectedEOF
			}
			return err
		}

		line = strings.TrimSpace(line)

		// Check for boundary
		if line == boundaryPrefix {
			return nil
		}

		// Check for final boundary
		if line == boundaryPrefix+"--" {
			p.finished = true
			return io.EOF
		}
	}
}

// readHeaders reads the MIME headers for a part
func (p *Parser) readHeaders() (textproto.MIMEHeader, error) {
	headers := make(textproto.MIMEHeader)

	for {
		line, err := p.readLine()
		if err != nil {
			return nil, err
		}

		// Empty line indicates end of headers
		if strings.TrimSpace(line) == "" {
			break
		}

		// Parse header line
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue // Invalid header line, skip
		}

		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])

		// Handle header continuation lines
		for {
			peek, err := p.reader.Peek(1)
			if err != nil {
				break
			}

			// Check if next line starts with whitespace (continuation)
			if peek[0] != ' ' && peek[0] != '\t' {
				break
			}

			contLine, err := p.readLine()
			if err != nil {
				break
			}
			value += " " + strings.TrimSpace(contLine)
		}

		key = textproto.CanonicalMIMEHeaderKey(key)
		headers[key] = append(headers[key], value)
	}

	return headers, nil
}

// readBody reads the body of a part until the next boundary
func (p *Parser) readBody() ([]byte, error) {
	var body bytes.Buffer
	boundaryPrefix := "--" + p.boundary

	for {
		line, err := p.readLine()
		if err != nil {
			if err == io.EOF {
				return body.Bytes(), nil
			}
			return nil, err
		}

		trimmedLine := strings.TrimSpace(line)

		// Check for boundary
		if trimmedLine == boundaryPrefix {
			// Put the boundary line back for the next part
			p.reader = bufio.NewReader(io.MultiReader(
				strings.NewReader(line+"\n"),
				p.reader,
			))

			// Remove trailing CRLF from body if present
			bodyBytes := body.Bytes()
			if len(bodyBytes) >= 2 && bodyBytes[len(bodyBytes)-2] == '\r' && bodyBytes[len(bodyBytes)-1] == '\n' {
				bodyBytes = bodyBytes[:len(bodyBytes)-2]
			} else if len(bodyBytes) >= 1 && bodyBytes[len(bodyBytes)-1] == '\n' {
				bodyBytes = bodyBytes[:len(bodyBytes)-1]
			}

			return bodyBytes, nil
		}

		// Check for final boundary
		if trimmedLine == boundaryPrefix+"--" {
			p.finished = true

			// Remove trailing CRLF from body if present
			bodyBytes := body.Bytes()
			if len(bodyBytes) >= 2 && bodyBytes[len(bodyBytes)-2] == '\r' && bodyBytes[len(bodyBytes)-1] == '\n' {
				bodyBytes = bodyBytes[:len(bodyBytes)-2]
			} else if len(bodyBytes) >= 1 && bodyBytes[len(bodyBytes)-1] == '\n' {
				bodyBytes = bodyBytes[:len(bodyBytes)-1]
			}

			return bodyBytes, nil
		}

		// Add line to body
		body.WriteString(line)
		body.WriteByte('\n')
	}
}

// readLine reads a line from the input, handling both CRLF and LF line endings
func (p *Parser) readLine() (string, error) {
	line, err := p.reader.ReadString('\n')
	if err != nil {
		return line, err
	}

	// Remove trailing CRLF or LF
	if strings.HasSuffix(line, "\r\n") {
		line = line[:len(line)-2]
	} else if strings.HasSuffix(line, "\n") {
		line = line[:len(line)-1]
	}

	// fmt.Printf("Line: %s | %s\n", line, string(line))

	return line, nil
}

// GetContentType returns the Content-Type header value for a part
func (p *Part) GetContentType() string {
	return p.Header.Get("Content-Type")
}

// GetContentDisposition returns the Content-Disposition header value for a part
func (p *Part) GetContentDisposition() string {
	return p.Header.Get("Content-Disposition")
}

// IsText returns true if the part contains text data
func (p *Part) IsText() bool {
	contentType := p.GetContentType()
	if contentType == "" {
		return true // Default to text if no Content-Type specified
	}
	return strings.HasPrefix(strings.ToLower(contentType), "text/")
}

// IsBinary returns true if the part contains binary data
func (p *Part) IsBinary() bool {
	return !p.IsText()
}

// String returns the body as a string (useful for text parts)
func (p *Part) String() string {
	return string(p.Body)
}
