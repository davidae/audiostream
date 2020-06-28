package icestream

import (
	"errors"
	"io"
	"sync"
	"time"
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

func WithMetadataInterval(interval int64) StreamOpts {
	return func(s *Stream) { s.metadataInterval = interval }
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
	metadataInterval       int64
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
}

// Start starts the stream.
// error ErrNoAudioInQueue might be returned
func (s *Stream) Start(handleTimeout func(c *Client)) error {
	for s.isStop {
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
