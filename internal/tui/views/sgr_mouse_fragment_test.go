package views

import "testing"

func TestAllSGRBodyRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		runes []rune
		want  bool
	}{
		{name: "bracket", runes: []rune("["), want: true},
		{name: "angle", runes: []rune("<"), want: true},
		{name: "digits", runes: []rune("65"), want: true},
		{name: "semicolons", runes: []rune(";"), want: true},
		{name: "M upper", runes: []rune("M"), want: true},
		{name: "m lower", runes: []rune("m"), want: true},
		{name: "full fragment body", runes: []rune("[<65;97;554M"), want: true},
		{name: "concatenated", runes: []rune("[<65;97;554M[<64;97;554M"), want: true},
		{name: "empty", runes: nil, want: true}, // vacuously true
		{name: "contains letter", runes: []rune("a"), want: false},
		{name: "contains space", runes: []rune("1 2"), want: false},
		{name: "mixed with text", runes: []rune("[<65;97;554M hello"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := allSGRBodyRunes(tc.runes); got != tc.want {
				t.Errorf("allSGRBodyRunes(%q) = %v, want %v", string(tc.runes), got, tc.want)
			}
		})
	}
}

func TestIsLikelySGRMouseFragment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		runes []rune
		want  bool
	}{
		// Positive cases.
		{name: "single complete fragment", runes: []rune("[<65;97;554M"), want: true},
		{name: "scroll up fragment", runes: []rune("[<64;97;554M"), want: true},
		{name: "lowercase terminator", runes: []rune("[<65;97;554m"), want: true},
		{name: "concatenated fragments", runes: []rune("[<65;97;554M[<64;97;554M[<65;108;260M"), want: true},
		{name: "partial tail no bracket", runes: []rune("<65;97;554M"), want: true},
		{name: "tail digits+M", runes: []rune("554M"), want: true},
		{name: "partial no terminator", runes: []rune("65;97;554"), want: true},
		{name: "digits and semicolons", runes: []rune("12;34"), want: true},

		// Negative cases — must NOT be flagged (len < 2).
		{name: "single rune bracket", runes: []rune("["), want: false},
		{name: "single rune M", runes: []rune("M"), want: false},
		{name: "single digit", runes: []rune("5"), want: false},
		{name: "empty", runes: nil, want: false},

		// Negative cases — non-SGR content.
		{name: "contains space", runes: []rune("fix the bug"), want: false},
		{name: "contains letters", runes: []rune("abc"), want: false},
		{name: "mixed content", runes: []rune("[<65;97;554M hello"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isLikelySGRMouseFragment(tc.runes); got != tc.want {
				t.Errorf("isLikelySGRMouseFragment(%q) = %v, want %v", string(tc.runes), got, tc.want)
			}
		})
	}
}

func TestExtractSGRScrollLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		runes []rune
		want  int // negative = up, positive = down
	}{
		{name: "scroll down", runes: []rune("[<65;97;554M"), want: 3},
		{name: "scroll up", runes: []rune("[<64;97;554M"), want: -3},
		{name: "three down", runes: []rune("[<65;97;554M[<65;97;554M[<65;97;554M"), want: 9},
		{name: "mixed directions", runes: []rune("[<65;97;554M[<64;97;554M"), want: 0},
		{name: "no complete fragment", runes: []rune("554M"), want: 0},
		{name: "partial without bracket", runes: []rune("<65;97;554M"), want: 0},
		{name: "non-wheel button 0", runes: []rune("[<0;97;554M"), want: 0},
		{name: "lowercase m terminator", runes: []rune("[<65;97;554m"), want: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractSGRScrollLines(tc.runes); got != tc.want {
				t.Errorf("extractSGRScrollLines(%q) = %d, want %d", string(tc.runes), got, tc.want)
			}
		})
	}
}
