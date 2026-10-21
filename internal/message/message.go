// Package message normalizes and validates Conventional Commits messages.
package message

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Type is a Conventional Commits type and what it's for.
type Type struct {
	Name    string
	Meaning string
}

// DefaultTypes are the allowed types unless ai-commit.types replaces them.
var DefaultTypes = []Type{
	{"feat", "adds a new feature"},
	{"fix", "fixes a bug"},
	{"build", "build system or external dependencies"},
	{"chore", "maintenance that doesn't touch source or tests"},
	{"ci", "CI configuration and scripts"},
	{"docs", "documentation only"},
	{"style", "formatting only, no change in meaning"},
	{"refactor", "code change that neither fixes a bug nor adds a feature"},
	{"perf", "performance improvement"},
	{"test", "adds or corrects tests"},
	{"revert", "reverts earlier commits; footer `Refs: <sha>[, <sha>]`"},
}

// Normalize tidies a model's reply without changing its meaning: CRLF line
// endings and surrounding whitespace go, the type is lowercased, the body is
// separated from the header by a blank line, and a BREAKING CHANGE footer
// token is uppercased.
func Normalize(reply string) string {
	msg := strings.TrimSpace(strings.ReplaceAll(reply, "\r\n", "\n"))
	if msg == "" {
		return ""
	}
	lines := strings.Split(msg, "\n")
	if i := strings.IndexAny(lines[0], "(!:"); i >= 0 {
		lines[0] = strings.ToLower(lines[0][:i]) + lines[0][i:]
	}
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
		lines = slices.Insert(lines, 1, "")
	}
	if start, ok := footerStart(lines); ok {
		for i := start; i < len(lines); i++ {
			lines[i] = upperBreakingChange(lines[i])
		}
	}
	return strings.Join(lines, "\n")
}

// footerStart returns the index of the first line of the footer block: the
// last paragraph, if it isn't the header and starts like a footer.
func footerStart(lines []string) (int, bool) {
	i := len(lines)
	for i > 0 && strings.TrimSpace(lines[i-1]) != "" {
		i--
	}
	return i, i >= 2 && i < len(lines) && isFooter(lines[i])
}

// isFooter reports whether line looks like `Token: value` or `Token #value`,
// where the token is hyphenated or `BREAKING CHANGE`.
func isFooter(line string) bool {
	i := -1
	for _, sep := range []string{": ", " #"} {
		if j := strings.Index(line, sep); j >= 0 && (i < 0 || j < i) {
			i = j
		}
	}
	if i <= 0 {
		return false
	}
	token := line[:i]
	if strings.EqualFold(token, "breaking change") {
		return true
	}
	return !strings.ContainsFunc(token, func(r rune) bool {
		return r != '-' && (r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r))
	})
}

func upperBreakingChange(line string) string {
	const n = len("breaking change")
	if len(line) < n {
		return line
	}
	token, rest := line[:n], line[n:]
	if !strings.EqualFold(token, "breaking change") && !strings.EqualFold(token, "breaking-change") {
		return line
	}
	if !strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, " #") {
		return line
	}
	return strings.ToUpper(token) + rest
}

// Validate reports why msg isn't a valid Conventional Commits message with one
// of the given types, or nil. A line starting with commentPrefix is invalid
// because git would strip it; an empty commentPrefix means git strips nothing.
func Validate(msg string, types []string, commentPrefix string) error {
	header, _, _ := strings.Cut(msg, "\n")
	if header == "" {
		return errors.New("the message is empty")
	}
	head, desc, ok := strings.Cut(header, ":")
	if !ok {
		return errors.New("the header must look like `type(scope)!: description`")
	}
	if desc, ok = strings.CutPrefix(desc, " "); !ok {
		return errors.New("`:` must be followed by a single space and the description")
	}
	if desc == "" || strings.TrimLeftFunc(desc, unicode.IsSpace) != desc {
		return errors.New("the description must be non-empty and start right after `: `")
	}

	typ := strings.TrimSuffix(head, "!")
	if i := strings.IndexByte(typ, '('); i >= 0 {
		scope, ok := strings.CutSuffix(typ[i+1:], ")")
		if !ok {
			return errors.New("the scope must end with `)` right before `:` (or `!:`)")
		}
		if scope == "" || strings.ContainsAny(scope, "()") {
			return errors.New("the scope must be a non-empty noun without parentheses")
		}
		typ = typ[:i]
	}
	if !slices.ContainsFunc(types, func(t string) bool { return strings.EqualFold(t, typ) }) {
		return fmt.Errorf("type `%s` is not allowed; use one of: %s", typ, strings.Join(types, ", "))
	}

	if commentPrefix != "" {
		for line := range strings.Lines(msg) {
			if strings.HasPrefix(line, commentPrefix) {
				return fmt.Errorf("no line may start with `%s`: git strips it as a comment", commentPrefix)
			}
		}
	}
	return nil
}
