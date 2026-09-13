package mediafs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkTree(t *testing.T) (root string, other string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "media")
	other = filepath.Join(base, "secrets")
	for _, d := range []string{root, other, filepath.Join(root, "Season 1"), filepath.Join(root, ".hidden")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "b-movie.mkv"):          "x",
		filepath.Join(root, "A-movie.mp4"):          "x",
		filepath.Join(root, "notes.txt"):            "x",
		filepath.Join(root, ".secret.mkv"):          "x",
		filepath.Join(root, "subs.srt"):             "x",
		filepath.Join(root, "Season 1", "ep01.mkv"): "x",
		filepath.Join(other, "private.mkv"):         "x",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Resolve symlinks the way NormalizeRoots does (macOS /var -> /private/var).
	rs, err := NormalizeRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	os2, err := NormalizeRoots([]string{other})
	if err != nil {
		t.Fatal(err)
	}
	return rs[0], os2[0]
}

func TestResolveAllowlist(t *testing.T) {
	root, other := mkTree(t)
	roots := []string{root}

	if _, err := Resolve(roots, filepath.Join(root, "b-movie.mkv")); err != nil {
		t.Fatalf("file inside the root should resolve: %v", err)
	}
	if _, err := Resolve(roots, filepath.Join(other, "private.mkv")); err == nil {
		t.Fatal("file outside the roots must be rejected")
	}
	if _, err := Resolve(roots, filepath.Join(root, "..", "secrets", "private.mkv")); err == nil {
		t.Fatal("traversal out of the root must be rejected")
	}
	if _, err := Resolve(roots, filepath.Join(root, "nope.mkv")); err == nil {
		t.Fatal("missing file must be rejected")
	}
	if _, err := Resolve(roots, ""); err == nil {
		t.Fatal("empty path must be rejected")
	}

	// A symlink inside the root pointing outside it must not launder the path.
	link := filepath.Join(root, "escape.mkv")
	if err := os.Symlink(filepath.Join(other, "private.mkv"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := Resolve(roots, link); err == nil {
		t.Fatalf("symlink escape must be rejected, resolved to %s", got)
	}
}

func TestListDirRootsAndOrdering(t *testing.T) {
	root, _ := mkTree(t)
	roots := []string{root}

	// Empty dir lists the roots themselves.
	list, err := ListDir(roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || !list.Entries[0].Dir || list.Entries[0].Path != root {
		t.Fatalf("root listing = %+v", list.Entries)
	}

	list, err = ListDir(roots, root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Name)
	}
	want := []string{"Season 1", "A-movie.mp4", "b-movie.mkv", "subs.srt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for _, e := range list.Entries {
		if e.Name == "b-movie.mkv" && e.Size != 1 {
			t.Fatalf("size not reported: %+v", e)
		}
	}
}

func TestListDirRejectsOutsideRoot(t *testing.T) {
	root, other := mkTree(t)
	if _, err := ListDir([]string{root}, other); err == nil {
		t.Fatal("listing outside the roots must be rejected")
	}
	if _, err := ListDir([]string{root}, filepath.Join(root, "b-movie.mkv")); err == nil {
		t.Fatal("listing a file must be rejected")
	}
}

func TestIsURL(t *testing.T) {
	for _, s := range []string{"https://youtu.be/x", "http://a/b", "rtmp://h/s", "file:///tmp/x"} {
		if !IsURL(s) {
			t.Fatalf("%q should be a URL", s)
		}
	}
	for _, s := range []string{"/home/me/a.mkv", "C:\\movies\\a.mkv", "a.mkv", "", "x:/y"} {
		if IsURL(s) {
			t.Fatalf("%q should not be a URL", s)
		}
	}
}
