package icestream

import (
	"errors"
	"io"
	"sync"
	"time"
)

const (
	defaultFrameSize        = 3000
	defaultMetadataInterval = 65536
)

type StreamOpts func(s *Stream)

func WithFramzeSize(size int) StreamOpts {
	return func(s *Stream) { s.frameSize = size }
}

func WithForceSampleRate(r int) StreamOpts {
	return func(s *Stream) { s.sampleRate = r }
}

func WithShoutcastMetadata() StreamOpts {
	return func(s *Stream) { s.allowShoutcastMetadata = true }
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

func (a *Audio) Read(b []byte) (int, error) {
	return a.Data.Read(b)
}

func NewStream(opts ...StreamOpts) *Stream {
	s := &Stream{
		sync:      &sync.Mutex{},
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

	return s
}

type Stream struct {
	frameSize              int
	sampleRate             int
	allowShoutcastMetadata bool
	blockOnNoAudio         bool
	metadataInterval       int64

	sync          *sync.Mutex
	clientTimeout chan *Client
	listeners     map[string]*Client
	queue         []*Audio
	reading       *Audio
	playing       *Audio
}

func (s *Stream) Append(a *Audio) {
	s.sync.Lock()
	s.queue = append(s.queue, a)
	s.sync.Unlock()
}

func (s *Stream) Deregister(c Client) {
	delete(s.listeners, c.uuid)
}

func (s *Stream) nextAudio() (*Audio, error) {
	s.sync.Lock()
	defer s.sync.Unlock()
	if len(s.queue) == 0 {
		return nil, ErrNoAudioInQueue
	}

	a := s.queue[0]
	s.queue = s.queue[1:]
	return a, nil
}

var ErrNoAudioInQueue = errors.New("no audio in stream queue")

func (s *Stream) Start(clientTimeout time.Duration, timeoutCh chan<- *Client) error {
	for {
		// are we done reading/playing a song?
		if s.reading == nil {
			reading, err := s.nextAudio()
			if err != nil {
				if err == ErrNoAudioInQueue {
					continue
				}
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

		// a very rough estimation of the playtime of the frame
		framePlaytime := time.Duration(float64(time.Millisecond) * (float64(bytes) / float64(s.sampleRate)) * 1000)

		// send frame to all clients
		start := time.Now()
		var wg sync.WaitGroup
		wg.Add(len(s.listeners))
		for _, c := range s.listeners {
			go func(c *Client) {
				select {
				case c.frame <- frame:
				case <-time.After(time.Second):
					timeoutCh <- c
				}
				wg.Done()
			}(c)
		}
		wg.Wait()

		finish := time.Now().Sub(start)
		sleep := framePlaytime - finish
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
}
