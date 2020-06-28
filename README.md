# audiostream
audiostream is a simple streaming API for audio files. It can easily be used 
together with a HTTP server to stream audio.

This is package is used in an ongoing effort together with another private project. 

## What does the API do?
* It implements the [ICY protocol](https://cast.readme.io/docs/icy).  It's optional to use.
* It will read files (assumed audio/media but technically not required) from an append-able queue.
* It will broadcast the files to any listeners in frames of bytes and the listeners can be read as a stream via channels.

## What does the API _not_ do?
* It does not implement the SHOUTcast_ or _Icecast_ protocol.
* It cannot decode or convert audio files. A decoder package can solve this and _ffmpeg_ can deal with converting sample rates if needed.

# Example
See how to use the audiostream together with an HTTP server in `examples/main.go`.

# Inspiration
* https://www.semicolonworld.com/question/47601/how-to-stream-mp3-data-via-websockets-with-node-js-and-socket-io
* https://stackoverflow.com/questions/35102278/python-3-get-song-name-from-internet-radio-stream
* https://github.com/krotik/dudeldu
* https://gist.github.com/jucrouzet/3e59877c0b4352966e6220034f2b84ac
* https://stackoverflow.com/a/57634692