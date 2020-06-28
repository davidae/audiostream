package icestream

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/gofrs/uuid"
)

const (
	defaultClientTimeout    = time.Millisecond * 500
	defaultFrameSize        = 3000
	defaultMetadataInterval = 65536

	// MaxMetaDataSize is the maximum size for meta data (everything over is truncated)
	// Must be a multiple of 16 which fits into one byte. Maximum: 16 * 255 = 4080
	MaxMetaDataSize = 4080
)

var ErrNoAudioInQueue = errors.New("no audio in stream queue")

type StreamOpts func(s *Stream)

func WithClientTimeout(d time.Duration) StreamOpts {
	return func(s *Stream) { s.clientTimeout = d }
}

func WithFramzeSize(size int) StreamOpts {
	return func(s *Stream) { s.frameSize = size }
}

func WithShoutcastMetadata(interval uint64) StreamOpts {
	return func(s *Stream) {
		s.allowShoutcastMetadata = true
		s.metadataInterval = interval
	}
}

func WithLazyFileRead() StreamOpts {
	return func(s *Stream) { s.lazyFileReading = true }
}

type RegisterOpts func(*Client)

// SupportsShoutCastMetadata is usually confirmed when checking the incoming
// request header icy-metadata: 1
func WithSupportsShoutCastMetadata() RegisterOpts {
	return func(c *Client) { c.supportShoutCastMetadata = true }
}

type Audio struct {
	Data          io.Reader
	SampleRate    int
	Title, Artist string
	Filename      string
}

func (a *Audio) Read(b []byte) (int, error) { return a.Data.Read(b) }

type Frame struct {
	data          []byte
	title, artist string
}

func (f Frame) ShoutcastMetadata() []byte {
	meta := fmt.Sprintf("StreamTitle='%v - %v';", f.artist, f.title)

	// is it above max size?
	if len(meta) > MaxMetaDataSize {
		meta = meta[:MaxMetaDataSize-2] + "';"
	}

	// Calculate the meta data frame size as a multiple of 16
	frameSize := byte(math.Ceil(float64(len(meta)) / 16.0))

	metadata := make([]byte, 16.0*frameSize+1, 16.0*frameSize+1)
	metadata[0] = frameSize
	copy(metadata[1:], meta)
	return metadata
}

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
	for _, c := range s.listeners {
		s.Deregister(c)
	}

	s.listeners = make(map[string]*Client)
}

// Start starts the stream.
// error ErrNoAudioInQueue might be returned
func (s *Stream) Start() error {
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

		s.clientMux.Lock()
		// send frame to all clients
		start := time.Now()
		var wg sync.WaitGroup
		wg.Add(len(s.listeners))
		for _, c := range s.listeners {
			go func(c *Client) {
				c.frame <- Frame{data: frame, artist: s.reading.Artist, title: s.reading.Title}
				wg.Done()
			}(c)
		}
		wg.Wait()
		s.clientMux.Unlock()

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
		frame:            make(chan Frame),
		stop:             make(chan struct{}),
		headers:          make(map[string]string),
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

var ErrClientNotFound = errors.New("client not found amoung active listeners")

func (s *Stream) Deregister(c *Client) error {
	s.clientMux.Lock()
	defer s.clientMux.Unlock()
	if _, ok := s.listeners[c.uuid]; !ok {
		return ErrClientNotFound
	}
	delete(s.listeners, c.uuid)
	close(c.stop)
	close(c.frame)
	close(c.stream)
	return nil
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
