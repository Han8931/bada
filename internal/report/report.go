// Package report turns a project's git history and task list into a written
// status report. It keeps the two halves separate on purpose: Digest assembles
// the facts deterministically (and is what the tests pin), while the model is
// only asked to narrate what the digest already contains.
package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bada/internal/config"
	"bada/internal/git"
	"bada/internal/llm"
	"bada/internal/storage"
)

// DefaultSystemPrompt is the built-in instruction set. Users can replace it
// wholesale via [report].system_prompt / system_prompt_file, or add to it with
// extra_instructions; ":report prompt" prints whatever is in force.
const DefaultSystemPrompt = `You write concise project status reports for a developer's terminal todo app.

You are given a factual digest of one project: its git commits and its tasks for a fixed period. Write a Markdown report with these sections, omitting any section that has nothing to say:

## Summary
Two or three sentences on what actually moved this period.

## Shipped
What was completed, grounded in the commits and the completed tasks. Group related commits into one line describing the change, rather than restating the log commit by commit.

## In progress
What is underway and how far along it looks.

## Risks
Overdue tasks, work that has been in progress a long time, and anything the commits suggest is half-finished. Say plainly if there are none.

## Next
The few things that most plausibly come next, based on pending tasks and their due dates.

Rules:
- Use only the facts in the digest. Never invent commits, tasks, dates, names, or numbers.
- Refer to dates as they appear in the digest; they are already the user's local dates.
- Be specific and brief. No preamble, no closing pleasantries, no restating these instructions.
- If the digest contains no commits and no tasks, say only that there was no recorded activity in the period.`

// Input is everything a report is written from. The caller gathers it — the
// tasks are already filtered to the project — so this package performs no
// database access of its own.
type Input struct {
	Project string
	Meta    storage.TopicMeta
	Stages  []storage.Stage
	Period  string // the window as the user typed it, e.g. "7d"
	Days    int
	Now     time.Time
	Since   time.Time
	Tasks   []storage.Task
	Commits []git.Commit
	// RepoNote explains a missing git section (no repo linked, unreadable, …).
	RepoNote string
}

// Since returns the start of the window: the local day that is days-1 days
// before today, so "7d" covers today plus the six days before it rather than a
// ragged 168 hours. Dates in bada are local wall-clock dates.
func Since(now time.Time, days int) time.Time {
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if days <= 1 {
		return startOfDay
	}
	return startOfDay.AddDate(0, 0, -(days - 1))
}

// SystemPrompt is the instruction set in force: the configured override if
// there is one, otherwise the built-in prompt, with extra_instructions
// appended either way.
func SystemPrompt(cfg config.Report) (string, error) {
	prompt := DefaultSystemPrompt
	if path := strings.TrimSpace(cfg.SystemPromptFile); path != "" {
		expanded, err := git.ExpandHome(path)
		if err != nil {
			return "", err
		}
		body, err := os.ReadFile(expanded)
		if err != nil {
			return "", fmt.Errorf("[report].system_prompt_file: %w", err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return "", fmt.Errorf("[report].system_prompt_file %s is empty", expanded)
		}
		prompt = strings.TrimSpace(string(body))
	} else if inline := strings.TrimSpace(cfg.SystemPrompt); inline != "" {
		prompt = inline
	}
	if extra := strings.TrimSpace(cfg.ExtraInstructions); extra != "" {
		prompt += "\n\nAdditional instructions:\n" + extra
	}
	return prompt, nil
}

// Messages is the exact conversation sent to the model.
func Messages(in Input, cfg config.Report) ([]llm.Message, error) {
	prompt, err := SystemPrompt(cfg)
	if err != nil {
		return nil, err
	}
	return []llm.Message{llm.System(prompt), llm.User(Digest(in, cfg))}, nil
}

// Generate writes the report from an already-resolved system prompt (see
// SystemPrompt) — callers that validate the prompt before doing other work
// pass it in rather than have it resolved, and any system_prompt_file
// re-read, a second time. The caller supplies the client so the whole call
// stays cancellable from the UI.
func Generate(ctx context.Context, client *llm.Client, in Input, cfg config.Report, prompt string) (string, error) {
	msgs := []llm.Message{llm.System(prompt), llm.User(Digest(in, cfg))}
	resp, err := client.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	body := strings.TrimSpace(resp.Text)
	if body == "" {
		return "", fmt.Errorf("the model returned an empty report")
	}
	return body, nil
}

// Digest is the factual brief handed to the model: every number in it is
// computed here, so the model is narrating rather than counting.
func Digest(in Input, cfg config.Report) string {
	maxCommits, maxTasks := cfg.Limits()
	var b strings.Builder

	fmt.Fprintf(&b, "Project: %s\n", in.Project)
	if desc := strings.TrimSpace(in.Meta.Description); desc != "" {
		fmt.Fprintf(&b, "Description: %s\n", desc)
	}
	if in.Meta.TargetDate.Valid {
		fmt.Fprintf(&b, "Target date: %s\n", day(in.Meta.TargetDate.Time))
	}
	fmt.Fprintf(&b, "Period: last %s (%s to %s, inclusive, local dates)\n",
		humanDays(in.Days), day(in.Since), day(in.Now))
	if len(in.Stages) > 0 {
		names := make([]string, 0, len(in.Stages))
		for _, s := range in.Stages {
			names = append(names, fmt.Sprintf("%s (%s)", s.Name, s.Category))
		}
		fmt.Fprintf(&b, "Workflow stages: %s\n", strings.Join(names, " → "))
	}

	b.WriteString("\n## Git commits in the period\n")
	switch {
	case strings.TrimSpace(in.RepoNote) != "":
		fmt.Fprintf(&b, "%s\n", in.RepoNote)
	case len(in.Commits) == 0:
		b.WriteString("No commits in this period.\n")
	default:
		fmt.Fprintf(&b, "%d commit(s), newest first:\n", len(in.Commits))
		for i, c := range in.Commits {
			if i >= maxCommits {
				fmt.Fprintf(&b, "- … and %d older commit(s), omitted\n", len(in.Commits)-maxCommits)
				break
			}
			fmt.Fprintf(&b, "- %s %s %s: %s\n", day(c.When), c.Short, c.Author, oneLine(c.Subject))
		}
	}

	buckets := bucketTasks(in)
	b.WriteString("\n## Tasks\n")
	if len(in.Tasks) == 0 {
		b.WriteString("This project has no tasks.\n")
	}
	for _, bucket := range buckets {
		if len(bucket.tasks) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s (%d)\n", bucket.title, len(bucket.tasks))
		for i, t := range bucket.tasks {
			if i >= maxTasks {
				fmt.Fprintf(&b, "- … and %d more, omitted\n", len(bucket.tasks)-maxTasks)
				break
			}
			fmt.Fprintf(&b, "- %s\n", describeTask(t, in.Now))
		}
	}
	return b.String()
}

type bucket struct {
	title string
	tasks []storage.Task
}

// bucketTasks sorts the project's tasks into the sections a status report is
// actually written from. A task appears in exactly one bucket.
func bucketTasks(in Input) []bucket {
	var completed, overdue, active, upcoming, undated []storage.Task
	for _, t := range in.Tasks {
		switch {
		case t.Done || t.CompletedAt.Valid:
			// Only work finished inside the window belongs in a period report.
			// A task marked done with no completion timestamp (restored from
			// trash, or done before completed_at existed) has no date to test
			// against, so it is included rather than silently dropped.
			if !t.CompletedAt.Valid || !t.CompletedAt.Time.Before(in.Since) {
				completed = append(completed, t)
			}
		case t.Due.Valid && t.Due.Time.Before(dayStart(in.Now)):
			overdue = append(overdue, t)
		case isActive(t, in.Stages):
			active = append(active, t)
		case t.Due.Valid:
			upcoming = append(upcoming, t)
		default:
			undated = append(undated, t)
		}
	}
	sort.SliceStable(completed, func(i, j int) bool {
		return completed[i].CompletedAt.Time.After(completed[j].CompletedAt.Time)
	})
	byDue := func(s []storage.Task) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Due.Time.Before(s[j].Due.Time) })
	}
	byDue(overdue)
	byDue(upcoming)
	sort.SliceStable(undated, func(i, j int) bool { return undated[i].Priority > undated[j].Priority })

	return []bucket{
		{"Completed in the period", completed},
		{"In progress", active},
		{"Overdue", overdue},
		{"Upcoming (due later)", upcoming},
		{"Pending, no due date", undated},
	}
}

// isActive reports whether a task sits in a stage the project treats as work
// underway — the built-in IN-PROGRESS, or any custom stage categorized active.
func isActive(t storage.Task, stages []storage.Stage) bool {
	status := strings.ToUpper(strings.TrimSpace(t.Status))
	if status == "IN-PROGRESS" || status == "IN PROGRESS" {
		return true
	}
	for _, s := range stages {
		if strings.EqualFold(s.Name, t.Status) {
			return s.Category == storage.StageActive
		}
	}
	return false
}

// describeTask renders one task as a single line of facts.
func describeTask(t storage.Task, now time.Time) string {
	parts := []string{fmt.Sprintf("%q", oneLine(t.Title))}
	if s := strings.TrimSpace(t.Status); s != "" {
		parts = append(parts, "status "+s)
	}
	if t.Priority > 0 {
		parts = append(parts, fmt.Sprintf("priority P%d", t.Priority))
	}
	if t.Due.Valid {
		parts = append(parts, fmt.Sprintf("due %s (%s)", day(t.Due.Time), relativeDays(t.Due.Time, now)))
	}
	if t.CompletedAt.Valid {
		parts = append(parts, "completed "+day(t.CompletedAt.Time))
	}
	if t.Start.Valid {
		parts = append(parts, "started "+day(t.Start.Time))
	}
	if a := strings.TrimSpace(t.Assignee); a != "" {
		parts = append(parts, "assignee "+a)
	}
	if tags := strings.TrimSpace(t.Tags); tags != "" {
		parts = append(parts, "tags "+tags)
	}
	if n := oneLine(t.Notes); n != "" {
		parts = append(parts, "notes: "+truncate(n, 200))
	}
	return strings.Join(parts, ", ")
}

// --- saving ------------------------------------------------------------------

// Save writes the report as Markdown and returns the path. The file is named
// for the project and the day it was written, so a second report on the same
// day replaces the first rather than littering the directory.
func Save(dir, project, period, model, body string, now time.Time) (string, error) {
	expanded, err := git.ExpandHome(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(expanded, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", expanded, err)
	}
	path := filepath.Join(expanded, fmt.Sprintf("%s-%s.md", slug(project), day(now)))
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — report for %s\n\n", project, day(now))
	fmt.Fprintf(&b, "*Period: last %s · generated %s", period, now.Format("2006-01-02 15:04"))
	if strings.TrimSpace(model) != "" {
		fmt.Fprintf(&b, " by %s", model)
	}
	b.WriteString("*\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// slug makes a project name safe to use as a filename.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(strings.ToLower(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\' || r == '.':
			b.WriteRune('-')
		default:
			// Keep non-ASCII letters (project names are often Korean) but drop
			// anything a path separator could hide in.
			if r > 127 {
				b.WriteRune(r)
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "report"
	}
	return out
}

// --- small helpers -----------------------------------------------------------

func day(t time.Time) string { return t.Format("2006-01-02") }

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func humanDays(days int) string {
	switch {
	case days == 1:
		return "1 day"
	case days == 7:
		return "7 days (one week)"
	case days == 30:
		return "30 days (one month)"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

// relativeDays phrases a due date the way the task list does.
func relativeDays(due, now time.Time) string {
	diff := int(dayStart(due).Sub(dayStart(now)).Hours() / 24)
	switch {
	case diff == 0:
		return "today"
	case diff == 1:
		return "tomorrow"
	case diff == -1:
		return "1 day overdue"
	case diff < 0:
		return fmt.Sprintf("%d days overdue", -diff)
	default:
		return fmt.Sprintf("in %d days", diff)
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
