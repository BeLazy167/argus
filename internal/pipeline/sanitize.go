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
