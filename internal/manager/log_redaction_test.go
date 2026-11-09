package manager

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestLogBufferChunkedWritesPreserveLineBoundaries(t *testing.T) {
	buf := newLogBuffer([]string{"split-secret"})
	_, _ = buf.Write([]byte("first\nsecond split-"))
	_, _ = buf.Write([]byte("secret\n"))
	_, _ = buf.Write([]byte(strings.Repeat("x", 10000)))
	_, _ = buf.Write([]byte(strings.Repeat("y", 6385)))
	_, _ = buf.Write([]byte("\nlast\n"))
	got := buf.read()
	want := []string{"first", "second [REDACTED]", "[oversized log line omitted]", "last"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("chunked log lines = %#v, want %#v", got, want)
	}
}

func TestLogRedactorMasksQuotedCredentials(t *testing.T) {
	secret := "a-credential:\"\\\t<>&\u2028\u2029\x7f"
	jsonText, _ := json.Marshal(secret)
	for _, form := range []string{secret, quote(secret), string(jsonText), strconv.Quote(secret), templateCredential(secret)} {
		forms := []string{form}
		if strings.HasPrefix(form, `"`) {
			forms = append(forms, form[1:len(form)-1])
		}
		for _, value := range forms {
			buf := newLogBuffer([]string{secret})
			_, _ = buf.Write([]byte("value: " + value + "\n"))
			if got := strings.Join(buf.read(), ""); got != "value: [REDACTED]" {
				t.Errorf("credential representation was not fully masked: %q", got)
			}
		}
	}
}

func TestLogRedactorMasksLongerOverlapFirst(t *testing.T) {
	buf := newLogBuffer([]string{"secret", "secret-with-sensitive-tail", "secret", ""})
	_, _ = buf.Write([]byte("secret-with-sensitive-tail secret \"secret-with-sensitive-tail\"\n"))
	if got := strings.Join(buf.read(), ""); got != "[REDACTED] [REDACTED] [REDACTED]" {
		t.Fatal("overlapping/duplicate/empty secret handling:", got)
	}
}

func TestLogRedactorStripsANSIBeforeMasking(t *testing.T) {
	buf := newLogBuffer([]string{"top-secret-value"})
	_, _ = buf.Write([]byte("top-\x1b[31msecret\x1b[0m-value\n"))
	if got := strings.Join(buf.read(), ""); got != "[REDACTED]" {
		t.Fatal("ANSI formatting bypassed masking:", got)
	}
}

func TestLogRedactorMasksMultilineCredentialFragments(t *testing.T) {
	buf := newLogBuffer([]string{"alpha\r\nbeta"})
	_, _ = buf.Write([]byte("alpha\r\nbeta\n"))
	if got := strings.Join(buf.read(), "|"); got != "[REDACTED]|[REDACTED]" {
		t.Fatal("raw multiline credential fragments exposed:", got)
	}
}

// An earlier credential may overlap the start of another credential rather
// than merely being its prefix. Neither tail may remain visible.
func TestLogRedactorMasksIntersectingCredentials(t *testing.T) {
	for _, tc := range []struct {
		secrets    []string
		line, want string
	}{
		{[]string{"abc", "bcdef"}, "x abcdef y", "x [REDACTED] y"},
		{[]string{"aba"}, "ababa", "[REDACTED]"},
		{[]string{"abc", "xyz"}, "abc / xyz", "[REDACTED] / [REDACTED]"},
		{[]string{"aaaaaaaa", "aaaaaaaaa", "aaaaaaaaaa"}, "prefix " + strings.Repeat("a", 16360) + " suffix", "[REDACTED]"},
	} {
		buf := newLogBuffer(tc.secrets)
		_, _ = buf.Write([]byte(tc.line + "\n"))
		if got := strings.Join(buf.read(), ""); got != tc.want {
			t.Errorf("redacted %q = %q, want %q", tc.line, got, tc.want)
		}
	}
}
