package image

import (
	"bytes"
	"io"
	"os"
)

// bufReaderAt adapts a *bytes.Reader to io.ReaderAt for re-reading the sniffed header.
type bufReaderAt struct {
	*bytes.Reader // Reader is the underlying in-memory byte buffer implementing ReaderAt
}

func newBufReaderAt(b []byte) io.ReaderAt {
	return &bufReaderAt{bytes.NewReader(b)}
}

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
