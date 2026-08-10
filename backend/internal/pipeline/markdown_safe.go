// Package pipeline — markdown_safe.go is the RENDER path for untrusted text.
//
// Argus posts a review comment on the PR and keeps editing named sections of
// it. Several of those sections quote text Argus does not own: issue titles,
// acceptance criteria lifted from an issue body, linked-PR titles, fetch
// errors. Anyone who can open an issue in a linked repository controls that
// text, and they need no access at all to the repository being reviewed.
//
// This file is deliberately NOT sanitize.go. That file is the PROMPT path:
// it redacts injection prefixes ("SYSTEM:", "ignore all previous
// instructions") because an LLM reads them as instructions. A human reading a
// GitHub comment does not, so redacting them here would mangle honest titles
// while fixing nothing. The property that matters when rendering is
// structural: quoted text must render as one inline run and nothing more.
package pipeline

import (
	"strings"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// markdownBlockOpeners are the ASCII punctuation characters that open a
// markdown block construct when they are first on a line: ATX headings,
// list bullets, blockquotes, table rows, setext underlines, thematic
// breaks, fences, and link reference definitions.
const markdownBlockOpeners = "#>-+*|=~_["

// safeMarkdownField prepares untrusted text for interpolation into markdown
// Argus POSTS, at a position where markdown is rendered normally.
//
// Guarantee: the result renders as inline text. It cannot open a block
// construct, cannot start a second line, and cannot emit HTML — whatever
// position a caller drops it in.
//
// Each step names the forgery it prevents:
//
//  1. strings.Fields collapses every whitespace run, newlines included.
//     util.Truncate alone keeps newlines, so an issue title of
//     "Fix parser\n## Verdict: approved" forges a heading inside Argus's own
//     comment, indistinguishable from one Argus wrote.
//  2. '<' becomes the &lt; entity. A field carrying
//     "<!-- argus:joint_acceptance:end -->" plants a second copy of the
//     sticky section marker inside the section body; the next
//     UpdateStickySection counts two end markers and returns
//     ErrMarkersCorrupt, so that section can never be updated again. The
//     entity is also strictly better rendering: GitHub swallows "<T>" as an
//     unknown tag today, and "&lt;T>" shows it.
//  3. Backticks are escaped so the value cannot open a code span that runs on
//     into Argus's own trailing text.
//  4. A leading block-opener is backslash-escaped. Every call site today puts
//     the value mid-line, where the character is inert; escaping keeps the
//     guarantee self-contained if a formatter ever renders it first on a
//     line. GFM consumes the backslash, so the reader still sees the
//     original character.
//
// Truncation sits between the shrinking step and the growing ones: max then
// bounds the source text a reader sees, and a cut can never land inside an
// entity or split a backslash from what it escapes.
func safeMarkdownField(s string, max int) string {
	out := util.Truncate(strings.Join(strings.Fields(s), " "), max, true)
	out = strings.ReplaceAll(out, "<", "&lt;")
	out = strings.ReplaceAll(out, "`", "\\`")
	return escapeLeadingBlockOpener(out)
}

// safeMarkdownCode prepares untrusted text for interpolation INSIDE a `code
// span`. Same threat, different rules, because a code span is not markdown:
//
//   - Backticks are dropped, not escaped. A backslash is literal inside a code
//     span, so escaping would print "\`" and still close the span, letting the
//     rest of the value render as markdown.
//   - No leading-character escape, for the same reason: nothing inside a code
//     span opens a block, and the backslash would be visible.
//   - '<' is still escaped. The sticky marker search is a literal
//     strings.Count over the raw comment body and does not care that the
//     marker sits inside a code span. An entity is visible there, which is the
//     price of not letting a path wedge the section; paths carrying '<' do not
//     occur in practice.
func safeMarkdownCode(s string, max int) string {
	collapsed := strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "`", "")
	return strings.ReplaceAll(util.Truncate(collapsed, max, true), "<", "&lt;")
}

// escapeLeadingBlockOpener backslash-escapes s's first character when it would
// open a markdown block at the start of a line.
//
// Byte indexing is safe: a multi-byte rune's lead byte is >= 0x80 and can
// never match an ASCII opener.
func escapeLeadingBlockOpener(s string) string {
	if s == "" {
		return s
	}
	if strings.IndexByte(markdownBlockOpeners, s[0]) >= 0 {
		return `\` + s
	}
	// "1." and "1)" open an ordered list. Escape the delimiter rather than the
	// digits so the number stays readable.
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') {
		return s[:i] + `\` + s[i:]
	}
	return s
}
