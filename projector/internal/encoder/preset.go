// Package encoder turns rendered bgr0 frames into H.264 access units (via an
// ffmpeg child process) and 20 ms PCM chunks into Opus frames (in-process,
// libopus via cgo).
package encoder

import (
	"fmt"

	"github.com/ShadowElf37/VideoStream/proto"
)

// Preset is one quality step offered to the host UI.
type Preset struct {
	Name   string
	Width  int
	Height int
	Kbps   int
}

// Presets, from best to worst. Order matters: --max-preset caps the list.
var Presets = []Preset{
	{proto.Preset1080pHigh, 1920, 1080, 8000},
	{proto.Preset1080p, 1920, 1080, 5000},
	{proto.Preset720p, 1280, 720, 3000},
	{proto.Preset540p, 960, 540, 1500},
}

// LookupPreset finds a preset by name.
func LookupPreset(name string) (Preset, error) {
	for _, p := range Presets {
		if p.Name == name {
			return p, nil
		}
	}
	return Preset{}, fmt.Errorf("unknown preset %q", name)
}

// PresetRank is the index in Presets; lower is higher quality.
func PresetRank(name string) int {
	for i, p := range Presets {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// AllowedPresets returns the presets at or below the given cap.
func AllowedPresets(max string) []Preset {
	r := PresetRank(max)
	if r < 0 {
		return Presets
	}
	return Presets[r:]
}

// AudioBitrate is the Opus bitrate for the movie audio track.
const AudioBitrate = 128000
