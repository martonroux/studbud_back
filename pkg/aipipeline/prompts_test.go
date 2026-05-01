package aipipeline

import (
	"strings"
	"testing"
)

func TestRenderPromptGenPrompt_IncludesPromptStyleAndCoverage(t *testing.T) {
	out, err := renderPromptGenPrompt(PromptGenValues{
		SubjectName: "Calc I",
		Style:       "standard",
		Coverage:    "Balanced",
		Focus:       "derivatives",
		Prompt:      "Explain power rule",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Calc I", "standard", "Balanced", "derivatives", "Explain power rule", "items"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRenderPromptGenPrompt_AutoChaptersFlipsInstruction(t *testing.T) {
	on, _ := renderPromptGenPrompt(PromptGenValues{SubjectName: "S", Style: "standard", Coverage: "Balanced", Prompt: "p", AutoChapters: true})
	off, _ := renderPromptGenPrompt(PromptGenValues{SubjectName: "S", Style: "standard", Coverage: "Balanced", Prompt: "p", AutoChapters: false})
	if !strings.Contains(on, "chapters\" array") {
		t.Error("auto_chapters=true missing chapters instruction")
	}
	if strings.Contains(off, "You MAY propose chapter splits") {
		t.Error("auto_chapters=false should not mention chapter proposal")
	}
	if !strings.Contains(off, "Do NOT propose chapters") {
		t.Error("auto_chapters=false missing negative chapter instruction")
	}
}

func TestRenderPromptGenPDF_AutoChaptersFlipsInstruction(t *testing.T) {
	on, _ := renderPromptGenPDF(PDFGenValues{SubjectName: "S", Style: "detailed", Coverage: "Comprehensive", AutoChapters: true})
	off, _ := renderPromptGenPDF(PDFGenValues{SubjectName: "S", Style: "detailed", Coverage: "Core", AutoChapters: false})
	if !strings.Contains(on, "chapters\" array") {
		t.Error("auto_chapters=true missing chapters instruction")
	}
	if strings.Contains(off, "You MAY propose chapter splits") {
		t.Error("auto_chapters=false should not mention chapter proposal")
	}
	if !strings.Contains(off, "Do NOT propose chapters") {
		t.Error("auto_chapters=false missing negative chapter instruction")
	}
}

func TestCoverageHint_AllLevels(t *testing.T) {
	for _, c := range []string{"Core", "Balanced", "Comprehensive"} {
		if coverageHint(c) == "" {
			t.Errorf("coverageHint(%q) empty", c)
		}
	}
}

func TestRenderPromptCheck_EmbedsAllFields(t *testing.T) {
	out, err := renderPromptCheck(CheckValues{SubjectName: "S", Title: "T", Question: "Q?", Answer: "A."})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"verdict", "findings", "suggestion", "Q?", "A."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

func TestRenderExtractKeywords_EmbedsAllFields(t *testing.T) {
	out, err := RenderExtractKeywords(ExtractKeywordsValues{
		Title:    "Mitose",
		Question: "Quelles sont les phases de la mitose ?",
		Answer:   "Prophase, métaphase, anaphase, télophase.",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Mitose", "phases de la mitose", "Prophase", "keywords"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

func TestRenderCrossSubjectRank_IncludesAllInputs(t *testing.T) {
	out, err := RenderCrossSubjectRank(CrossSubjectRankValues{
		ExamSubject: "Biologie Cellulaire",
		ExamTitle:   "Partiel mitose",
		Candidates: []CrossSubjectCandidate{
			{ID: 12, Title: "Cycle cellulaire", SubjectName: "Microbiologie", Keywords: []string{"mitose", "cycle"}, OverlapScore: 2},
			{ID: 13, Title: "ADN", SubjectName: "Biochimie", Keywords: []string{"chromosome"}, OverlapScore: 1},
		},
		TopK: 15,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Biologie Cellulaire", "Partiel mitose", "Cycle cellulaire", "Microbiologie", "mitose", "15"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderRevisionPlan_IncludesAllSections(t *testing.T) {
	out, err := RenderRevisionPlan(RevisionPlanValues{
		ExamDate:      "2026-06-15",
		DaysRemaining: 30,
		ExamTitle:     "Partiel Biologie",
		ExamNotes:     "Focus on mitosis",
		SubjectName:   "Biologie Cellulaire",
		PrimaryCards: []PlanCardInfo{
			{ID: 12, Title: "Mitose", Keywords: []string{"mitose", "chromosome"}},
		},
		CrossSubjectCards: []PlanCardInfo{
			{ID: 205, Title: "Cycle procaryote", Keywords: []string{"cycle"}, SubjectName: "Microbiologie"},
		},
		UserStats: PlanUserStats{New: 42, Bad: 8, Ok: 15, Good: 67},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"2026-06-15", "30", "Partiel Biologie", "Focus on mitosis", "Mitose", "Cycle procaryote", "Microbiologie", "42"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
