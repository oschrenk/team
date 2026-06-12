package validate

import (
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const labelMaxCP = 60

// NormalizeLabel applies Unicode NFC normalization.
func NormalizeLabel(s string) string {
	return norm.NFC.String(s)
}

// Label reports whether s is a valid display label.
//
// Rules (mirrors shared.py::validate_label):
//   - Empty allowed.
//   - NFC-normalized length ≤ 60 codepoints.
//   - Every codepoint is neither category-C (control/format/surrogate/
//     private-use/unassigned) nor category-Z (separator) — except literal
//     space, which is allowed.
//
// The intent is to block BiDi-override / ZWJ / NBSP-style display
// injection while still permitting reasonable display labels.
func Label(s string) bool {
	if s == "" {
		return true
	}
	nfc := norm.NFC.String(s)
	count := 0
	for _, r := range nfc {
		count++
		if count > labelMaxCP {
			return false
		}
		if r == ' ' {
			continue
		}
		if unicode.Is(unicode.C, r) || unicode.Is(unicode.Z, r) {
			return false
		}
	}
	return true
}
