// Package media is the server's view of the pushed library: what titles exist
// on disk, and the signed URLs that let a browser fetch their bytes.
//
// A title is a directory, not a file — the movie plus its metadata, and later
// a poster, subtitle sidecars or a second rendition. Adding any of those needs
// no change here, to the API, or to the UI.
package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ShadowElf37/VideoStream/proto"
)

// ErrNotFound is returned for a title id that is not in the library.
var ErrNotFound = errors.New("media not found")

// Library is a directory of title directories.
type Library struct{ root string }

// New opens the library at root. A missing root is not an error — a
// deployment with nothing pushed yet is a normal state, and it starts working
// the moment a title lands.
func New(root string) *Library { return &Library{root: root} }

// Root is where titles live.
func (l *Library) Root() string { return l.root }

// ValidID reports whether id is a plausible title id.
//
// Every id from a client is checked with this before it touches a path. Ids
// come from vspush's `sanitize`, so they are already narrow; this is the
// boundary that makes "../../etc/passwd" a 404 rather than a file read.
func ValidID(id string) bool {
	if id == "" || len(id) > 200 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	// A leading dot would hide the title and reach dotfiles by name.
	return !strings.HasPrefix(id, ".")
}

// ValidFile reports whether name is a file a client may fetch from a title.
// An allowlist rather than a character check: everything servable is a known
// name, so there is no reason to accept anything else.
func ValidFile(name string) bool {
	switch name {
	case proto.MovieFileName, proto.MetaFileName:
		return true
	}
	return false
}

// Path resolves a file inside a title, refusing anything that escapes it.
func (l *Library) Path(id, file string) (string, error) {
	if !ValidID(id) || !ValidFile(file) {
		return "", ErrNotFound
	}
	return filepath.Join(l.root, id, file), nil
}

// Get reads one title's metadata.
func (l *Library) Get(id string) (proto.MediaMeta, error) {
	if !ValidID(id) {
		return proto.MediaMeta{}, ErrNotFound
	}
	return l.readMeta(id)
}

// List returns every readable title, newest push first.
//
// A directory without a readable meta.json is skipped rather than failing the
// listing: a half-finished scp should not take the whole library down, and
// vspush writes the movie before the metadata.
func (l *Library) List() ([]proto.MediaMeta, error) {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		if os.IsNotExist(err) {
			return []proto.MediaMeta{}, nil
		}
		return nil, err
	}
	out := make([]proto.MediaMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		m, err := l.readMeta(e.Name())
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PushedAt != out[j].PushedAt {
			return out[i].PushedAt > out[j].PushedAt
		}
		return out[i].Title < out[j].Title
	})
	return out, nil
}

func (l *Library) readMeta(id string) (proto.MediaMeta, error) {
	var m proto.MediaMeta
	blob, err := os.ReadFile(filepath.Join(l.root, id, proto.MetaFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return m, ErrNotFound
		}
		return m, err
	}
	if err := json.Unmarshal(blob, &m); err != nil {
		return m, fmt.Errorf("%s/%s: %w", id, proto.MetaFileName, err)
	}
	// The directory name is the id that addresses it; trust it over whatever
	// the file claims, so a renamed directory still works.
	m.ID = id
	return m, nil
}

// Delete removes a title and everything in it.
func (l *Library) Delete(id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	dir := filepath.Join(l.root, id)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return os.RemoveAll(dir)
}

// FreeBytes reports the free space on the library's filesystem, so the UI can
// say how much room is left before a push fails halfway.
func FreeBytes(path string) (uint64, error) { return freeBytes(path) }
