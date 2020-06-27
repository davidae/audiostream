package icestream

type Client struct {
	uuid   string
	stream chan []byte

	supportShoutCastMetadata bool
}

func (c Client) Stream() <-chan []byte {
	return c.stream
}
