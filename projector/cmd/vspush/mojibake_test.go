package main

import "testing"

// mangled is a real chapter title from a Madoka release. Its bytes in the MKV
// really are UTF-8 that was read as cp1252 and re-encoded; the correct bytes
// for 黒髪の少女 do not appear in the file at all.  and  are the
// cp1252 slots that have no character and pass through untranslated.
const mangled = "Black Haired Girlã€€ã€Œé»’é«ªã®å°‘å¥³ã€"

func TestRepairMojibake(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"japanese chapter title", mangled, "Black Haired Girl　「黒髪の少女」"},

		// Everything below must come back untouched.
		{"plain ascii", "Opening", "Opening"},
		{"already correct japanese", "黒髪の少女", "黒髪の少女"},
		{"ordinary accents", "Café Society", "Café Society"},
		{"french", "Les Enfants du Paradis", "Les Enfants du Paradis"},
		{"empty", "", ""},
		{"typographic quotes in a real title", "Don’t Look Now", "Don’t Look Now"},
		{"em dash", "Nostalghia — 1983", "Nostalghia — 1983"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repairMojibake(tc.in); got != tc.want {
				t.Errorf("repairMojibake(%q)\n = %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// Repairing twice must not mangle an already-repaired string.
func TestRepairMojibakeIsIdempotent(t *testing.T) {
	once := repairMojibake(mangled)
	if twice := repairMojibake(once); twice != once {
		t.Errorf("second pass changed it: %q -> %q", once, twice)
	}
}
