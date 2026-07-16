package pipeline

import (
	"regexp"
	"strings"
)

// injectionPatterns matches common prompt injection prefixes in non-code user input.
var injectionPatterns = regexp.MustCompile(
	`(?i)(?:^|\n)\s*(?:` +
		`ignore (?:all |any )?(?:previous|above|prior) (?:instructions|prompts|rules)` +
		`|forget (?:your|all|the) (?:instructions|rules|prompt)` +
		`|you are now` +
		`|SYSTEM:\s` +
		`|disregard (?:all|the|your)` +
		`|override (?:the |your )?(?:system|rules|instructions)` +
		`|new instructions:` +
		`|do not review` +
		`|approve (?:this|the) (?:PR|pull request|code) (?:without|regardless)` +
		`)`)

// sanitizeUserInput strips known prompt injection patterns from non-code user input
// (PR titles, commit messages, PR body). Does NOT sanitize code/diffs — those strings
// could be legitimate in source code.
func sanitizeUserInput(s string) string {
	return injectionPatterns.ReplaceAllString(s, "[redacted]")
}

// wrapInDelimiters wraps content in XML-style delimiters that instruct the LLM
// to treat the content as data, not instructions.
func wrapInDelimiters(tag, content string) string {
	return "<" + tag + ">\n" + content + "\n</" + tag + ">"
}

// wrapSafeDelimiters wraps content in <tag>…</tag> after neutralising any literal
// `tag` delimiter tokens inside content, so the content cannot close its own
// delimiter (the wrap + tag-scrub halves of the prompt-safety idiom in one call).
// The tag is a fixed literal chosen by the caller — never user input.
func wrapSafeDelimiters(tag, content string) string {
	return wrapInDelimiters(tag, scrubDelimiterToken(tag, content))
}

// scrubDelimiterToken breaks any <tag> / </tag> occurrences (case-insensitive)
// inside content by swapping just the delimiter's ASCII angle brackets for
// unicode look-alikes, so the LLM reads them as data rather than a closing tag.
// Only the exact delimiter shape is touched: arbitrary '<' / '>' in code or diffs
// (generics, comparisons, HTML) are preserved, so it is safe on source text that
// wrapSafeDelimiters must not corrupt.
func scrubDelimiterToken(tag, content string) string {
	if content == "" {
		return content
	}
	re := regexp.MustCompile(`(?i)<\s*/?\s*` + regexp.QuoteMeta(tag) + `\s*>`)
	return re.ReplaceAllStringFunc(content, func(m string) string {
		m = strings.ReplaceAll(m, "<", "‹")
		return strings.ReplaceAll(m, ">", "›")
	})
}

// ValidateCustomPrompt checks a user-written custom prompt for manipulation attempts.
// Returns the prompt if valid, or an error message if blocked.
func ValidateCustomPrompt(prompt string) (string, string) {
	if len(prompt) > 2000 {
		return "", "Custom prompt exceeds 2000 character limit"
	}
	lower := strings.ToLower(prompt)
	blocklist := []string{
		"ignore all previous",
		"ignore prior instructions",
		"override system prompt",
		"forget your instructions",
		"disregard all rules",
		"you are now a",
		"new system prompt",
		"output your system prompt",
		"reveal your instructions",
		"print your prompt",
	}
	for _, pattern := range blocklist {
		if strings.Contains(lower, pattern) {
			return "", "Custom prompt contains blocked pattern: " + pattern
		}
	}
	return prompt, ""
}
