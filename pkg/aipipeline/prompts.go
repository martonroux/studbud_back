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
	return renderTemplate("prompts/generate_prompt.tmpl", v)
}

// renderPromptGenPDF renders the PDF-generation template with the given values.
func renderPromptGenPDF(v PDFGenValues) (string, error) {
	return renderTemplate("prompts/generate_pdf.tmpl", v)
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
	SubjectName string // SubjectName is the target subject's name
	Target      int    // Target is the requested card count (0 = auto)
	Style       string // Style is "short" | "standard" | "detailed"
	Focus       string // Focus is the optional narrowing text
	Prompt      string // Prompt is the user's free-text topic
}

// PDFGenValues is the template input for the PDF-mode generator.
type PDFGenValues struct {
	SubjectName  string // SubjectName is the target subject's name
	Style        string // Style is "short" | "standard" | "detailed"
	Coverage     string // Coverage is "essentials" | "balanced" | "comprehensive"
	CoverageHint string // CoverageHint is a short English explanation of the level
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
