package icestream

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gofrs/uuid"
)

const (
	defaultClientTimeout    = time.Millisecond * 500
	defaultFrameSize        = 3000
	defaultMetadataInterval = 65536
)

var ErrNoAudioInQueue = errors.New("no audio in stream queue")

type StreamOpts func(s *Stream)

func WithClientTimeout(d time.Duration) StreamOpts {
	return func(s *Stream) { s.clientTimeout = d }
}

func WithFramzeSize(size int) StreamOpts {
	return func(s *Stream) { s.frameSize = size }
}

func WithShoutcastMetadata() StreamOpts {
	return func(s *Stream) { s.allowShoutcastMetadata = true }
}

func WithLazyFileRead() StreamOpts {
	return func(s *Stream) { s.lazyFileReading = true }
}

func WithMetadataInterval(interval uint64) StreamOpts {
	return func(s *Stream) { s.metadataInterval = interval }
}

type RegisterOpts func(*Client)

// SupportsShoutCastMetadata is usually confirmed when checking the incoming
// request header icy-metadata: 1
func SupportsShoutCastMetadata() RegisterOpts {
	return func(c *Client) { c.supportShoutCastMetadata = true }
}

type Audio struct {
	Data          io.Reader
	SampleRate    int
	Title, Artist string
	Filename      string
}

func (a *Audio) Read(b []byte) (int, error) { return a.Data.Read(b) }

type Stream struct {
	frameSize              int
	allowShoutcastMetadata bool
	metadataInterval       uint64
	lazyFileReading        bool
	clientTimeout          time.Duration

	audioMux, clientMux *sync.Mutex
	listeners           map[string]*Client
	queue               []*Audio
	reading             *Audio
	isStop              bool
}

func NewStream(opts ...StreamOpts) *Stream {
	s := &Stream{
		audioMux:  &sync.Mutex{},
		clientMux: &sync.Mutex{},
		listeners: make(map[string]*Client),
		queue:     []*Audio{},
	}

	for _, o := range opts {
		o(s)
	}

	if s.frameSize == 0 {
		s.frameSize = defaultFrameSize
	}

	if s.metadataInterval == 0 {
		s.metadataInterval = defaultMetadataInterval
	}

	if s.clientTimeout == 0 {
		s.clientTimeout = defaultClientTimeout
	}

	return s
}

func (s *Stream) Append(a *Audio) {
	s.audioMux.Lock()
	s.queue = append(s.queue, a)
	s.audioMux.Unlock()
}

func (s *Stream) Stop() {
	s.isStop = true
	s.listeners = make(map[string]*Client)
}

// Start starts the stream.
// error ErrNoAudioInQueue might be returned
func (s *Stream) Start(handleTimeout func(c *Client)) error {
	for !s.isStop {
		// are we done reading/playing a song?
		if s.reading == nil {
			reading, err := s.dequeue()
			if err != nil {
				return err
			}
			s.reading = reading
		}

		// read a frame from audio
		frame := make([]byte, s.frameSize)
		bytes, err := s.reading.Read(frame)
		if err != nil {
			// we are done reading this audio file
			if err == io.EOF {
				s.reading = nil
				continue
			}
			return err
		}

		timedOut := []*Client{}

		s.clientMux.Lock()
		// send frame to all clients
		start := time.Now()
		var wg sync.WaitGroup
		wg.Add(len(s.listeners))
		for _, c := range s.listeners {
			go func(c *Client) {
				select {
				case c.frame <- frame:
				case <-time.After(s.clientTimeout):
					timedOut = append(timedOut, c)
				}
				wg.Done()
			}(c)
		}
		wg.Wait()
		s.clientMux.Unlock()

		for _, c := range timedOut {
			// handle timed out client here instead of inside s.clientMux lock
			// to avoid any potential race confliects.
			handleTimeout(c)
		}

		finish := time.Now().Sub(start)
		// a very rough estimation of the playtime of the frame
		framePlaytime := time.Duration(float64(time.Millisecond) * (float64(bytes) / float64(s.reading.SampleRate)) * 1000)
		sleep := framePlaytime - finish
		if sleep > 0 && s.lazyFileReading {
			time.Sleep(sleep)
		}
	}

	return nil
}

func (s *Stream) Register(opts ...RegisterOpts) (*Client, error) {
	uuid, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	c := &Client{
		uuid:             uuid.String(),
		stream:           make(chan []byte),
		frame:            make(chan []byte),
		stop:             make(chan struct{}),
		headers:          make(map[string]string),
		isAlive:          true,
		metadataInterval: s.metadataInterval,
	}

	for _, o := range opts {
		o(c)
	}

	if c.supportShoutCastMetadata && s.allowShoutcastMetadata {
		c.headers["icy-name"] = "hello world"
		c.headers["icy-metadata"] = "1"
		c.headers["icy-metaint"] = "1"

		c.writeMetadata = true
	}

	s.listeners[uuid.String()] = c
	return c, nil
}

func (s *Stream) Deregister(clients ...*Client) {
	for _, c := range clients {
		s.clientMux.Lock()
		delete(s.listeners, c.uuid)
		c.isAlive = false
		c.stop <- struct{}{}
		close(c.stop)
		close(c.frame)
		close(c.stream)
		s.clientMux.Unlock()
	}
}

func (s *Stream) dequeue() (*Audio, error) {
	s.audioMux.Lock()
	defer s.audioMux.Unlock()
	if len(s.queue) == 0 {
		return nil, ErrNoAudioInQueue
	}

	a := s.queue[0]
	s.queue = s.queue[1:]
	return a, nil
}
