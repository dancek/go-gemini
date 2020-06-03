package gemini

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Response represent the response from a Gemini server.
type Response struct {
	Status int
	Meta   string
	Body   io.ReadCloser
}

type header struct {
	status int
	meta   string
}

type Client struct {
	// NoTimeCheck allows connections with expired or future certs if set to true.
	NoTimeCheck bool
	// NoHostnameCheck allows connections when the cert doesn't match the
	// requested hostname or IP.
	NoHostnameCheck bool
	// AllowInvalidStatuses means the client won't raise an error if a status
	// that is out of spec is returned.
	AllowInvalidStatuses bool
	// Insecure disables all TLS-based checks, use with caution.
	// It overrides all the variables above.
	Insecure bool
}

var DefaultClient = &Client{}

// Fetch a resource from a Gemini server with the given URL
func (c *Client) Fetch(rawURL string) (*Response, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %v", err)
	}
	// Add default protocol port if none provided
	if parsedURL.Port() == "" {
		parsedURL.Host = net.JoinHostPort(parsedURL.Hostname(), "1965")
	}

	// Add gemini scheme if not provided
	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "gemini"
	}

	conn, err := c.connect(parsedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the server: %v", err)
	}

	err = sendRequest(conn, rawURL)
	if err != nil {
		conn.Close()
		return nil, err
	}

	res, err := getResponse(conn)
	if err != nil {
		return nil, err
	}
	if !c.AllowInvalidStatuses && !IsStatusValid(res.Status) {
		return nil, fmt.Errorf("invalid status code: %v", res.Status)
	}

	return res, nil
}

func (c *Client) connect(parsedURL *url.URL) (io.ReadWriteCloser, error) {
	conf := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // This must be set to allow self-signed certs
	}

	conn, err := tls.Dial("tcp", parsedURL.Host, conf)
	if err != nil {
		return conn, err
	}

	if c.Insecure {
		return conn, nil
	}

	// Verify hostname
	if !c.NoHostnameCheck && conn.VerifyHostname(parsedURL.Hostname()) != nil {
		return nil, fmt.Errorf("hostname does not verify")
	}
	// Verify expiry
	if !c.NoTimeCheck {
		serverCert := conn.ConnectionState().PeerCertificates[0]
		if serverCert.NotBefore.Before(time.Now()) {
			// It's a future cert
			return nil, fmt.Errorf("server cert is for the future")
		} else if serverCert.NotAfter.After(time.Now()) {
			// It's expired
			return nil, fmt.Errorf("server cert is expired")
		}
	}

	return conn, nil
}

// Fetch a resource from a Gemini server with the default client
func Fetch(url string) (*Response, error) {
	return DefaultClient.Fetch(url)
}

func sendRequest(conn io.Writer, requestURL string) error {
	_, err := fmt.Fprintf(conn, "%s\r\n", requestURL)
	if err != nil {
		return fmt.Errorf("could not send request to the server: %v", err)
	}

	return nil
}

func getResponse(conn io.ReadCloser) (*Response, error) {
	header, err := getHeader(conn)
	if err != nil {
		conn.Close()
		return &Response{}, fmt.Errorf("failed to get header: %v", err)
	}

	return &Response{header.status, header.meta, conn}, nil
}

func getHeader(conn io.Reader) (header, error) {
	line, err := readHeader(conn)
	if err != nil {
		return header{}, fmt.Errorf("failed to read header: %v", err)
	}

	fields := strings.Fields(string(line))
	status, err := strconv.Atoi(fields[0])
	if err != nil {
		return header{}, fmt.Errorf("unexpected status value %v: %v", fields[0], err)
	}

	meta := strings.Join(fields[1:], " ")
	if len(meta) > 1024 {
		return header{}, fmt.Errorf("meta string is too long")
	}

	return header{status, meta}, nil
}

func readHeader(conn io.Reader) ([]byte, error) {
	var line []byte
	delim := []byte("\r\n")
	// A small buffer is inefficient but the maximum length of the header is small so it's okay
	buf := make([]byte, 1)

	for {
		_, err := conn.Read(buf)
		if err != nil {
			return []byte{}, err
		}

		line = append(line, buf...)
		if bytes.HasSuffix(line, delim) {
			return line[:len(line)-len(delim)], nil
		}
	}
}
