package message

import "testing"

// Internal tests live in package `message` so they can exercise the
// unexported bodyPreview helper directly. Without these the §11.5
// fan-out path's textual rendering had no unit coverage even though
// the §13.8 audit showed the function at 0%.

func TestBodyPreview_TruncatesLongBody(t *testing.T) {
	t.Parallel()
	long := make([]rune, 250)
	for i := range long {
		long[i] = 'a'
	}
	got := bodyPreview(string(long))
	wantPrefixLen := 100
	wantSuffix := "…"
	gotRunes := []rune(got)
	if len(gotRunes) != wantPrefixLen+1 {
		t.Fatalf("preview rune count = %d, want %d", len(gotRunes), wantPrefixLen+1)
	}
	if string(gotRunes[wantPrefixLen]) != wantSuffix {
		t.Errorf("preview missing ellipsis suffix: %q", got)
	}
}

func TestBodyPreview_ShortBodyPassesThrough(t *testing.T) {
	t.Parallel()
	in := "hi there"
	if got := bodyPreview(in); got != in {
		t.Errorf("preview = %q, want %q", got, in)
	}
}

func TestBodyPreview_WhitespaceFallsBackToGeneric(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   ", "\n\t"} {
		got := bodyPreview(in)
		if got != "You have a new message" {
			t.Errorf("preview(%q) = %q, want generic fallback", in, got)
		}
	}
}

// Multi-byte runes must count as one rune each so an emoji-heavy body
// doesn't get cut mid-codepoint.
func TestBodyPreview_CountsRunesNotBytes(t *testing.T) {
	t.Parallel()
	in := "🐶🐱🐭" // 3 runes, 12 bytes
	if got := bodyPreview(in); got != in {
		t.Errorf("preview = %q, want %q (no truncation expected for short body)", got, in)
	}
}
