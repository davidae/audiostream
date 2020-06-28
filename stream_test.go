package icestream_test

import (
	"strings"
	"testing"
	"time"

	"github.com/davidae/icestream"
)

func TestStreamingWhenAudioQueueISEmpty(t *testing.T) {
	s := icestream.NewStream()
	err := s.Start(func(c *icestream.Client) {})

	if err != icestream.ErrNoAudioInQueue {
		t.Errorf("expected error %s, but got %s", icestream.ErrNoAudioInQueue, err)
	}
}

func TestStreamingToTwoClientsWithNoMetadata(t *testing.T) {
	data := "123456789 101112131415161718192021222324252627"
	s := icestream.NewStream(icestream.WithFramzeSize(2))
	c1, err := s.Register()
	noError(err, t)
	c2, err := s.Register()
	noError(err, t)

	s.Append(&icestream.Audio{
		Artist:   "Foo ft. Bar",
		Title:    "Hello world",
		Filename: "song.mp3",
		Data:     strings.NewReader(data),
	})

	go func() {
		// ignore error return here, not testing that.
		s.Start(func(c *icestream.Client) {
			t.Errorf("client timed out: %#v", c)
		})
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
	s.Deregister(c1, c2)
	if outc1 != data {
		t.Errorf("expected client 1 to have streamed %q, but got %q", data, outc1)
	}
	if outc2 != data {
		t.Errorf("expected client 2 to have streamed %q, but got %q", data, outc2)
	}
}

func noError(err error, t *testing.T) {
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}
