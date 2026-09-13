package main

import (
	"strings"
	"unicode/utf8"
)

// Repairing mojibake in metadata.
//
// Plenty of releases carry chapter titles written by something that read UTF-8
// bytes as cp1252 and re-encoded them, so a title that should read
// "黒髪の少女" arrives as "é»’é«ªã®å°‘å¥³". The bytes in the file really are
// wrong — ffprobe reports them faithfully and every player shows the same
// soup — so the repair has to happen somewhere, and doing it once at push time
// beats doing it in every client on every render.
//
// The transformation is exactly invertible: map each rune back to the cp1252
// byte that would have produced it, then read those bytes as UTF-8. It is
// applied only when that round trip succeeds and yields valid UTF-8 with no
// remaining suspicious runes, so ordinary text — including ordinary accented
// text, which does not survive the inverse — is left alone.

// cp1252High maps the 0x80–0x9F range, where cp1252 differs from Latin-1, back
// to its byte. The five slots cp1252 leaves undefined (0x81, 0x8D, 0x8F, 0x90,
// 0x9D) pass straight through, which is what the encoders causing this do.
var cp1252High = map[rune]byte{
	'€': 0x80, 0x81: 0x81, '‚': 0x82, 'ƒ': 0x83,
	'„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87,
	'ˆ': 0x88, '‰': 0x89, 'Š': 0x8A, '‹': 0x8B,
	'Œ': 0x8C, 0x8d: 0x8D, 'Ž': 0x8E, 0x8f: 0x8F,
	0x90: 0x90, '‘': 0x91, '’': 0x92, '“': 0x93,
	'”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97,
	'˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B,
	'œ': 0x9C, 0x9d: 0x9D, 'ž': 0x9E, 'Ÿ': 0x9F,
}

// repairMojibake returns s decoded back through cp1252, or s unchanged when
// that is not what happened to it.
func repairMojibake(s string) string {
	if s == "" || !strings.ContainsFunc(s, suspicious) {
		return s
	}
	buf := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r <= 0xFF:
			buf = append(buf, byte(r))
		default:
			b, ok := cp1252High[r]
			if !ok {
				// Not something cp1252 could have produced, so this string was
				// never mangled that way. Leave it alone.
				return s
			}
			buf = append(buf, b)
		}
	}
	out := string(buf)
	if out == s || !utf8.ValidString(out) {
		return s
	}
	// A repair must remove the suspicious runes, not shuffle them around.
	if strings.ContainsFunc(out, suspicious) {
		return s
	}
	return out
}

// suspicious reports runes that mojibake produces in quantity and that real
// titles almost never contain: the C1 control range and the handful of
// cp1252 punctuation characters that UTF-8 lead bytes decode into.
func suspicious(r rune) bool {
	if r >= 0x80 && r <= 0x9F {
		return true
	}
	switch r {
	case 'Â', 'Ã', '€', 'œ', 'Œ', 'Ž', 'ž', 'š', 'Š':
		return true
	}
	return false
}
