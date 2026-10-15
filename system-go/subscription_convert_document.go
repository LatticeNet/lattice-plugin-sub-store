package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/LatticeNet/lattice-sdk/model"
)

// Document substitution (S2 plan section 2.3). A document plan's text
// carries placeholders where the bound credentials belong, and convert puts
// each credential in its placeholder's place encoded for the scalar the
// placeholder sits in, because an identity password may hold any printable
// character and a quote or a backslash pasted byte for byte would end a YAML
// or JSON string early. No S2 record produces a document plan (files are
// S3), so this ships with its unit tests and S3 inherits a working sealed
// path.
//
// The encodings, by context:
//
//   - a YAML plain scalar that is exactly the placeholder becomes a
//     double-quoted YAML string;
//   - a YAML double-quoted scalar and a JSON string take JSON escapes, which
//     YAML's double-quoted style reads the same way;
//   - a YAML single-quoted scalar takes its one escape, a doubled quote;
//   - a URI's userinfo or query value takes percent-encoding of every byte
//     outside the unreserved set;
//   - a line that is the placeholder alone takes the bare value.
//
// A placeholder in a block scalar, a comment or any context not listed is
// refused with document_scalar_unsupported rather than guessed at.

// codeDocumentScalarUnsupported leads the refusal of a placeholder whose
// context has no encoding.
const codeDocumentScalarUnsupported = "document_scalar_unsupported"

// documentPlaceholder finds every placeholder in a document.
var documentPlaceholder = regexp.MustCompile(regexp.QuoteMeta(model.PlanPlaceholderPrefix) +
	`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}-[a-z][a-z0-9_]{0,31}-[0-9a-f]{32}`)

// blockScalarIndicator matches a line that opens a YAML block scalar: a key
// or a sequence entry whose value is | or > with optional chomping and
// indentation indicators, and an optional comment.
var blockScalarIndicator = regexp.MustCompile(`(^|:\s+|^\s*-\s+)[|>][+-]?[0-9]?[+-]?\s*(#.*)?$`)

// substituteDocument writes each bound credential in its placeholder's place.
// Every placeholder in the text must have a substitution. Errors name the
// context and never quote a credential.
func substituteDocument(doc model.ConvertDocument) (string, error) {
	if err := doc.Validate(); err != nil {
		return "", err
	}
	content := doc.Content
	matches := documentPlaceholder.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return content, nil
	}
	var out strings.Builder
	out.Grow(len(content))
	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		placeholder := content[start:end]
		credential, ok := doc.Substitutions[placeholder]
		if !ok {
			return "", errors.New("the document carries a placeholder with no substitution")
		}
		replaceStart, replaceEnd, encoded, err := encodeForContext(content, start, end, credential)
		if err != nil {
			return "", err
		}
		if replaceStart < last {
			return "", fmt.Errorf("%s: two placeholders share one scalar", codeDocumentScalarUnsupported)
		}
		out.WriteString(content[last:replaceStart])
		out.WriteString(encoded)
		last = replaceEnd
	}
	out.WriteString(content[last:])
	return out.String(), nil
}

// encodeForContext reads the context of the placeholder at content[start:end]
// and returns the span to replace and its replacement.
func encodeForContext(content string, start, end int, credential string) (int, int, string, error) {
	lineStart := strings.LastIndexByte(content[:start], '\n') + 1
	lineEnd := len(content)
	if i := strings.IndexByte(content[end:], '\n'); i >= 0 {
		lineEnd = end + i
	}
	line := strings.TrimRight(content[lineStart:lineEnd], "\r")
	before := content[lineStart:start]
	after := ""
	if end-lineStart <= len(line) {
		after = line[end-lineStart:]
	}

	if inYAMLBlockScalar(content, lineStart) {
		return 0, 0, "", fmt.Errorf("%s: a placeholder in a block scalar", codeDocumentScalarUnsupported)
	}
	// A line that is the placeholder alone takes the bare value.
	if strings.TrimSpace(line) == content[start:end] {
		return start, end, credential, nil
	}
	// A URI: the placeholder sits in its userinfo or in a query value.
	if scheme := strings.Index(before, "://"); scheme >= 0 && !strings.ContainsAny(before[scheme:], " \t\"'") {
		rest := before[scheme+3:]
		inUserinfo := !strings.ContainsAny(rest, "@/?#")
		inQuery := strings.Contains(rest, "?") && !strings.Contains(rest, "#") && (strings.HasSuffix(rest, "=") || strings.HasSuffix(rest, "&"))
		if inUserinfo || inQuery {
			return start, end, percentEncodeStrict(credential), nil
		}
		return 0, 0, "", fmt.Errorf("%s: a placeholder in a URI outside its userinfo and query", codeDocumentScalarUnsupported)
	}
	quote, comment := scanQuoteState(before)
	switch {
	case comment:
		return 0, 0, "", fmt.Errorf("%s: a placeholder in a comment", codeDocumentScalarUnsupported)
	case quote == '"':
		return start, end, jsonStringBody(credential), nil
	case quote == '\'':
		return start, end, strings.ReplaceAll(credential, "'", "''"), nil
	}
	// A plain scalar: it must be the placeholder exactly, delimited by the
	// value position on the left and the end of the scalar on the right.
	if plainScalarAlone(before, after) {
		return start, end, `"` + jsonStringBody(credential) + `"`, nil
	}
	return 0, 0, "", fmt.Errorf("%s: a placeholder inside a longer plain scalar", codeDocumentScalarUnsupported)
}

// plainScalarAlone reports whether the text around a placeholder makes it a
// whole YAML plain scalar: a mapping value or a sequence entry (": " or "- "
// before it, with the space YAML requires) or a flow item ("[", "{" or ","
// before it), ended by the line, a comment or a flow delimiter.
func plainScalarAlone(before, after string) bool {
	left := strings.TrimRight(before, " \t")
	spaced := len(left) < len(before)
	leftOK := false
	switch {
	case strings.HasSuffix(left, ":"), strings.HasSuffix(left, "-"):
		leftOK = spaced
	case strings.HasSuffix(left, "["), strings.HasSuffix(left, "{"), strings.HasSuffix(left, ","):
		leftOK = true
	}
	right := strings.TrimLeft(after, " \t")
	switch {
	case right == "":
		return leftOK
	case strings.HasPrefix(right, "#"):
		// A # that touches the scalar is part of it, not a comment.
		return leftOK && len(right) < len(after)
	case strings.HasPrefix(right, ","), strings.HasPrefix(right, "}"), strings.HasPrefix(right, "]"):
		return leftOK
	}
	return false
}

// scanQuoteState reads a line up to a position and reports the quote it is
// inside (0 for none) and whether it is inside a comment. A YAML comment
// starts with # at the line start or after whitespace, outside quotes.
func scanQuoteState(before string) (quote byte, comment bool) {
	for i := 0; i < len(before); i++ {
		c := before[i]
		switch quote {
		case '"':
			if c == '\\' {
				i++
			} else if c == '"' {
				quote = 0
			}
		case '\'':
			if c == '\'' {
				if i+1 < len(before) && before[i+1] == '\'' {
					i++
				} else {
					quote = 0
				}
			}
		default:
			switch {
			case c == '"' || c == '\'':
				quote = c
			case c == '#' && (i == 0 || before[i-1] == ' ' || before[i-1] == '\t'):
				return 0, true
			}
		}
	}
	return quote, false
}

// inYAMLBlockScalar reports whether the line starting at lineStart is inside
// a YAML block scalar: some earlier line, less indented than every line
// between it and this one, opens a block scalar.
func inYAMLBlockScalar(content string, lineStart int) bool {
	indent := func(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }
	lines := strings.Split(content[:lineStart], "\n")
	current := content[lineStart:]
	if i := strings.IndexByte(current, '\n'); i >= 0 {
		current = current[:i]
	}
	limit := indent(current)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) >= limit {
			continue
		}
		if blockScalarIndicator.MatchString(strings.TrimSpace(line)) || blockScalarIndicator.MatchString(line) {
			return true
		}
		limit = indent(line)
		if limit == 0 {
			return false
		}
	}
	return false
}

// jsonStringBody is s escaped as the inside of a JSON string, which is also
// the inside of a YAML double-quoted scalar.
func jsonStringBody(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded[1 : len(encoded)-1])
}

// percentEncodeStrict percent-encodes every byte outside the URI unreserved
// set, which is safe in a userinfo and in a query value alike.
func percentEncodeStrict(s string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[c>>4])
		out.WriteByte(hex[c&0xf])
	}
	return out.String()
}
