package memory

import (
	"io"
	"log/slog"
	"regexp"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// customIDAllowedRe codifies the allowed customID/tag character set so test
// assertions do not drift from the sanitizers in tags.go.
var customIDAllowedRe = regexp.MustCompile(`^[a-zA-Z0-9_:-]+$`)
