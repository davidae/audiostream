package icestream

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"
)

const (
	defaultListenerTimeout  = time.Millisecond * 500
	defaultFrameSize        = 3000
	defaultMetadataInterval = 65536

	// MaxMetaDataSize is the maximum size for meta data (everything over is truncated)
	// Must be a multiple of 16 which fits into one byte. Maximum: 16 * 255 = 4080
	MaxMetaDataSize = 4080
)

var (
	ErrNoAudioInQueue   = errors.New("no audio in stream queue")
	ErrListenerNotFound = errors.New("listener not found amoung active listeners")
)

type StreamOpts func(s *Stream)

func WithFramzeSize(size int) StreamOpts {
	return func(s *Stream) { s.frameSize = size }
}

func WithShoutcastMetadata(interval uint64) StreamOpts {
	return func(s *Stream) {
		s.allowShoutcastMetadata = true
		s.metadataInterval = interval
	}
}

// a very rough estimation of the playtime of the frame
func DefaultSleepForFunc() SleepFor {
	return func(broadcastTime time.Duration, numBytes, sampleRate int) time.Duration {
		playtime := time.Duration(float64(time.Millisecond) * (float64(numBytes) / float64(sampleRate)) * 1000)
		return playtime - broadcastTime
	}
}

func WithLazyFileRead(fn SleepFor) StreamOpts {
	return func(s *Stream) {
		s.lazyFileReading = true
		s.fileReadSleepFn = fn
	}
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

type SleepFor func(broadcastTime time.Duration, numBytes, sampleRate int) time.Duration

type Stream struct {
	frameSize              int
	allowShoutcastMetadata bool
	metadataInterval       uint64
	lazyFileReading        bool
	fileReadSleepFn        SleepFor

	audioMux, clientMux *sync.Mutex
	listeners           map[string]*Listener
	queue               []*Audio
	reading             *Audio
	isStop              bool
}

func NewStream(opts ...StreamOpts) *Stream {
	s := &Stream{
		audioMux:  &sync.Mutex{},
		clientMux: &sync.Mutex{},
		listeners: make(map[string]*Listener),
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

func (s *Stream) AppendAudio(a *Audio) {
	s.audioMux.Lock()
	s.queue = append(s.queue, a)
	s.audioMux.Unlock()
}

func (s *Stream) AddListener(ls ...*Listener) {
	for _, l := range ls {
		s.listeners[l.uuid] = l
	}
}

func (s *Stream) RemoveListener(l *Listener) error {
	s.clientMux.Lock()
	defer s.clientMux.Unlock()
	if _, ok := s.listeners[l.uuid]; !ok {
		return ErrListenerNotFound
	}
	delete(s.listeners, l.uuid)
	close(l.stop)
	close(l.frame)
	close(l.stream)
	return nil
}

func (s *Stream) Stop() {
	s.isStop = true
	for _, l := range s.listeners {
		s.RemoveListener(l)
	}

	s.listeners = make(map[string]*Listener)
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
		for _, l := range s.listeners {
			go func(l *Listener) {
				l.frame <- Frame{data: frame, artist: s.reading.Artist, title: s.reading.Title}
				wg.Done()
			}(l)
		}
		wg.Wait()
		s.clientMux.Unlock()

		finish := time.Now().Sub(start)
		if s.lazyFileReading && s.fileReadSleepFn != nil {
			time.Sleep(s.fileReadSleepFn(finish, bytes, s.reading.SampleRate))
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
