package prompt

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"text/template"
	"time"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates *template.Template

func init() {
	templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))
}

// Build renders a named template with the given data.
func Build(name string, data any) (rendered string, err error) {
	started := time.Now()
	defer func() {
		level := slog.LevelDebug
		if err != nil {
			level = slog.LevelError
		}
		slog.Log(context.Background(), level, "prompt template rendering completed", "template", name,
			"output_bytes", len(rendered), "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}()
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", name, err)
	}
	rendered = buf.String()
	return rendered, nil
}

// FileReviewData is the data passed to the file review prompt template.
type FileReviewData struct {
	FileName string
	PRNumber int
	PRTitle  string
	PRAuthor string
	Diff     string
	Rules    string // formatted rules for the prompt
}

// SynthesisData is the data passed to the synthesis prompt template.
type SynthesisData struct {
	PRTitle     string
	PRAuthor    string
	FileCount   int
	FileReviews string // serialized file reviews
}
