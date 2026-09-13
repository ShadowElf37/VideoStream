package control

import "testing"

// isPauseCommand is the whole of what a viewer may do when the room has
// anyoneCanPause on, so it has to be exactly a pause toggle and nothing that
// merely starts like one.
func TestIsPauseCommand(t *testing.T) {
	cases := []struct {
		name string
		cmd  []any
		want bool
	}{
		{"cycle pause", []any{"cycle", "pause"}, true},
		{"set pause true", []any{"set", "pause", true}, true},
		{"set pause string", []any{"set", "pause", "yes"}, true},

		{"empty", nil, false},
		{"name only", []any{"cycle"}, false},
		{"seek", []any{"seek", "60"}, false},
		{"loadfile", []any{"loadfile", "/etc/passwd"}, false},
		{"quit", []any{"quit"}, false},
		{"cycle other property", []any{"cycle", "mute"}, false},
		{"set other property", []any{"set", "volume", 100}, false},
		{"virtual command", []any{"vs/quality", "1080p-high"}, false},
		// Trailing arguments would be a different command with a pause-shaped
		// prefix, so length is part of the match.
		{"cycle pause with extra arg", []any{"cycle", "pause", "up"}, false},
		{"set pause with extra arg", []any{"set", "pause", true, "extra"}, false},
		// mpv accepts these spellings too, but we deliberately do not: the
		// allowlist is the two forms the web client actually sends.
		{"no-osd set pause", []any{"no-osd", "set", "pause", true}, false},
		{"non-string name", []any{1, "pause"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPauseCommand(tc.cmd); got != tc.want {
				t.Errorf("isPauseCommand(%v) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestPauseAllowedFollowsSettings(t *testing.T) {
	c := New(Deps{AnyoneCanPause: false})
	if c.pauseAllowed() {
		t.Error("pauseAllowed() = true with the setting off")
	}
	c.setMu.Lock()
	c.anyoneCanPause = true
	c.setMu.Unlock()
	if !c.pauseAllowed() {
		t.Error("pauseAllowed() = false after the setting was turned on")
	}
}
