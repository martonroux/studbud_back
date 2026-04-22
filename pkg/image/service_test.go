package image

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"studbud/backend/internal/storage"
	"studbud/backend/testutil"
)

// 1x1 PNG (red pixel).
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
	0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x2F, 0xC0, 0x0F, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestUploadOpenDelete(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	dir, err := os.MkdirTemp("", "imgtest-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, _ := storage.NewFileStore(dir)
	svc := NewService(pool, store, "http://localhost:8080")

	img, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pngBytes), "pic.png")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if img.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", img.MimeType)
	}

	rc, mime, err := svc.Open(context.Background(), img.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if mime != "image/png" || len(b) == 0 {
		t.Fatalf("Open returned bad data")
	}

	if err := svc.Delete(context.Background(), u.ID, img.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := svc.Open(context.Background(), img.ID); err == nil {
		t.Fatal("expected error after Delete")
	}
}
