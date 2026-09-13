package control

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ShadowElf37/VideoStream/proto"
)

// MediaExts are the file types the Queue tab is allowed to show.
var MediaExts = map[string]bool{
	".mkv": true, ".mp4": true, ".webm": true, ".avi": true, ".mov": true,
	".m4v": true, ".ts": true, ".mpg": true, ".wmv": true, ".flv": true,
	".srt": true, ".ass": true, ".sub": true, ".vtt": true,
}

// NormalizeRoots turns the --media-root flags into absolute, symlink-resolved
// directories. Roots that do not exist are dropped with an error.
func NormalizeRoots(roots []string) ([]string, error) {
	var out []string
	var bad []string
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			bad = append(bad, r)
			continue
		}
		res, err := filepath.EvalSymlinks(abs)
		if err != nil {
			bad = append(bad, r)
			continue
		}
		if fi, err := os.Stat(res); err != nil || !fi.IsDir() {
			bad = append(bad, r)
			continue
		}
		out = append(out, res)
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("unusable media roots: %s", strings.Join(bad, ", "))
	}
	return out, nil
}

// under reports whether path p (already absolute and resolved) is inside root.
func under(root, p string) bool {
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolve checks a local path against the media-root allowlist and returns its
// canonical form. Symlinks are resolved first so a link inside a root cannot
// point outside it.
func Resolve(roots []string, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// A path that does not exist can never be under a root we trust.
		return "", fmt.Errorf("no such file: %s", p)
	}
	for _, r := range roots {
		if under(r, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path is outside the allowed media roots: %s", p)
}

// IsURL reports whether a vs/load argument is a URL rather than a local path.
// Windows drive letters ("C:\...") are deliberately not treated as schemes.
func IsURL(s string) bool {
	i := strings.Index(s, "://")
	if i <= 1 {
		return false
	}
	for _, r := range s[:i] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// ListDir lists one directory for the host UI's file browser. An empty dir
// lists the roots themselves. Entries are sorted directories first, then by
// name; dotfiles are skipped and only media extensions are shown.
func ListDir(roots []string, dir string) (proto.FsList, error) {
	out := proto.FsList{Dir: dir, Roots: roots, Entries: []proto.FsEntry{}}
	if dir == "" {
		for _, r := range roots {
			fi, err := os.Stat(r)
			if err != nil {
				continue
			}
			out.Entries = append(out.Entries, proto.FsEntry{
				Name:  filepath.Base(r),
				Path:  r,
				Dir:   true,
				MTime: fi.ModTime().UnixMilli(),
			})
		}
		return out, nil
	}
	resolved, err := Resolve(roots, dir)
	if err != nil {
		return out, err
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.IsDir() {
		return out, fmt.Errorf("not a directory: %s", dir)
	}
	out.Dir = resolved
	ents, err := os.ReadDir(resolved)
	if err != nil {
		return out, err
	}
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(resolved, name)); err == nil {
				isDir = st.IsDir()
			}
		}
		if !isDir && !MediaExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		ent := proto.FsEntry{
			Name: name,
			Path: filepath.Join(resolved, name),
			Dir:  isDir,
		}
		if info, err := e.Info(); err == nil {
			ent.MTime = info.ModTime().UnixMilli()
			if !isDir {
				ent.Size = info.Size()
			}
		}
		out.Entries = append(out.Entries, ent)
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}
