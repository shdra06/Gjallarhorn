package policy

import (
	"errors"
	"strings"
)

// Glob patterns are deliberately simple so they are easy to read in a policy file:
//
//	*  matches any run of characters (including none, and including "/" and ":")
//	?  matches exactly one character
//
// Everything else matches itself. Matching is case-sensitive.

const maxPatternLen = 256

// ValidatePattern reports whether p is an acceptable glob.
func ValidatePattern(p string) error {
	if p == "" {
		return errors.New("empty pattern")
	}
	if len(p) > maxPatternLen {
		return errors.New("pattern too long")
	}
	if strings.ContainsAny(p, "\n\r\t") {
		return errors.New("pattern contains control characters")
	}
	return nil
}

// Glob reports whether s matches pattern p. It runs in O(len(p)*len(s)) time in the worst case
// and never recurses, so hostile patterns can't blow the stack.
func Glob(p, s string) bool {
	px, sx := 0, 0
	starP, starS := -1, -1
	for sx < len(s) {
		switch {
		case px < len(p) && (p[px] == '?' || p[px] == s[sx]) && p[px] != '*':
			px++
			sx++
		case px < len(p) && p[px] == '*':
			starP, starS = px, sx
			px++
		case starP >= 0:
			starS++
			px, sx = starP+1, starS
		default:
			return false
		}
	}
	for px < len(p) && p[px] == '*' {
		px++
	}
	return px == len(p)
}

// AnyGlob reports whether s matches any of the patterns. An empty list matches everything.
func AnyGlob(patterns []string, s string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if Glob(p, s) {
			return true
		}
	}
	return false
}
