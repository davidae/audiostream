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
	// ErrNoAudioInQueue is an error stating that there are no more audio to stream in the queue
	ErrNoAudioInQueue = errors.New("no audio in stream queue")
	// ErrListenerNotFound is an error stating that a listener was not found amount active listener in a stream
	ErrListenerNotFound = errors.New("listener not found amoung active listeners")
)

// StreamOption is a func used to configure the streamer upon initialization
type StreamOption func(s *Stream)

// WithFramzeSize sets the frame size, which if the size of bytes used when reading a block of audio
func WithFramzeSize(size int) StreamOption {
	return func(s *Stream) { s.frameSize = size }
}

// DefaultSleepForFunc is the default function one can use to estimate the time the streamer can
// sleep after broadcast a frame to all the listeners. It is used in conjunction with WithLazyFileRead
// The formula is a very rough estimation of the playtime of the frame
func DefaultSleepForFunc() SleepFor {
	return func(broadcastTime time.Duration, numBytes, sampleRate int) time.Duration {
		playtime := time.Duration(float64(time.Millisecond) * (float64(numBytes) / float64(sampleRate)) * 1000)
		return playtime - broadcastTime
	}
}

// WithLazyFileRead is an option that will halt the stream from reading an audio file overzealously and
// passing it on to the listeners. This can be useful if we want to keep the in-memory low and
// avoid often empty queueus
func WithLazyFileRead(fn SleepFor) StreamOption {
	return func(s *Stream) {
		s.lazyFileReading = true
		s.fileReadSleepFn = fn
	}
}

// Audio is a simple description of an audio item, it's data and metadata
type Audio struct {
	Data          io.Reader
	SampleRate    int
	Title, Artist string
	Filename      string
}

// Read will read the audio data
func (a *Audio) Read(b []byte) (int, error) { return a.Data.Read(b) }

// Frame is a simple abstraction of what a stream will send to its listeners
type Frame struct {
	data          []byte
	title, artist string
}

// ShoutcastMetadata will build a frame to send metadata to a client that can
// decode/parse ShoutCast metadata as a part of the audio stream from a listener
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

// SleepFor is a function used to determine how much to sleep when using lazy read
type SleepFor func(broadcastTime time.Duration, numBytes, sampleRate int) time.Duration

// Stream is responsible for reading and broadcasting to the data to listeners
type Stream struct {
	frameSize       int
	lazyFileReading bool
	fileReadSleepFn SleepFor

	audioMux, clientMux *sync.Mutex
	listeners           map[string]*Listener
	queue               []*Audio
	reading             *Audio
	eof                 chan int
	isStop              bool
}

// NewStream initiates and returns a Stream
func NewStream(opts ...StreamOption) *Stream {
	s := &Stream{
		audioMux:  &sync.Mutex{},
		clientMux: &sync.Mutex{},
		listeners: make(map[string]*Listener),
		queue:     []*Audio{},
		eof:       make(chan int),
	}

	for _, o := range opts {
		o(s)
	}

	if s.frameSize == 0 {
		s.frameSize = defaultFrameSize
	}

	return s
}

// AppendAudio adds an audio file to the stream to be read and broadcasted to listeners
func (s *Stream) AppendAudio(a *Audio) {
	s.audioMux.Lock()
	s.queue = append(s.queue, a)
	s.audioMux.Unlock()
}

// AddListener adds a new listener to the stream to broadcast audio data
func (s *Stream) AddListener(ls ...*Listener) {
	for _, l := range ls {
		s.listeners[l.uuid] = l
	}
}

// RemoveListener removes a listener from a stream
func (s *Stream) RemoveListener(l *Listener) error {
	s.clientMux.Lock()
	defer s.clientMux.Unlock()
	if _, ok := s.listeners[l.uuid]; !ok {
		return ErrListenerNotFound
	}
	delete(s.listeners, l.uuid)
	close(l.frame)
	close(l.stream)
	return nil
}

// Stop stops the broadcasting initiated by Start()
// You cannot re Start() a stream. This function is maybe a bit useless.
func (s *Stream) Stop() {
	s.isStop = true
}

// EndOfFile sends the number of Audio items in the queue after an audio file
// has been dropped dropped from the queue, and read and brodcasted all the data
// completely (EOF) to the listeners.
// This can be useful you want to reduce the number of files held in the queue
func (s *Stream) EndOfFile() <-chan int {
	return s.eof
}

// Start starts the stream.
// error ErrNoAudioInQueue might be returned
func (s *Stream) Start() error {
	for !s.isStop {
		// are we done reading/playing a song?
		if s.reading == nil {
			reading, err := s.dequeue()
			if err != nil {
				// ignoring this error for now, might add a callback or smth in the future
				if err == ErrNoAudioInQueue {
					time.Sleep(time.Second)
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
				select {
				case s.eof <- len(s.queue):
				case <-time.After(time.Millisecond * 100):
				}
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
