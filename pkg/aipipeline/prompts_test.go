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
