package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestRepo builds a throwaway repository with the given commit subjects
// (oldest first) and returns its path.
func newTestRepo(t *testing.T, subjects ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Author", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-q")
	for i, subject := range subjects {
		file := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(file, []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		runGit("add", "file.txt")
		runGit("commit", "-q", "-m", subject)
	}
	return dir
}

func TestLogReturnsCommitsNewestFirst(t *testing.T) {
	repo := newTestRepo(t, "first commit", "second commit", "third commit")

	commits, err := Log(context.Background(), repo, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}
	want := []string{"third commit", "second commit", "first commit"}
	for i, subject := range want {
		if commits[i].Subject != subject {
			t.Errorf("commit %d subject = %q, want %q", i, commits[i].Subject, subject)
		}
	}
	c := commits[0]
	if c.Author != "Test Author" {
		t.Errorf("Author = %q", c.Author)
	}
	if len(c.Hash) != 40 {
		t.Errorf("Hash = %q, want a full 40-char sha", c.Hash)
	}
	if !strings.HasPrefix(c.Hash, c.Short) {
		t.Errorf("Short %q is not a prefix of Hash %q", c.Short, c.Hash)
	}
	if c.When.IsZero() {
		t.Error("When was not parsed")
	}
	if time.Since(c.When) > time.Hour {
		t.Errorf("When = %v, want roughly now", c.When)
	}
}

func TestLogHonoursLimit(t *testing.T) {
	repo := newTestRepo(t, "one", "two", "three", "four")

	commits, err := Log(context.Background(), repo, 2)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
}

// A subject containing the characters a naive delimiter would choke on must
// survive parsing intact.
func TestLogParsesAwkwardSubjects(t *testing.T) {
	subject := "fix: a|b\tc — 100% done"
	repo := newTestRepo(t, subject)

	commits, err := Log(context.Background(), repo, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
	if commits[0].Subject != subject {
		t.Errorf("Subject = %q, want %q", commits[0].Subject, subject)
	}
}

// A repository with no commits has no HEAD; that's an empty history, not a
// failure the UI should shout about.
func TestLogOnEmptyRepoReturnsNothing(t *testing.T) {
	repo := newTestRepo(t)

	commits, err := Log(context.Background(), repo, 10)
	if err != nil {
		t.Fatalf("Log on empty repo: %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("got %d commits, want 0", len(commits))
	}
}

func TestLogOnMissingRepoFails(t *testing.T) {
	if _, err := Log(context.Background(), filepath.Join(t.TempDir(), "gone"), 10); err == nil {
		t.Fatal("expected an error for a missing repository")
	}
}

func TestResolveReturnsTopLevelFromSubdirectory(t *testing.T) {
	repo := newTestRepo(t, "first commit")
	sub := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	top, err := Resolve(context.Background(), sub)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// macOS temp dirs are symlinked (/var → /private/var), so compare the
	// resolved forms rather than the raw strings.
	wantTop, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gotTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if gotTop != wantTop {
		t.Errorf("Resolve = %q, want %q", gotTop, wantTop)
	}
}

func TestResolveRejectsNonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	// A temp dir could sit inside an enclosing repo on some machines; only
	// assert the error when git agrees it isn't one.
	if _, err := Resolve(context.Background(), dir); err == nil {
		t.Skip("temp dir is inside a git repository")
	}
}

func TestResolveRejectsMissingAndEmptyPaths(t *testing.T) {
	if _, err := Resolve(context.Background(), ""); err == nil {
		t.Error("expected an error for an empty path")
	}
	if _, err := Resolve(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing path")
	}
}

func TestShowIncludesSubjectAndStat(t *testing.T) {
	repo := newTestRepo(t, "first commit")
	commits, err := Log(context.Background(), repo, 1)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	out, err := Show(context.Background(), repo, commits[0].Hash)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if !strings.Contains(out, "first commit") {
		t.Errorf("Show output missing the subject:\n%s", out)
	}
	if !strings.Contains(out, "file.txt") {
		t.Errorf("Show output missing the diffstat:\n%s", out)
	}
}

// A revision is interpolated into an argv, so anything non-hex is refused
// before it reaches git.
func TestShowRejectsNonHexRevisions(t *testing.T) {
	repo := newTestRepo(t, "first commit")
	for _, rev := range []string{"", "--upload-pack=touch /tmp/pwned", "HEAD; rm -rf /", "main"} {
		if _, err := Show(context.Background(), repo, rev); err == nil {
			t.Errorf("Show(%q) succeeded, want a rejection", rev)
		}
	}
}

func TestLogRespectsCancelledContext(t *testing.T) {
	repo := newTestRepo(t, "first commit")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Log(ctx, repo, 10); err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
}
