package icestream

import (
	"net/http"
)

type Client struct {
	uuid             string
	frame            chan Frame
	stream           chan []byte
	stop             chan struct{}
	isAlive          bool
	writeMetadata    bool
	startedToStream  bool
	metadataInterval uint64

	framesWrittenInInterval  uint64
	supportShoutCastMetadata bool
	headers                  map[string]string
}

func (c Client) SetHeaders(r *http.Request) {
	for k, v := range c.headers {
		r.Header.Set(k, v)
	}
}

func (c *Client) Stream() <-chan []byte {
	if c.startedToStream {
		return c.stream
	}

	c.startedToStream = true

	go func() {
		for {
			dataFrame, ok := <-c.frame
			if !ok {
				return
			}

			frame := dataFrame.data
			// metadata should be within this frame now
			if c.writeMetadata && c.framesWrittenInInterval+uint64(len(frame)) >= c.metadataInterval {
				// how much can we write to stream before we need to send metadata
				preMetadata := c.metadataInterval - c.framesWrittenInInterval
				if preMetadata > 0 {
					c.stream <- frame[:preMetadata]
					c.framesWrittenInInterval += preMetadata

					// remainder of the frame that is to be send after metadata
					frame = frame[preMetadata:]
				}
				// at this point in time, we've reached the interval
				c.framesWrittenInInterval = 0
				// write metdata
				metadata := dataFrame.ShoutcastMetadata()
				c.stream <- metadata
				c.framesWrittenInInterval += uint64(len(metadata))
				// write remainder frame
				c.stream <- frame
				c.framesWrittenInInterval += uint64(len(frame))
				return
			}

			c.framesWrittenInInterval += uint64(len(frame))
			c.stream <- frame
		}

	}()

	return c.stream
}
