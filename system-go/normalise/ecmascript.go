package normalise

// The ECMAScript value semantics the rules use, exported for the operators,
// which read the same model values with the same meaning (truthiness of an
// argument, String(value) of a field, trim of a renamed name).

// Truthy is ECMAScript truthiness over model values: null, false, 0,
// not-a-number and the empty text are false; objects and lists, empty ones
// included, are true.
func Truthy(v any) bool { return truthy(v) }

// Text is ECMAScript's String(value) over model values. A list is joined
// with commas, its null elements written as empty text, as
// Array.prototype.join writes them.
func Text(v any) string { return text(v) }

// TrimES removes what ECMAScript's String.prototype.trim removes: white
// space and line terminators, U+FEFF included.
func TrimES(s string) string { return trimES(s) }
