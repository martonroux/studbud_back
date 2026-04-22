package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidName is returned when a caller-supplied name would escape the store root.
var ErrInvalidName = errors.New("storage: invalid name")

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

// safePath joins root and name, rejecting names that contain separators or traversal segments.
func (s *FileStore) safePath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("%q:\n%w", name, ErrInvalidName)
	}
	return filepath.Join(s.root, name), nil
}

// Write writes the reader's bytes to <root>/<name> and returns the path.
func (s *FileStore) Write(name string, src io.Reader) (string, error) {
	path, err := s.safePath(name)
	if err != nil {
		return "", err
	}
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
	path, err := s.safePath(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s:\n%w", path, err)
	}
	return f, nil
}

// Remove deletes the file <root>/<name>. Missing files are not an error.
func (s *FileStore) Remove(name string) error {
	path, err := s.safePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s:\n%w", path, err)
	}
	return nil
}
