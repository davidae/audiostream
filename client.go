package icestream

import (
	"net/http"

	"github.com/gofrs/uuid"
)

type Client struct {
	uuid          string
	frame, stream chan []byte

	framesWritten            uint64
	supportShoutCastMetadata bool
	headers                  map[string]string
}

func (c Client) SetHeaders(r *http.Request) {
	for k, v := range c.headers {
		r.Header.Set(k, v)
	}
}

func (c *Client) Stream() <-chan []byte {
	go func() {
		for {
			frame := <-c.frame
			c.framesWritten += uint64(len(frame))
			c.stream <- frame
		}
	}()
	return c.stream
}

type RegisterOpts func(*Client)

// SupportsShoutCastMetadata is usually confirmed when checking the incoming
// request header icy-metadata: 1
func SupportsShoutCastMetadata() RegisterOpts {
	return func(c *Client) { c.supportShoutCastMetadata = true }
}

func (s *Stream) Register(opts ...RegisterOpts) (*Client, error) {
	uuid, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	c := &Client{
		uuid:    uuid.String(),
		stream:  make(chan []byte),
		frame:   make(chan []byte),
		headers: make(map[string]string),
	}

	for _, o := range opts {
		o(c)
	}

	if c.supportShoutCastMetadata && s.allowShoutcastMetadata {
		c.headers["icy-name"] = "hello world"
		c.headers["icy-metadata"] = "1"
		c.headers["icy-metaint"] = "1"
	}

	s.listeners[uuid.String()] = c
	return c, nil
}
