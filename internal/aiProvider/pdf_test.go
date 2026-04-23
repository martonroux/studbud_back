//go:build cgo

package aiProvider_test

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"studbud/backend/internal/aiProvider"
)

func TestPDFToImages_ReturnsOnePNGPerPage(t *testing.T) {
	pdf := loadTestPDF(t)
	imgs, err := aiProvider.PDFToImages(context.Background(), pdf, aiProvider.PDFOptions{
		MaxConcurrency: 2,
		PerPageTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("PDFToImages: %v", err)
	}
	if len(imgs) == 0 {
		t.Fatal("no images returned")
	}
	for i, img := range imgs {
		if img.MediaType != "image/png" {
			t.Errorf("img[%d].MediaType = %q, want image/png", i, img.MediaType)
		}
		if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
			t.Errorf("img[%d] not a PNG: %v", i, err)
		}
	}
}

func loadTestPDF(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "sample.pdf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no test PDF at %s: %v", path, err)
	}
	return b
}
