// Package git shells out to the git binary to read a project's history.
//
// It is deliberately read-only: bada shows commits, it never mutates a
// repository. Every call takes a context so the UI can bound how long a slow or
// enormous repo is allowed to block.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Commit is one entry in a repository's log.
type Commit struct {
	Hash    string
	Short   string
	Author  string
	When    time.Time // authored instant, rendered in the viewer's local zone
	Subject string
}

// ErrNoGit reports that the git binary isn't on PATH.
var ErrNoGit = errors.New("git is not installed or not on PATH")

// field and record separators. Commit subjects can contain anything printable,
// including tabs and pipes, so the log format is delimited by ASCII unit (0x1f)
// and record (0x1e) separators instead — neither can appear in git's output.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
	logFormat = "%H" + fieldSep + "%h" + fieldSep + "%an" + fieldSep + "%aI" + fieldSep + "%s" + recordSep
)

// run executes git in repoDir and returns its stdout.
func run(ctx context.Context, repoDir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", ErrNoGit
	}
	full := append([]string{"--no-pager", "-C", repoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// Keep git non-interactive: without this a repo needing credentials would
	// hang the UI waiting on a prompt that nobody can see.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Wrap rather than replace, so callers can tell "the context ended" from
		// "git said no" — they mean very different things.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("git did not finish in %s: %w", repoDir, ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(firstLine(msg))
		}
		return "", err
	}
	return string(out), nil
}

// Resolve expands and validates a user-supplied repository path, returning the
// repository's top-level directory. Storing the top level (rather than whatever
// subdirectory was typed) keeps later log calls stable.
func Resolve(ctx context.Context, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is empty")
	}
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no such directory: %s", abs)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	out, err := run(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		if errors.Is(err, ErrNoGit) {
			return "", err
		}
		return "", fmt.Errorf("not a git repository: %s", abs)
	}
	top := strings.TrimSpace(out)
	if top == "" {
		return "", fmt.Errorf("not a git repository: %s", abs)
	}
	return top, nil
}

// Log returns up to limit commits from the repository's current HEAD, newest
// first. An empty repository (no commits yet) yields an empty slice, not an
// error.
func Log(ctx context.Context, repoDir string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 100
	}
	if strings.TrimSpace(repoDir) == "" {
		return nil, errors.New("no repository configured")
	}
	if _, err := os.Stat(repoDir); err != nil {
		return nil, fmt.Errorf("repository unavailable: %s", repoDir)
	}
	// A repo with no commits has no HEAD to walk, and `git log` fails there
	// rather than returning nothing. Treat that as an empty history — but only
	// when git itself refused; a missing binary or an expired context must not
	// be reported as "no commits yet".
	if _, err := run(ctx, repoDir, "rev-parse", "--verify", "HEAD"); err != nil {
		if errors.Is(err, ErrNoGit) || ctx.Err() != nil {
			return nil, err
		}
		return nil, nil
	}
	out, err := run(ctx, repoDir, "log", fmt.Sprintf("-n%d", limit), "--pretty=format:"+logFormat)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

// Show returns `git show --stat` output for a single revision.
func Show(ctx context.Context, repoDir, rev string) (string, error) {
	if !isHex(rev) {
		return "", fmt.Errorf("invalid revision: %q", rev)
	}
	// "--" ends option parsing so a revision can never be read as a flag.
	return run(ctx, repoDir, "show", "--stat", "--pretty=fuller", rev, "--")
}

func parseLog(out string) []Commit {
	records := strings.Split(out, recordSep)
	commits := make([]Commit, 0, len(records))
	for _, rec := range records {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.Split(rec, fieldSep)
		if len(parts) < 5 {
			continue
		}
		c := Commit{
			Hash:    parts[0],
			Short:   parts[1],
			Author:  parts[2],
			Subject: parts[4],
		}
		// %aI is strict ISO 8601; keep the instant and let the UI localize it.
		if ts, err := time.Parse(time.RFC3339, parts[3]); err == nil {
			c.When = ts
		}
		commits = append(commits, c)
	}
	return commits
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
