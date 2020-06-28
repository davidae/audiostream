package icestream_test

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/davidae/icestream"
)

// func TestStreamingWhenAudioQueueISEmpty(t *testing.T) {
// 	s := icestream.NewStream()
// 	err := s.Start()

// 	if err != icestream.ErrNoAudioInQueue {
// 		t.Errorf("expected error %s, but got %s", icestream.ErrNoAudioInQueue, err)
// 	}
// }

func TestStreamingToTwoListenersWithNoMetadata(t *testing.T) {
	data := "123456789 101112131415161718192021222324252627"
	s := icestream.NewStream(icestream.WithFramzeSize(2))
	c1, err := icestream.NewListener()
	noError(err, t)
	c2, err := icestream.NewListener()
	noError(err, t)

	s.AddListener(c1, c2)

	s.AppendAudio(&icestream.Audio{
		Artist:     "Foo ft. Bar",
		Title:      "Hello world",
		Filename:   "song.mp3",
		Data:       strings.NewReader(data),
		SampleRate: 44100,
	})

	go func() {
		// ignore error return here, not testing that.
		s.Start()
	}()

	var outc1, outc2 string
	end := false
	for {
		select {
		case msg := <-c1.Stream():
			outc1 += string(msg)
		case msg := <-c2.Stream():
			outc2 += string(msg)
		case <-time.After(time.Second):
			end = true
		}
		if end {
			break
		}
	}

	s.Stop()
	if outc1 != data {
		t.Errorf("expected client 1 to have streamed %q, but got %q", data, outc1)
	}
	if outc2 != data {
		t.Errorf("expected client 2 to have streamed %q, but got %q", data, outc2)
	}
}

func TestStreamingToListenerWithMultipleFiles(t *testing.T) {
	data := "123456789 101112131415161718192021222324252627"
	s := icestream.NewStream(icestream.WithFramzeSize(2))
	c1, err := icestream.NewListener()
	noError(err, t)

	s.AddListener(c1)
	audio := &icestream.Audio{
		Artist:     "Foo ft. Bar",
		Title:      "Hello world",
		Filename:   "song.mp3",
		Data:       strings.NewReader(data),
		SampleRate: 44100,
	}

	s.AppendAudio(audio)
	s.AppendAudio(audio)
	s.AppendAudio(audio)

	go func() {
		s.Start()
	}()

	go func() {
		for {
			q := <-s.EndOfFile()
			if q != 2 {
				t.Errorf("expected queue to be 2, got %d", q)
			}
			q = <-s.EndOfFile()
			if q != 1 {
				t.Errorf("expected queue to be 1, got %d", q)
			}
			q = <-s.EndOfFile()
			if q != 0 {
				t.Errorf("expected queue to be 0, got %d", q)
			}
		}
	}()

	var outc1 string
	end := false
	for {
		select {
		case msg := <-c1.Stream():
			outc1 += string(msg)
		case <-time.After(time.Second):
			end = true
		}
		if end {
			break
		}
	}

	s.Stop()
	if outc1 != data {
		t.Errorf("expected client 1 to have streamed %q, but got %q", data, outc1)
	}
}

func TestStreamingMetadataWithInterval(t *testing.T) {
	data := "123456789 101112131415161718192021222324252627"
	expectedStreamTitle := "Foo ft. Bar - Hello world"
	s := icestream.NewStream(
		icestream.WithFramzeSize(len(data)),
	)

	s.AppendAudio(&icestream.Audio{
		Artist:   "Foo ft. Bar",
		Title:    "Hello world",
		Filename: "song.mp3",
		Data:     strings.NewReader(data),
	})

	ts := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				panic(errors.New("need a flusher for keep alive"))
			}

			w.Header().Set("Connection", "Keep-Alive")
			w.Header().Set("Transfer-Encoding", "chunked")
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("icy-metadata", "1")
			w.Header().Set("icy-metaint", "10")
			w.Header().Set("icy-name", "hello world")

			client, err := icestream.NewListener(icestream.WithMetadataSupport(10))
			noError(err, t)
			s.AddListener(client)

			endLoop := false
			for !endLoop {
				select {
				case <-time.After(time.Second * 2):
					endLoop = true
				case out, ok := <-client.Stream():
					if !ok {
						endLoop = true
						break
					}
					binary.Write(w, binary.BigEndian, out)
					flusher.Flush()
				}
			}
			s.RemoveListener(client)
		}))

	defer ts.Close()

	go func() {
		s, err := GetStreamTitle(ts.URL)
		if err != nil {
			t.Errorf("unexpected error: %s", err)
		}
		if s != expectedStreamTitle {
			t.Errorf("expected stream title %s, but got %s", expectedStreamTitle, s)
		}
	}()
	time.Sleep(time.Second)
	go func() {
		// ignore error return here, not testing that.
		s.Start()
	}()

	time.Sleep(time.Second)
	s.Stop()
}

// BORROWED FROM https://gist.github.com/jucrouzet/3e59877c0b4352966e6220034f2b84ac
// GetStreamTitle get the current song/show in an Icecast stream
func GetStreamTitle(streamUrl string) (string, error) {
	m, err := getStreamMetas(streamUrl)

	if err != nil {
		return "", err
	}
	// Should be at least "StreamTitle=' '"
	if len(m) < 15 {
		return "", nil
	}
	// Split meta by ';', trim it and search for StreamTitle
	for _, m := range bytes.Split(m, []byte(";")) {
		m = bytes.Trim(m, " \t")
		if bytes.Compare(m[0:13], []byte("StreamTitle='")) != 0 {
			continue
		}
		return string(m[13 : len(m)-1]), nil
	}
	return "", nil
}

// get stream metadatas
func getStreamMetas(streamUrl string) ([]byte, error) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", streamUrl, nil)
	req.Header.Set("Icy-MetaData", "1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// We sent "Icy-MetaData", we should have a "icy-metaint" in return
	ih := resp.Header.Get("icy-metaint")
	if ih == "" {
		return nil, fmt.Errorf("no metadata")
	}
	// "icy-metaint" is how often (in bytes) should we receive the meta
	ib, err := strconv.Atoi(ih)
	if err != nil {
		return nil, err
	}

	reader := bufio.NewReader(resp.Body)

	// skip the first mp3 frame
	c, err := reader.Discard(ib)
	if err != nil {
		return nil, err
	}
	// If we didn't received ib bytes, the stream is ended
	if c != ib {
		return nil, fmt.Errorf("stream ended prematurally")
	}

	// get the size byte, that is the metadata length in bytes / 16
	sb, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	ms := int(sb * 16)

	// read the ms first bytes it will contain metadata
	m, err := reader.Peek(ms)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func noError(err error, t *testing.T) {
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}
