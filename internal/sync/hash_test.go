package sync

import "testing"

func TestNormalizeBodyConvertsCRLF(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain LF unchanged", "a\nb\nc", "a\nb\nc"},
		{"CRLF collapses", "a\r\nb\r\nc", "a\nb\nc"},
		{"bare CR collapses", "a\rb\rc", "a\nb\nc"},
		{"mixed CRLF and CR", "a\r\nb\rc\r\nd", "a\nb\nc\nd"},
		{"empty stays empty", "", ""},
		{"trailing CRLF", "a\r\n", "a\n"},
		{"leading CRLF", "\r\na", "\na"},
		{"hash invariant under CRLF", "x\r\ny", "x\ny"},
	}
	for _, tc := range cases {
		got := string(NormalizeBody([]byte(tc.in)))
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestContentHashStableAcrossLineEndings(t *testing.T) {
	lf := NormalizeBody([]byte("hello\nworld\n"))
	crlf := NormalizeBody([]byte("hello\r\nworld\r\n"))
	cr := NormalizeBody([]byte("hello\rworld\r"))
	if ContentHash(lf) != ContentHash(crlf) || ContentHash(lf) != ContentHash(cr) {
		t.Errorf("hash differs across line endings: lf=%s crlf=%s cr=%s",
			ContentHash(lf), ContentHash(crlf), ContentHash(cr))
	}
}
