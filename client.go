package icestream

import (
	"net/http"
)

type Client struct {
	uuid             string
	frame, stream    chan []byte
	stop             chan struct{}
	isAlive          bool
	writeMetadata    bool
	startedToStream  bool
	metadataInterval uint64

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
	if c.startedToStream {
		return c.stream
	}

	c.startedToStream = true

	go func() {
		endLoop := false
		for !endLoop {
			select {
			case frame := <-c.frame:
				// metadata should be within this frame now
				if c.writeMetadata && c.framesWritten+uint64(len(frame)) >= c.metadataInterval {
					// how much can we write to stream before we need to send metadta
					preMetadata := c.metadataInterval - c.framesWritten
					if preMetadata > 0 {
						c.stream <- frame[:preMetadata]
						frame = frame[preMetadata:]
						c.framesWritten += preMetadata
					}

				}

				c.framesWritten += uint64(len(frame))
				c.stream <- frame
			case <-c.stop:
				endLoop = true
			}
		}
	}()

	return c.stream
}
