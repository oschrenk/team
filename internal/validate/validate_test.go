package validate

import (
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"a", true},
		{"ab", true},
		{"abc-def", true},
		{"a1-b2", true},
		{"0", true},
		{strings.Repeat("a", 40), true},

		{"", false},
		{"-abc", false},                  // leading hyphen
		{"abc_def", false},               // underscore
		{"ABC", false},                   // uppercase
		{"abc.def", false},               // dot
		{"abc def", false},               // space
		{strings.Repeat("a", 41), false}, // >40 chars
	}
	for _, tc := range cases {
		if got := Name(tc.in); got != tc.want {
			t.Errorf("Name(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAutoNameFromCwd(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{"/Users/oliver/Projects/tools/team", "team"},
		{"/path/to/My Repo", "my-repo"},
		{"/abc/Foo___Bar", "foo-bar"},
		{"/abc/--leading-dashes--", "leading-dashes"},
		{"/abc/123abc", "123abc"},
		{"/abc/" + strings.Repeat("a", 50), strings.Repeat("a", 40)},
		// Empty / invalid → "":
		{"/abc/!!!", ""},
		{"/", ""},
		// filepath.Base strips trailing slashes, so /abc/ → "abc".
		{"/abc/", "abc"},
	}
	for _, tc := range cases {
		if got := AutoNameFromCwd(tc.cwd); got != tc.want {
			t.Errorf("AutoNameFromCwd(%q) = %q, want %q", tc.cwd, got, tc.want)
		}
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"ascii", "hello", true},
		{"ascii with space", "hello world", true},
		{"cyrillic", "Привет", true},
		{"cjk", "日本語", true},
		{"emoji", "emoji 👋", true},
		{"exactly 60", strings.Repeat("a", 60), true},

		{"61 codepoints", strings.Repeat("a", 61), false},
		{"newline", "hello\nworld", false},                  // Cc
		{"nul", "hello\x00world", false},                    // Cc
		{"zero-width space", "hello​world", false},     // Cf
		{"rtl override", "hello‮world", false},         // Cf
		{"nbsp", "hello world", false},                 // Zs
		{"line separator", "hello world", false},       // Zl
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Label(tc.in); got != tc.want {
				t.Errorf("Label(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeLabel_NFC(t *testing.T) {
	// "é" decomposed (U+0065 U+0301) → composed (U+00E9)
	decomposed := "é"
	composed := "é"
	if got := NormalizeLabel(decomposed); got != composed {
		t.Fatalf("NFC normalize: got %q, want %q", got, composed)
	}
}

func TestSanitizeForStdout(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"newline", "hello\nworld", "hello↵world"},
		{"crlf", "hello\r\nworld", "hello↵↵world"},
		{"tab preserved", "tab\there", "tab\there"},
		{"ansi color", "\x1b[31mred\x1b[0m", "red"},
		{"ansi multi-param", "\x1b[1;38;5;200mfancy\x1b[0m", "fancy"},
		{"nul dropped", "null\x00byte", "nullbyte"},
		{"bidi dropped", "BiDi‮attack", "BiDiattack"},
		{"zwsp dropped", "join​ed", "joined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeForStdout(tc.in); got != tc.want {
				t.Errorf("SanitizeForStdout(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncateForStdout(t *testing.T) {
	t.Run("under cap", func(t *testing.T) {
		got, was, full := TruncateForStdout("hello", 10)
		if got != "hello" || was || full != 5 {
			t.Fatalf("got (%q, %v, %d), want (\"hello\", false, 5)", got, was, full)
		}
	})

	t.Run("at cap", func(t *testing.T) {
		got, was, full := TruncateForStdout("hello", 5)
		if got != "hello" || was || full != 5 {
			t.Fatalf("got (%q, %v, %d)", got, was, full)
		}
	})

	t.Run("over cap", func(t *testing.T) {
		got, was, full := TruncateForStdout("helloworld", 5)
		if got != "hello" || !was || full != 10 {
			t.Fatalf("got (%q, %v, %d)", got, was, full)
		}
	})

	t.Run("codepoints not bytes", func(t *testing.T) {
		// 6 codepoints: 3 CJK (3 bytes each) + 3 ASCII
		s := "日本語abc"
		got, was, full := TruncateForStdout(s, 3)
		if got != "日本語" || !was || full != 6 {
			t.Fatalf("got (%q, %v, %d)", got, was, full)
		}
	})
}

func TestNextAvailableName(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		taken map[string]bool
		want  string
	}{
		{
			name:  "base free",
			base:  "foo",
			taken: map[string]bool{},
			want:  "foo",
		},
		{
			name:  "base taken, -2 free",
			base:  "foo",
			taken: map[string]bool{"foo": true},
			want:  "foo-2",
		},
		{
			name:  "base and -2 taken, -3 free",
			base:  "foo",
			taken: map[string]bool{"foo": true, "foo-2": true},
			want:  "foo-3",
		},
		{
			name:  "gap: -2 free, -3 taken",
			base:  "foo",
			taken: map[string]bool{"foo": true, "foo-3": true},
			want:  "foo-2",
		},
		{
			name: "-2 through -9 taken, -10 free",
			base: "foo",
			taken: map[string]bool{
				"foo":   true,
				"foo-2": true,
				"foo-3": true,
				"foo-4": true,
				"foo-5": true,
				"foo-6": true,
				"foo-7": true,
				"foo-8": true,
				"foo-9": true,
			},
			want: "foo-10",
		},
		{
			name:  "39-char base, -2 would be 41 chars",
			base:  strings.Repeat("a", 39),
			taken: map[string]bool{strings.Repeat("a", 39): true},
			want:  "",
		},
		{
			name:  "38-char base, -2 fits at 40 chars",
			base:  strings.Repeat("a", 38),
			taken: map[string]bool{strings.Repeat("a", 38): true},
			want:  strings.Repeat("a", 38) + "-2",
		},
		{
			name:  "empty base",
			base:  "",
			taken: map[string]bool{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextAvailableName(tc.base, tc.taken); got != tc.want {
				t.Errorf("NextAvailableName(%q, %v) = %q, want %q", tc.base, tc.taken, got, tc.want)
			}
		})
	}
}
