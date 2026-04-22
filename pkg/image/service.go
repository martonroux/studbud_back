package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/internal/storage"
)

// Image represents an uploaded image row.
type Image struct {
	ID       string // ID is the image primary key (ULID-like identifier)
	OwnerID  int64  // OwnerID is the user ID that uploaded the image
	Filename string // Filename is the name under which the file is stored on disk
	MimeType string // MimeType is the detected MIME type of the image
	Bytes    int64  // Bytes is the size of the stored file in bytes
}

// Service owns upload, fetch, and delete for images.
type Service struct {
	db         *pgxpool.Pool      // db is the Postgres connection pool
	store      *storage.FileStore // store is the filesystem image store
	backendURL string             // backendURL is the public base URL used to build image links
}

// NewService constructs the image service.
func NewService(db *pgxpool.Pool, store *storage.FileStore, backendURL string) *Service {
	return &Service{db: db, store: store, backendURL: backendURL}
}

// Upload reads src, detects content type, writes to storage, and records the DB row.
func (s *Service) Upload(ctx context.Context, uid int64, src io.Reader, filename string) (*Image, error) {
	sniff := make([]byte, 512)
	n, _ := io.ReadFull(src, sniff)
	mime := http.DetectContentType(sniff[:n])
	if !isAllowedImage(mime) {
		return nil, fmt.Errorf("unsupported mime type %q:\n%w", mime, myErrors.ErrValidation)
	}
	id := storage.NewImageID()
	diskName := id + extensionFor(mime)
	full := io.MultiReader(io.NewSectionReader(newBufReaderAt(sniff[:n]), 0, int64(n)), src)
	path, err := s.store.Write(diskName, full)
	if err != nil {
		return nil, err
	}
	size, err := fileSize(path)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `
        INSERT INTO images (id, owner_id, filename, mime_type, bytes)
        VALUES ($1, $2, $3, $4, $5)
    `, id, uid, diskName, mime, size)
	if err != nil {
		_ = s.store.Remove(diskName)
		return nil, fmt.Errorf("insert image:\n%w", err)
	}
	return &Image{ID: id, OwnerID: uid, Filename: diskName, MimeType: mime, Bytes: size}, nil
}

// Open returns an io.ReadCloser for the image and its mime type.
func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, string, error) {
	img, err := s.byID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	f, err := s.store.Open(img.Filename)
	if err != nil {
		return nil, "", err
	}
	return f, img.MimeType, nil
}

// Delete removes the image row and file if owned by uid.
func (s *Service) Delete(ctx context.Context, uid int64, id string) error {
	img, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	if img.OwnerID != uid {
		return fmt.Errorf("not owner:\n%w", myErrors.ErrForbidden)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM images WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete image row:\n%w", err)
	}
	return s.store.Remove(img.Filename)
}

// URL returns the public fetch URL for an image ID.
func (s *Service) URL(id string) string {
	return s.backendURL + "/images/" + id
}

func (s *Service) byID(ctx context.Context, id string) (*Image, error) {
	img := &Image{}
	err := s.db.QueryRow(ctx,
		`SELECT id, owner_id, filename, mime_type, bytes FROM images WHERE id = $1`, id).
		Scan(&img.ID, &img.OwnerID, &img.Filename, &img.MimeType, &img.Bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("image %s:\n%w", id, myErrors.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load image:\n%w", err)
	}
	return img, nil
}

func isAllowedImage(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

func extensionFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ""
}
