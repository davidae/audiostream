package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/davidae/audiostream"
)

func main() {
	lazyFileRead := audiostream.WithLazyFileRead(audiostream.DefaultLazyWaitFunc())
	stream := audiostream.NewStream(lazyFileRead)
	audio, err := os.Open("your-mp3-file.mp3")
	if err != nil {
		panic(err)
	}

	stream.AppendAudio(&audiostream.Audio{
		Data:       audio,
		Artist:     "Fizz",
		Title:      "Buzz",
		SampleRate: 44100, // a naive assumption, use a decoder to know for sure.
	})

	http.HandleFunc("/audio", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			panic(errors.New("need a flusher for keep alive"))
		}
		w.Header().Set("Connection", "Keep-Alive")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Content-Type", "audio/mpeg")

		listener, err := audiostream.NewListener()
		if err != nil {
			panic(err)
		}

		stream.AddListener(listener)

		endLoop := false
		for !endLoop {
			select {
			case <-time.After(time.Second * 2):
				// timeout
				endLoop = true
			case out := <-listener.Stream():
				binary.Write(w, binary.BigEndian, out)
				flusher.Flush()
			}
		}

		stream.RemoveListener(listener)
	})

	go func() {
		for {
			fmt.Printf("dequeued, queue size is now %d\n", <-stream.Dequeued())
		}
	}()

	go func() {
		if err := stream.Start(); err != nil {
			panic(err)
		}
	}()

	http.ListenAndServe(":8080", nil)
}

// To easily listen in,
// mplayer http://localhost:8080/audio -v
