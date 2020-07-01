package audiostream

import (
	"github.com/gofrs/uuid"
)

// Listener is a client used to listen to the stream of audio
type Listener struct {
	uuid             string
	frame            chan frame
	stream           chan []byte
	startedToStream  bool
	metadataInterval int64

	supportMetadata bool
}

// ListenerOption is a func used to configure the listener upon initialization
type ListenerOption func(*Listener)

// WithIcyMetadataSupport is usually confirmed when checking the incoming
// request header icy-metadata: 1
func WithIcyMetadataSupport(interval int64) ListenerOption {
	return func(l *Listener) {
		l.supportMetadata = true
		l.metadataInterval = interval
	}
}

// NewListener initializes and return a Listener
func NewListener(opts ...ListenerOption) (*Listener, error) {
	uuid, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}

	l := &Listener{
		uuid:   uuid.String(),
		stream: make(chan []byte),
		frame:  make(chan frame),
	}

	for _, o := range opts {
		o(l)
	}

	return l, nil
}

// Stream returns a channel that sends audio from the stream
func (l *Listener) Stream() <-chan []byte {
	if l.startedToStream {
		return l.stream
	}

	l.startedToStream = true

	go func() {
		var framesWrittenInInterval int64

		for {
			dataFrame, ok := <-l.frame
			if !ok {
				return
			}

			frame := dataFrame.data
			// metadata should be within this frame now
			if l.supportMetadata && framesWrittenInInterval+int64(len(frame)) >= l.metadataInterval {
				// how much can we write to stream before we need to send metadata
				preMetadata := l.metadataInterval - framesWrittenInInterval
				if preMetadata > 0 {
					l.stream <- frame[:preMetadata]
					framesWrittenInInterval += preMetadata

					// remainder of the frame that is to be send after metadata
					frame = frame[preMetadata:]
				}
				// at this point in time, we've reached the interval
				framesWrittenInInterval = 0
				// write metdata
				metadata := dataFrame.IcyMetadata()
				l.stream <- metadata
				framesWrittenInInterval += int64(len(metadata))
				// write remainder frame
				l.stream <- frame
				framesWrittenInInterval += int64(len(frame))
				return
			}

			framesWrittenInInterval += int64(len(frame))
			l.stream <- frame
		}

	}()

	return l.stream
}
