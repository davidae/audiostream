package icestream

import (
	"net/http"

	"github.com/gofrs/uuid"
)

type Client struct {
	uuid          string
	frame, stream chan []byte
	stop          chan struct{}
	isAlive       bool

	framesWritten            uint64
	supportShoutCastMetadata bool
	headers                  map[string]string
}

func (c Client) SetHeaders(r *http.Request) {
	for k, v := range c.headers {
		r.Header.Set(k, v)
	}
}

func (c Client) IsAlive() bool {
	return c.isAlive
}

func (c *Client) Stream() <-chan []byte {
	go func() {
		for {
			select {
			case frame := <-c.frame:
				c.framesWritten += uint64(len(frame))
				c.stream <- frame
			case <-c.stop:
				return
			}

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
		isAlive: true,
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

func (s *Stream) Deregister(c *Client) {
	s.clientMux.Lock()
	defer s.clientMux.Unlock()
	delete(s.listeners, c.uuid)
	c.isAlive = false
	c.stop <- struct{}{}
	close(c.frame)
	close(c.stream)
}
