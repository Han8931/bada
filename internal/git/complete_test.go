package git

import (
	"os"
	"path/filepath"
	"testing"
)

// mkdirs creates the given directories under a fresh temp root and returns it.
func mkdirs(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", n, err)
		}
	}
	return root
}

func matchNames(matches []DirMatch) []string {
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.Name)
	}
	return names
}

func TestCompleteDirCompletesUniqueMatchWithSeparator(t *testing.T) {
	root := mkdirs(t, "bada", "notes")

	got, matches := CompleteDir(filepath.Join(root, "ba"))
	if want := filepath.Join(root, "bada") + "/"; got != want {
		t.Fatalf("completion = %q, want %q", got, want)
	}
	if len(matches) != 1 || matches[0].Name != "bada" {
		t.Fatalf("matches = %v, want [bada]", matchNames(matches))
	}
}

func TestCompleteDirExtendsToCommonPrefixAndListsAll(t *testing.T) {
	root := mkdirs(t, "project-one", "project-two", "notes")

	got, matches := CompleteDir(filepath.Join(root, "pro"))
	if want := filepath.Join(root, "project-"); got != want {
		t.Fatalf("completion = %q, want %q", got, want)
	}
	if names := matchNames(matches); len(names) != 2 || names[0] != "project-one" || names[1] != "project-two" {
		t.Fatalf("matches = %v, want [project-one project-two]", names)
	}
}

func TestCompleteDirListsEveryChildOfAnOpenDirectory(t *testing.T) {
	root := mkdirs(t, "alpha", "beta")

	_, matches := CompleteDir(root + "/")
	if names := matchNames(matches); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("matches = %v, want [alpha beta]", names)
	}
}

func TestCompleteDirMarksGitRepositories(t *testing.T) {
	root := mkdirs(t, "plain", "cloned/.git")

	_, matches := CompleteDir(root + "/")
	byName := map[string]bool{}
	for _, m := range matches {
		byName[m.Name] = m.IsRepo
	}
	if !byName["cloned"] {
		t.Error("cloned should be reported as a repo")
	}
	if byName["plain"] {
		t.Error("plain should not be reported as a repo")
	}
}

func TestCompleteDirIgnoresFilesAndHiddenDirs(t *testing.T) {
	root := mkdirs(t, ".hidden", "visible")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, matches := CompleteDir(root + "/")
	if names := matchNames(matches); len(names) != 1 || names[0] != "visible" {
		t.Fatalf("matches = %v, want [visible]", names)
	}
	// A leading dot opts back in. Built by hand: filepath.Join would clean the
	// trailing "." away.
	_, matches = CompleteDir(root + "/.")
	if names := matchNames(matches); len(names) != 1 || names[0] != ".hidden" {
		t.Fatalf("hidden matches = %v, want [.hidden]", names)
	}
}

func TestCompleteDirIsCaseInsensitiveAndFixesCase(t *testing.T) {
	root := mkdirs(t, "Project")

	got, _ := CompleteDir(filepath.Join(root, "pro"))
	if want := filepath.Join(root, "Project") + "/"; got != want {
		t.Fatalf("completion = %q, want %q", got, want)
	}
}

func TestCompleteDirNeverDeletesTypedCharacters(t *testing.T) {
	// "Ab" and "aC" both match "a" case-insensitively but share no prefix, so
	// the typed text must survive untouched.
	root := mkdirs(t, "Ab", "aC")

	input := filepath.Join(root, "a")
	got, matches := CompleteDir(input)
	if got != input {
		t.Fatalf("completion = %q, want it unchanged (%q)", got, input)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %v, want both", matchNames(matches))
	}
}

func TestCompleteDirOpensBareTilde(t *testing.T) {
	got, matches := CompleteDir("~")
	if got != "~/" {
		t.Fatalf("completion = %q, want %q", got, "~/")
	}
	if matches != nil {
		t.Fatalf("matches = %v, want none", matchNames(matches))
	}
}

func TestCompleteDirKeepsTildePrefixUnexpanded(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	dir := filepath.Join(home, ".bada-complete-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skip("cannot write to home directory")
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	got, _ := CompleteDir("~/.bada-complete-te")
	if want := "~/.bada-complete-test/"; got != want {
		t.Fatalf("completion = %q, want %q", got, want)
	}
}

func TestCompleteDirOnUnreadablePathIsUnchanged(t *testing.T) {
	input := filepath.Join(t.TempDir(), "nope", "deeper")

	got, matches := CompleteDir(input)
	if got != input {
		t.Fatalf("completion = %q, want it unchanged (%q)", got, input)
	}
	if matches != nil {
		t.Fatalf("matches = %v, want none", matchNames(matches))
	}
}
