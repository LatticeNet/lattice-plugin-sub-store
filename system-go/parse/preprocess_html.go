package parse

import "strings"

// preprocessHTML is row 1 of parser.md section 2: a document that starts
// with exactly "<!DOCTYPE html>" (case-sensitive, no leading white space) is
// an error page, and yields the empty text.
func preprocessHTML(text string) (string, bool, string, error) {
	if strings.HasPrefix(text, "<!DOCTYPE html>") {
		return "", true, "", nil
	}
	return "", false, "", nil
}
