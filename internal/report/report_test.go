package report

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bada/internal/config"
	"bada/internal/git"
	"bada/internal/storage"
)

func onDay(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }

// sampleInput is a project with one task in every bucket.
func sampleInput(t *testing.T) Input {
	t.Helper()
	now := onDay(t, "2026-09-02").Add(10 * time.Hour)
	return Input{
		Project: "bada",
		Meta: storage.TopicMeta{
			Topic:       "bada",
			Description: "Vim-first TUI todo app",
			TargetDate:  nullTime(onDay(t, "2026-10-01")),
		},
		Stages:  []storage.Stage{{Name: "writing", Category: storage.StageActive}},
		Period:  "7d",
		Days:    7,
		Now:     now,
		Since:   Since(now, 7),
		Commits: []git.Commit{{Short: "4d2c682", Author: "han", When: onDay(t, "2026-09-01"), Subject: "Link projects to git repos"}},
		Tasks: []storage.Task{
			{ID: 1, Title: "Ship the LLM client", Status: "DONE", Done: true, CompletedAt: nullTime(onDay(t, "2026-08-30"))},
			{ID: 2, Title: "Write the report view", Status: "IN-PROGRESS", Priority: 2},
			{ID: 3, Title: "Fix the timezone bug", Status: "PENDING", Due: nullTime(onDay(t, "2026-08-20"))},
			{ID: 4, Title: "Polish the gantt view", Status: "PENDING", Due: nullTime(onDay(t, "2026-09-20"))},
			{ID: 5, Title: "Someday: plugins", Status: "PENDING"},
			{ID: 6, Title: "Ancient history", Status: "DONE", Done: true, CompletedAt: nullTime(onDay(t, "2026-01-01"))},
		},
	}
}

// Since must cover whole local days: "7d" is today plus the six days before it.
func TestSinceCoversWholeLocalDays(t *testing.T) {
	now := onDay(t, "2026-09-02").Add(23 * time.Hour)
	got := Since(now, 7)
	if want := onDay(t, "2026-08-27"); !got.Equal(want) {
		t.Errorf("Since(7d) = %s, want %s", got, want)
	}
	if got.Location() != now.Location() {
		t.Errorf("Since must stay in the local zone, got %s", got.Location())
	}
	if got := Since(now, 1); !got.Equal(onDay(t, "2026-09-02")) {
		t.Errorf("Since(1d) = %s, want the start of today", got)
	}
}

func TestDigestGroupsTasksIntoBuckets(t *testing.T) {
	digest := Digest(sampleInput(t), config.Report{})

	for _, want := range []string{
		"Project: bada",
		"Description: Vim-first TUI todo app",
		"Target date: 2026-10-01",
		"last 7 days (one week) (2026-08-27 to 2026-09-02",
		"writing (active)",
		"4d2c682 han: Link projects to git repos",
		"### Completed in the period (1)",
		"### In progress (1)",
		"### Overdue (1)",
		"### Upcoming (due later) (1)",
		"### Pending, no due date (1)",
		"13 days overdue",
		"in 18 days",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest is missing %q\n---\n%s", want, digest)
		}
	}

	// A task completed long before the window is out of scope for the period.
	if strings.Contains(digest, "Ancient history") {
		t.Errorf("a task completed outside the period must not appear:\n%s", digest)
	}
}

// The overdue bucket is about dates, the in-progress bucket about stages: a
// task must land in exactly one of them.
func TestDigestBucketsAreExclusive(t *testing.T) {
	in := sampleInput(t)
	in.Tasks = []storage.Task{
		{ID: 1, Title: "Late and underway", Status: "IN-PROGRESS", Due: nullTime(onDay(t, "2026-08-01"))},
	}
	digest := Digest(in, config.Report{})
	if strings.Count(digest, "Late and underway") != 1 {
		t.Errorf("task should appear exactly once:\n%s", digest)
	}
	if !strings.Contains(digest, "### Overdue (1)") {
		t.Errorf("an overdue task belongs in Overdue:\n%s", digest)
	}
}

// A task marked done with no completion timestamp — e.g. restored from
// trash, or done from before completed_at existed — must still show up
// somewhere, not vanish from the report.
func TestDigestKeepsDoneTasksWithNoCompletedAt(t *testing.T) {
	in := sampleInput(t)
	in.Tasks = []storage.Task{
		{ID: 1, Title: "Restored from trash", Status: "DONE", Done: true},
	}
	digest := Digest(in, config.Report{})
	if !strings.Contains(digest, "Restored from trash") {
		t.Errorf("a done task with no completed_at must still appear:\n%s", digest)
	}
	if !strings.Contains(digest, "### Completed in the period (1)") {
		t.Errorf("it belongs in Completed:\n%s", digest)
	}
}

// A project with a custom workflow has no "IN-PROGRESS" status at all; the
// stage's category is what marks work as underway.
func TestDigestUsesCustomStageCategories(t *testing.T) {
	in := sampleInput(t)
	in.Stages = []storage.Stage{
		{Name: "drafting", Category: storage.StagePending},
		{Name: "review", Category: storage.StageActive},
	}
	in.Tasks = []storage.Task{{ID: 1, Title: "Chapter 3", Status: "review"}}
	digest := Digest(in, config.Report{})
	if !strings.Contains(digest, "### In progress (1)") {
		t.Errorf("a task in an active custom stage belongs in In progress:\n%s", digest)
	}
}

func TestDigestReportsAnEmptyProject(t *testing.T) {
	in := sampleInput(t)
	in.Tasks, in.Commits = nil, nil
	digest := Digest(in, config.Report{})
	if !strings.Contains(digest, "No commits in this period") {
		t.Errorf("digest should state there were no commits:\n%s", digest)
	}
	if !strings.Contains(digest, "This project has no tasks") {
		t.Errorf("digest should state there are no tasks:\n%s", digest)
	}
}

func TestDigestExplainsAMissingRepo(t *testing.T) {
	in := sampleInput(t)
	in.Commits = nil
	in.RepoNote = "No git repository is linked to this project."
	digest := Digest(in, config.Report{})
	if !strings.Contains(digest, "No git repository is linked") {
		t.Errorf("digest should carry the repo note:\n%s", digest)
	}
}

// The prompt has to stay inside the model's context window, however busy the
// period was.
func TestDigestRespectsLimits(t *testing.T) {
	in := sampleInput(t)
	for i := 0; i < 50; i++ {
		in.Commits = append(in.Commits, git.Commit{Short: "abc123", Author: "han", When: in.Now, Subject: "work"})
		in.Tasks = append(in.Tasks, storage.Task{ID: 100 + i, Title: "pending task", Status: "PENDING"})
	}
	digest := Digest(in, config.Report{MaxCommits: 5, MaxTasks: 5})
	if !strings.Contains(digest, "older commit(s), omitted") {
		t.Errorf("digest should note the omitted commits:\n%s", digest)
	}
	if !strings.Contains(digest, "more, omitted") {
		t.Errorf("digest should note the omitted tasks:\n%s", digest)
	}
	if got := strings.Count(digest, "- 2026-09-02 abc123"); got > 5 {
		t.Errorf("listed %d commits, want at most 5", got)
	}
}

func TestSystemPromptDefaults(t *testing.T) {
	got, err := SystemPrompt(config.Report{})
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}
	if got != DefaultSystemPrompt {
		t.Error("an unconfigured report should use the built-in prompt")
	}
}

func TestSystemPromptInlineOverride(t *testing.T) {
	got, err := SystemPrompt(config.Report{SystemPrompt: "Be terse."})
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}
	if got != "Be terse." {
		t.Errorf("SystemPrompt() = %q, want the configured override", got)
	}
}

func TestSystemPromptFileWinsOverInline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(path, []byte("  From a file.\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	got, err := SystemPrompt(config.Report{SystemPrompt: "inline", SystemPromptFile: path})
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}
	if got != "From a file." {
		t.Errorf("SystemPrompt() = %q, want the file's contents", got)
	}
}

func TestSystemPromptExtraInstructionsAreAppended(t *testing.T) {
	got, err := SystemPrompt(config.Report{ExtraInstructions: "Write in Korean."})
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}
	if !strings.HasPrefix(got, DefaultSystemPrompt) {
		t.Error("extra instructions should add to the default, not replace it")
	}
	if !strings.Contains(got, "Write in Korean.") {
		t.Errorf("SystemPrompt() = %q, want the extra instructions", got)
	}

	got, err = SystemPrompt(config.Report{SystemPrompt: "Be terse.", ExtraInstructions: "In Korean."})
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}
	if !strings.Contains(got, "Be terse.") || !strings.Contains(got, "In Korean.") {
		t.Errorf("SystemPrompt() = %q, want both the override and the extras", got)
	}
}

func TestSystemPromptReportsABadFile(t *testing.T) {
	_, err := SystemPrompt(config.Report{SystemPromptFile: filepath.Join(t.TempDir(), "missing.md")})
	if err == nil {
		t.Fatal("expected an error for a missing prompt file")
	}
	if !strings.Contains(err.Error(), "system_prompt_file") {
		t.Errorf("error should name the setting, got %v", err)
	}

	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := SystemPrompt(config.Report{SystemPromptFile: empty}); err == nil {
		t.Error("expected an error for an empty prompt file")
	}
}

func TestMessagesCarryPromptAndDigest(t *testing.T) {
	msgs, err := Messages(sampleInput(t), config.Report{SystemPrompt: "Be terse."})
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Messages() returned %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "Be terse." {
		t.Errorf("first message = %+v, want the system prompt", msgs[0])
	}
	if msgs[1].Role != "user" || !strings.Contains(msgs[1].Content, "Project: bada") {
		t.Errorf("second message should be the digest, got %+v", msgs[1])
	}
}

func TestSaveWritesMarkdown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reports")
	now := onDay(t, "2026-09-02")
	path, err := Save(dir, "bada", "7d", "llama3.2", "## Summary\nAll good.", now)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want := filepath.Join(dir, "bada-2026-09-02.md"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, want := range []string{"# bada — report for 2026-09-02", "Period: last 7d", "by llama3.2", "## Summary", "All good."} {
		if !strings.Contains(string(body), want) {
			t.Errorf("saved report missing %q:\n%s", want, body)
		}
	}

	// A second report the same day replaces the first rather than piling up.
	if _, err := Save(dir, "bada", "7d", "llama3.2", "## Summary\nRevised.", now); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("wrote %d files, want 1", len(entries))
	}
}

// A project name must never be able to steer the write outside the directory.
func TestSaveSanitizesTheProjectName(t *testing.T) {
	dir := t.TempDir()
	path, err := Save(dir, "../../etc/passwd", "7d", "m", "body", onDay(t, "2026-09-02"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("report escaped its directory: %q", path)
	}
	if strings.Contains(filepath.Base(path), "/") || strings.Contains(filepath.Base(path), "..") {
		t.Errorf("unsafe filename %q", filepath.Base(path))
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"bada":          "bada",
		"My Project":    "my-project",
		"a/b":           "a-b",
		"...":           "report",
		"바다":            "바다",
		"Q3 (planning)": "q3-planning",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRelativeDays(t *testing.T) {
	now := onDay(t, "2026-09-02").Add(9 * time.Hour)
	for in, want := range map[string]string{
		"2026-09-02": "today",
		"2026-09-03": "tomorrow",
		"2026-09-01": "1 day overdue",
		"2026-08-30": "3 days overdue",
		"2026-09-09": "in 7 days",
	} {
		if got := relativeDays(onDay(t, in), now); got != want {
			t.Errorf("relativeDays(%s) = %q, want %q", in, got, want)
		}
	}
}
