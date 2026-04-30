package aipipeline_test

import (
	"context"
	"testing"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/testutil"
)

func TestRankCrossSubjects_HappyPath(t *testing.T) {
	body := `{"selectedIds":[12,205,308]}`
	svc := aipipeline.NewServiceForTest(nil, &testutil.FakeAIClient{
		Chunks: []aiProvider.Chunk{{Text: body, Done: true}},
	}, "claude-test")

	out, err := svc.RankCrossSubjects(context.Background(), aipipeline.RankInput{
		ExamSubject: "Biologie",
		ExamTitle:   "Partiel",
		Candidates: []aipipeline.CrossSubjectCandidate{
			{ID: 12, Title: "Mitose", SubjectName: "Microbio", Keywords: []string{"mitose"}, OverlapScore: 2},
			{ID: 205, Title: "Cycle", SubjectName: "Biochimie", Keywords: []string{"cycle"}, OverlapScore: 3},
			{ID: 308, Title: "ADN", SubjectName: "Biochimie", Keywords: []string{"chromosome"}, OverlapScore: 1},
		},
		TopK: 15,
	})
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(out.SelectedIDs) != 3 || out.SelectedIDs[0] != 12 {
		t.Errorf("unexpected ids: %+v", out.SelectedIDs)
	}
}

func TestRankCrossSubjects_EmptyCandidates(t *testing.T) {
	svc := aipipeline.NewServiceForTest(nil, &testutil.FakeAIClient{}, "claude-test")
	out, err := svc.RankCrossSubjects(context.Background(), aipipeline.RankInput{TopK: 15})
	if err != nil {
		t.Fatalf("expected silent success on empty input, got %v", err)
	}
	if len(out.SelectedIDs) != 0 {
		t.Errorf("want empty, got %+v", out.SelectedIDs)
	}
}
