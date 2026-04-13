package prompt

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates *template.Template

func init() {
	templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))
}

// Build renders a named template with the given data.
func Build(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", name, err)
	}
	return buf.String(), nil
}

// FileReviewData is the data passed to the file review prompt template.
type FileReviewData struct {
	FileName  string
	PRNumber  int
	PRTitle   string
	PRAuthor  string
	Diff      string
	Rules     string // formatted rules for the prompt
}

// SynthesisData is the data passed to the synthesis prompt template.
type SynthesisData struct {
	PRTitle     string
	PRAuthor    string
	FileCount   int
	FileReviews string // serialized file reviews
}
