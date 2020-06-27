package icestream

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gofrs/uuid"
)

const (
	defaultFrameSize = 3000
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

	return s
}

type Stream struct {
	frameSize              int
	sampleRate             int
	allowShoutcastMetadata bool
	blockOnNoAudio         bool

	sync          *sync.Mutex
	clientTimeout chan *Client
	listeners     map[string]*Client
	queue         []*Audio
	playing       *Audio
}

func (s *Stream) Append(a *Audio) {
	s.sync.Lock()
	s.queue = append(s.queue, a)
	s.sync.Unlock()
}

type RegisterOpts func(*Client)

func SupportsShoutCastMetadata() RegisterOpts {
	return func(c *Client) { c.supportShoutCastMetadata = true }
}

func (s *Stream) Register(opts ...RegisterOpts) (*Client, error) {
	uuid, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	c := &Client{
		uuid:   uuid.String(),
		stream: make(chan []byte),
	}

	for _, o := range opts {
		o(c)
	}

	s.listeners[uuid.String()] = c
	return c, nil
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
		if s.playing == nil {
			playing, err := s.nextAudio()
			if err != nil {
				if err == ErrNoAudioInQueue {
					continue
				}
				return err
			}
			s.playing = playing
		}

		// read a frame from audio
		frame := make([]byte, s.frameSize)
		bytes, err := s.playing.Read(frame)
		if err != nil {
			// we are done reading this audio file
			if err == io.EOF {
				s.playing = nil
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
				case c.stream <- frame:
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
