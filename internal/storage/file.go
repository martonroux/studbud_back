package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStore writes and reads image files from a filesystem root directory.
type FileStore struct {
	root string // root is the base directory (e.g. "./uploads")
}

// NewFileStore constructs a FileStore rooted at the given directory.
// The directory is created if it does not exist.
func NewFileStore(root string) (*FileStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir uploads root:\n%w", err)
	}
	return &FileStore{root: root}, nil
}

// Write writes the reader's bytes to <root>/<name> and returns the path.
func (s *FileStore) Write(name string, src io.Reader) (string, error) {
	path := filepath.Join(s.root, name)
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %s:\n%w", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return "", fmt.Errorf("write %s:\n%w", path, err)
	}
	return path, nil
}

// Open opens the file <root>/<name> for reading.
func (s *FileStore) Open(name string) (*os.File, error) {
	path := filepath.Join(s.root, name)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s:\n%w", path, err)
	}
	return f, nil
}

// Remove deletes the file <root>/<name>. Missing files are not an error.
func (s *FileStore) Remove(name string) error {
	path := filepath.Join(s.root, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s:\n%w", path, err)
	}
	return nil
}
