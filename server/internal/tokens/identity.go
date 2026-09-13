package tokens

import (
	"crypto/rand"
	"hash/fnv"
	"strings"
)

// palette is 12 pleasant, evenly-spaced hues used to color participants.
var palette = [12]string{
	"#e57373", // red
	"#f0975a", // orange
	"#e0c25a", // amber
	"#c3d16b", // yellow-green
	"#81c784", // green
	"#4fc3a1", // teal-green
	"#4dd0e1", // cyan
	"#64b5f6", // blue
	"#7986cb", // indigo
	"#9575cd", // violet
	"#ba68c8", // purple
	"#f06292", // pink
}

// randomSuffixAlphabet excludes visually ambiguous characters.
const randomSuffixAlphabet = "abcdefghijkmnopqrstuvwxyz23456789"

// Slugify lowercases name, replaces runs of non-alphanumeric characters
// with a single hyphen, and trims leading/trailing hyphens. An empty result
// falls back to "user".
func Slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.TrimSuffix(b.String(), "-")
	if slug == "" {
		return "user"
	}
	return slug
}

// RandomSuffix returns n random characters drawn from an unambiguous
// lowercase alphanumeric alphabet.
func RandomSuffix(n int) string {
	out := make([]byte, n)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	for i, b := range buf {
		out[i] = randomSuffixAlphabet[int(b)%len(randomSuffixAlphabet)]
	}
	return string(out)
}

// NewIdentity builds a human participant identity from a display name:
// slug(name) + "-" + 4 random characters.
func NewIdentity(name string) string {
	return Slugify(name) + "-" + RandomSuffix(4)
}

// ColorFor deterministically maps an identity to one of the 12 palette
// colors by hashing it.
func ColorFor(identity string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(identity))
	return palette[h.Sum32()%uint32(len(palette))]
}
