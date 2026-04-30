package aipipeline

import (
	"context"
	"strings"
	"testing"

	"studbud/backend/internal/aiProvider"
)

type fakeKeywordProvider struct {
	body string
}

func (f *fakeKeywordProvider) Stream(ctx context.Context, _ aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	ch := make(chan aiProvider.Chunk, 1)
	ch <- aiProvider.Chunk{Text: f.body, Done: true}
	close(ch)
	return ch, nil
}

func TestExtractKeywords_HappyPath(t *testing.T) {
	body := `{"keywords":[{"keyword":"mitose","weight":1.0},{"keyword":"chromosome","weight":0.7}]}`
	svc := &Service{provider: &fakeKeywordProvider{body: body}, model: "claude-test"}
	out, err := svc.ExtractKeywords(context.Background(), ExtractInput{
		Title:    "Mitose",
		Question: "Q",
		Answer:   "A",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(out.Keywords) != 2 {
		t.Fatalf("want 2 keywords, got %d", len(out.Keywords))
	}
	if out.Keywords[0].Keyword != "mitose" || out.Keywords[0].Weight != 1.0 {
		t.Errorf("first kw mismatch: %+v", out.Keywords[0])
	}
}

func TestExtractKeywords_BadJSON(t *testing.T) {
	svc := &Service{provider: &fakeKeywordProvider{body: "not json"}, model: "claude-test"}
	_, err := svc.ExtractKeywords(context.Background(), ExtractInput{Question: "Q", Answer: "A"})
	if err == nil || !strings.Contains(err.Error(), "parse keywords") {
		t.Fatalf("want parse error, got %v", err)
	}
}
