package aipipeline

import (
	"strings"
	"testing"
)

func TestRenderPromptGenPrompt_IncludesPromptAndStyle(t *testing.T) {
	out, err := renderPromptGenPrompt(PromptGenValues{
		SubjectName: "Calc I",
		Target:      5,
		Style:       "standard",
		Focus:       "derivatives",
		Prompt:      "Explain power rule",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Calc I", "5 flashcards", "standard", "derivatives", "Explain power rule", "items"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRenderPromptGenPDF_AutoChaptersFlipsInstruction(t *testing.T) {
	on, _ := renderPromptGenPDF(PDFGenValues{SubjectName: "S", Style: "detailed", Coverage: "comprehensive", CoverageHint: "thorough", AutoChapters: true})
	off, _ := renderPromptGenPDF(PDFGenValues{SubjectName: "S", Style: "detailed", Coverage: "essentials", CoverageHint: "core", AutoChapters: false})
	if !strings.Contains(on, "chapters\" array") {
		t.Error("auto_chapters=true missing chapters instruction")
	}
	if strings.Contains(off, "You MAY propose chapter splits") {
		t.Error("auto_chapters=false should not mention chapter proposal")
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
