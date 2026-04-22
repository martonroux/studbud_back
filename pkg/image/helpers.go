package image

import (
	"bytes"
	"io"
	"os"
)

type bufReaderAt struct {
	*bytes.Reader
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
