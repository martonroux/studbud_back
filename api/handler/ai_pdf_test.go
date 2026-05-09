package handler_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"studbud/backend/testutil"
)

func newPDFFormReader(t *testing.T, subjectID int64, mode string, pdfBytes []byte) (*bytes.Buffer, string) {
	t.Helper()
	form := new(bytes.Buffer)
	w := multipart.NewWriter(form)
	_ = w.WriteField("subject_id", strconv.FormatInt(subjectID, 10))
	if mode != "" {
		_ = w.WriteField("mode", mode)
	}
	fw, err := w.CreateFormFile("file", "test.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(pdfBytes)); err != nil {
		t.Fatalf("copy pdf: %v", err)
	}
	_ = w.Close()
	return form, w.FormDataContentType()
}

// largePDFFixture returns bytes of a PDF with strictly more than 30 pages.
// Looks for api/handler/testdata/large.pdf; skips the test if missing.
func largePDFFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "large.pdf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("missing api/handler/testdata/large.pdf (>30 pages required): %v", err)
	}
	return b
}

func TestGenerateFromPDF_ImageModeRejectsOver30Pages(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)
	subj := testutil.NewSubject(t, pool, u.ID)

	bigPDF := largePDFFixture(t)
	srv := newAIPDFServer(t, pool, &testutil.FakeAIClient{})
	tok := mintToken(t, u.ID, true, false)

	form, ct := newPDFFormReader(t, subj.ID, "image", bigPDF)
	req := httptest.NewRequest("POST", "/ai/flashcards/pdf", form)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("pdf_image_mode_unavailable")) {
		t.Errorf("body missing pdf_image_mode_unavailable: %s", w.Body.String())
	}
}
