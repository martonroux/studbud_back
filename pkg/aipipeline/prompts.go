package aipipeline

import (
	"bytes"
	"embed"
	"fmt"
	"sync"
	"text/template"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

// promptTemplates is the lazy-loaded cache of parsed prompt templates.
var promptTemplates = sync.Map{}

// renderPromptGenPrompt renders the prompt-generation template with the given values.
func renderPromptGenPrompt(v PromptGenValues) (string, error) {
	return renderTemplate("prompts/generate_prompt.tmpl", promptGenData{
		PromptGenValues: v,
		Hint:            coverageHint(v.Coverage),
	})
}

// renderPromptGenPDF renders the PDF-generation template with the given values.
func renderPromptGenPDF(v PDFGenValues) (string, error) {
	return renderTemplate("prompts/generate_pdf.tmpl", pdfGenData{
		PDFGenValues: v,
		Hint:         coverageHint(v.Coverage),
	})
}

// renderPromptCheck renders the check-flashcard template with the given values.
func renderPromptCheck(v CheckValues) (string, error) {
	return renderTemplate("prompts/check.tmpl", v)
}

// renderTemplate reads, parses (cached), and executes a template from the embedded FS.
func renderTemplate(path string, data any) (string, error) {
	tmpl, err := loadTemplate(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s:\n%w", path, err)
	}
	return buf.String(), nil
}

// loadTemplate returns the cached template for path, parsing once.
func loadTemplate(path string) (*template.Template, error) {
	if v, ok := promptTemplates.Load(path); ok {
		return v.(*template.Template), nil
	}
	raw, err := promptFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s:\n%w", path, err)
	}
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s:\n%w", path, err)
	}
	promptTemplates.Store(path, tmpl)
	return tmpl, nil
}

// PromptGenValues is the template input for the prompt-mode generator.
type PromptGenValues struct {
	SubjectName  string // SubjectName is the target subject's name
	Style        string // Style is "short" | "standard" | "detailed"
	Coverage     string // Coverage is "Core" | "Balanced" | "Comprehensive"
	Focus        string // Focus is the optional narrowing text
	Prompt       string // Prompt is the user's free-text topic
	AutoChapters bool   // AutoChapters enables "chapters" array in the output
}

// PDFGenValues is the template input for the PDF-mode generator.
type PDFGenValues struct {
	SubjectName  string // SubjectName is the target subject's name
	Style        string // Style is "short" | "standard" | "detailed"
	Coverage     string // Coverage is "Core" | "Balanced" | "Comprehensive"
	Focus        string // Focus is the optional narrowing text
	AutoChapters bool   // AutoChapters enables "chapters" array in the output
}

// CheckValues is the template input for the check-flashcard feature.
type CheckValues struct {
	SubjectName string // SubjectName is the owning subject
	Title       string // Title is the flashcard title
	Question    string // Question is the flashcard prompt
	Answer      string // Answer is the flashcard answer
}

// promptGenData augments PromptGenValues with the resolved coverage hint
// before template execution.
type promptGenData struct {
	PromptGenValues        // PromptGenValues is the caller-provided input
	Hint            string // Hint is the human-readable coverage explanation
}

// pdfGenData augments PDFGenValues with the resolved coverage hint
// before template execution.
type pdfGenData struct {
	PDFGenValues        // PDFGenValues is the caller-provided input
	Hint         string // Hint is the human-readable coverage explanation
}

// coverageHint returns a short English explanation of the coverage level.
// It is intentionally subject-agnostic; math vocabulary is illustrative.
func coverageHint(c string) string {
	switch c {
	case "Core":
		return "cover only the core notions: definitions and the central theorems / facts that are unavoidable to understand the subject"
	case "Comprehensive":
		return "cover everything substantive, including examples, remarks, edge cases, and connections between topics"
	default:
		return "cover the core notions plus secondary results — propositions, lemmas, named methods — that build on the core"
	}
}

// RenderPromptGen is the exported wrapper for the prompt-mode template.
func RenderPromptGen(v PromptGenValues) (string, error) { return renderPromptGenPrompt(v) }

// RenderPDFGen is the exported wrapper for the PDF-mode template.
func RenderPDFGen(v PDFGenValues) (string, error) { return renderPromptGenPDF(v) }

// RenderCheck is the exported wrapper for the check-flashcard template.
func RenderCheck(v CheckValues) (string, error) { return renderPromptCheck(v) }
