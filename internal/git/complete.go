package git

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// DirMatch is one candidate directory for a partially typed repo path.
type DirMatch struct {
	Name   string // base name, without any parent path
	IsRepo bool   // holds a .git entry, so it can be linked as-is
}

// CompleteDir expands a partially typed directory path against the filesystem,
// the way a shell does. It returns the completed input — unchanged when nothing
// matches — along with every candidate, so the caller can fill the field and
// still show what else was available.
//
// Only directories are offered, since a repo path is never a file, and the "~"
// prefix the user typed is preserved rather than expanded, keeping the field
// short enough to read inside a panel.
func CompleteDir(input string) (string, []DirMatch) {
	// A bare "~" has no separator to split on yet; open it so the next Tab can
	// list the home directory.
	if strings.TrimSpace(input) == "~" {
		return "~/", nil
	}
	parent, base := splitDirInput(input)
	scan, err := expandHome(parent)
	if err != nil {
		return input, nil
	}
	if scan == "" {
		scan = "."
	}
	entries, err := os.ReadDir(scan)
	if err != nil {
		return input, nil
	}
	lowerBase := strings.ToLower(base)
	var matches []DirMatch
	for _, e := range entries {
		name := e.Name()
		// Hidden directories stay out of the way until asked for by name.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		// Case-insensitive, because nobody remembers whether they named it
		// "Project" or "project" — and the completion writes the real case back.
		if !strings.HasPrefix(strings.ToLower(name), lowerBase) {
			continue
		}
		full := filepath.Join(scan, name)
		if !isDir(e, full) {
			continue
		}
		matches = append(matches, DirMatch{Name: name, IsRepo: isRepo(full)})
	}
	if len(matches) == 0 {
		return input, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })

	shared := matches[0].Name
	for _, m := range matches[1:] {
		shared = commonPrefix(shared, m.Name)
	}
	// Matching case-insensitively means the shared prefix can be shorter than
	// what was typed; never delete the user's own characters in that case.
	if len(shared) < len(base) {
		return input, matches
	}
	// One match is unambiguous, so close it with a separator and let the next
	// Tab descend into it.
	if len(matches) == 1 {
		shared += "/"
	}
	return parent + shared, matches
}

// splitDirInput divides typed input into the directory to scan and the partial
// name being completed inside it. The parent keeps its trailing separator so
// the completion can simply be appended to it.
func splitDirInput(input string) (parent, base string) {
	input = strings.TrimSpace(input)
	if i := strings.LastIndex(input, "/"); i >= 0 {
		return input[:i+1], input[i+1:]
	}
	return "", input
}

// isDir reports whether the entry is a directory, following symlinks — a repo
// reached through a symlinked parent is still a perfectly good repo.
func isDir(e os.DirEntry, full string) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && info.IsDir()
}

// isRepo reports whether dir holds a .git entry. It is a directory in a normal
// clone and a file in a worktree or submodule, so only existence is checked.
func isRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// commonPrefix returns the longest shared prefix of a and b, trimmed back to a
// rune boundary so a truncated multi-byte name never becomes mojibake.
func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	p := a[:i]
	for len(p) > 0 && !utf8.ValidString(p) {
		p = p[:len(p)-1]
	}
	return p
}
