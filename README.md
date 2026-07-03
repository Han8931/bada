# Bada (바다, "sea")


<p align="center">
  <img src="assets/icon.svg" alt="Bada logo" width="180">
</p>


> **bada** means "sea" (바다) in Korean — a place where important things gather and flow.

**bada** is a Vim-first TUI todo app for capturing what matters, organizing it into projects/tags, and finishing with calm focus.


## Interface

bada uses a framed, "taskdog-inspired" terminal UI:

- **Framed panels** — every screen is drawn in a rounded, titled panel
  (`bada · Tasks`, `bada · Calendar`, `bada · Gantt`, `bada · Trash`,
  `bada · Help`).
- **Timeline panel** — a compact Gantt-style day grid of active dated tasks sits
  above the task table.
- **Task table** — columns for status, title, assignee, reporter, priority
  (`Pn`), due-in (relative, e.g. `in 3d`/`today`/`2d ago`), due, end, topic,
  tags, timezone, recurrence, and notes.
- **Full-width selection bar** highlights the current row; done tasks are
  struck through, multi-selected tasks are tinted.
- **Status-dot legend** (`●  ●  ●  ●`) and a
  **key-hint footer** showing the active shortcuts.
- **Full-width status bar** with the current mode, sort, active filter/search,
  and position.

Tasks are listed directly; each task's topic appears in the table so the list
stays flat and scan-friendly.

### Views

Open with `:` commands (tab to autocomplete), or the shortcuts noted:

- `:agenda` — the daily agenda (also opens on launch). Leads with a day's I Ching
  reading, an at-a-glance triage summary, a completion streak, and a 7-day
  sparkline, then sections for Overdue, Due Today (as a schedule), Upcoming,
  Recurring, No date, and Recently Added/Done. Press `z` to fold the header.
- `:calendar` — month grid; `h/l` day, `j/k` week, `H/L` month, `Enter` day detail.
- `:gantt` — timeline of tasks with start/due bars and a "today" marker.
- `:stats` — productivity dashboard: counts, completions (today/week/month),
  current & longest streaks, a 7-day completion chart, and pending breakdowns by
  priority and topic.
- `:help` (or `?`) — full keybinding reference.
- `:config` — open the config file in `$EDITOR`; bada reloads it (theme,
  keybindings, etc.) when you save and quit the editor.
- `:theme` — list available palettes; `:theme <name>` switches to one
  (`light`, `dark`, `purple`).

Quick filter commands:

- `:overdue` — show overdue unfinished tasks.
- `:pending` — show pending tasks.
- `:in-progress` / `:progress` — show in-progress tasks.
- `:done` / `:completed` — show completed tasks.
- `:today` — show unfinished tasks due today.
- `:week` — show unfinished tasks due in the next 7 days.
- `:all` / `:clear` / `:reset` — return to the original unfiltered list.


## Daily Use

- Status: `r` rotates the task through its status pipeline. By default that is
  `PENDING` → `IN-PROGRESS` → `DONE`, but a project (topic) can define its own
  workflow — see **Projects & custom workflows** below.
- Delete to trash: `D` (`X` deletes all done).
- Edit / rename: `e` opens the metadata editor.
- Priority: `+` / `-` cycles None → Low → Med → High (green / amber / red flag).
- Due shift: `]` / `[` (+1d/-1d).
- Undo: `u` reverts the last in-place edit (status, priority, due, or metadata).
  Deletes are recovered from the Trash (`T`) instead.
- Sort: `s` then `d/p/t/c/o/w/a/s`
  (due/priority/title/created/topic/stage/auto/state).
- `gg` / `G` bindings (jump to top / bottom)
- Quick filters: use commands like `:overdue`, `:pending`, `:today`, `:week`,
  and `:all` to clear.
- Search: `/` opens a query prompt; `F` or `,f` opens fuzzy search;
  `Enter` applies, `Esc` cancels (submit empty to clear).
- Detail & notes: `Enter` opens a task's detail view — status (with an overdue
  tag), topics, priority, due, recurrence, and its notes. Inside it, `e` edits the
  task's fields (the metadata box) and `n` (or `v`) opens the note text editor.
  Works from the task list, the agenda, and the kanban.
- Agenda: opens on launch; type `:agenda` to view again. It leads with an
  at-a-glance summary (overdue / due today / upcoming / done today), a completion
  streak, and a 7-day due-count sparkline, then sections for Overdue, Due Today
  (as a time-ordered schedule), Upcoming, Recurring, No date (prioritized undated
  tasks), and Recently Added/Done. Each row shows its project and workflow stage.
  Use `j/k` to select, `Enter` notes, `r` status, `e` edit, `[`/`]` reschedule,
  `g` jump to the task list, and `z` to fold the header for more room. Scope the
  list to a topic first and the agenda filters to that project (`Agenda · <topic>`).
- Help: `?` (or `:help`) opens the full keybinding reference.
- Other views: `:calendar` (month grid) and `:gantt` (timeline).
- From a filtered/searched/topic-scoped list, `Esc` or `q` returns to the
  original list; when already on the original list, `q` quits.

## Projects & custom workflows

Topics double as **projects**. Open the **projects overview** with `:projects`
(aliases `:topics`, and `:dashboard` for muscle memory) to manage them:

- **Scope** to a project with `Enter` (filters the task list to that topic).
- **Custom status workflow** with `w`: define an ordered pipeline of stages, e.g.
  a thesis project with `writing → review → submission → rebuttal`. In the editor:
  `a` add, `e` rename, `c` cycle a stage's category, `J`/`K` reorder, `D` delete,
  `s` save (`Esc` also saves and closes).
- Each stage has a **category** — `pending`, `active`, or `done` — which drives
  its color. Mark the terminal stage `done`; rotating a task into it completes
  the task (sets its completion time), just like the built-in `DONE`.
- **Project metadata**: `e` edits the description, `t` sets a target date, `a`
  toggles archived. The projects overview shows each project's completion bar,
  overdue count, and stage funnel.
- **Kanban board**: `:kanban` (or `:kanban <topic>`; `:board` is a legacy alias)
  opens a column-per-stage board for the scoped project. `h`/`l` move between
  columns, `j`/`k` between tasks, and `L`/`H` advance a task to the next stage or
  send it back.
- **Stage filter & sort**: `:stage <name>` filters the list to a single workflow
  stage; sort by pipeline position with `s` then `w`.

A task's workflow is governed by its **primary topic** — the *first* topic listed
on the task. Other topics remain plain labels. Tasks whose primary topic has no
custom workflow keep the default `PENDING`/`IN-PROGRESS`/`DONE` behavior, so
existing tasks are unaffected.

## Recurrence Syntax

You can set recurrence in the metadata editor using the `Recurrence` and `Interval` fields.

Examples:

- `every day`
- `every 3 days`
- `every 2 weeks`
- `every 2 weeks on Mon`
- `every month`
- `every month on Fri`
- `daily`, `weekly`, `monthly` (aliases)

Notes:
- Weekday names accept short and long forms: `Mon`/`Monday`, `Tue`/`Tuesday`, etc.
- If `Recurrence` is empty but `Interval` is set, it is treated as `every N days`.
- The UI shows a "Next: YYYY-MM-DD" preview for recurring tasks.

## Theme

bada ships several built-in palettes: **light** (default), **dark**, **purple**,
**ocean**, **forest**, **rose**, and **graphite**. There are four ways to switch:

- **`:theme` command:** run `:theme` to list the available palettes, or
  `:theme <name>` (e.g. `:theme purple`) to switch. The change applies
  immediately and is saved to your `config.toml`.
- **Cycle live:** press `t` in the list view to rotate through the presets,
  applied and saved immediately.
- **Pick a preset:** set `preset = "ocean"`, `"forest"`, `"rose"`, `"graphite"`,
  `"purple"`, `"dark"`, or `"light"` in the `[theme]` section of `config.toml`.
- **Hand-tune colors:** edit any key in `[theme]` to customize headings, accents,
  status bar, selection highlight, etc. Individual keys override the chosen preset,
  so you can start from `preset = "purple"` and adjust a single hue.

The UI is framed in rounded panels with a colored table-header bar, a status-dot
legend, and a key-hint footer. Two notable theme keys drive the framing:

- `border` — color of the rounded panel frames.
- `status_alt_bg` / `status_alt_fg` — the table-header bar and the key-hint chips.

See `config.example.toml` for the full `[theme]` section and explicit palette
values.

## Trash

- Deleted tasks are archived as JSON snapshots in `trash_dir` (default `trash/`).
- Press `T` to open Trash; `space` multi-selects (auto-advances), `u` restores selected/current, `P` purges (with confirm), `esc`/`q` exits.
- Status bar shows cursor, selection count, and trash path; clear the folder to purge manually if needed.

## Install (Linux)

```
./install.sh
```

Options:

```
./install.sh --prefix /usr/local --bin-name bada
```

The installer does a clean rebuild every time and warns if another `bada`
earlier on your `PATH` (e.g. a stale copy in `~/go/bin` or `~/.local/bin`)
would shadow the freshly built binary — that is the usual cause of an "old
version keeps running" after an update.

## Uninstall (Linux)

```
./uninstall.sh
```

Removes the installed binary, the local build artifact, and any other `bada`
copies found on your `PATH`. Use the same `--prefix`/`--bin-name` you installed
with. To also delete user data (config, DB, and trash):

```
./uninstall.sh --purge
```


# Todo

## Basic Features

* Agenda row selection/actions (jump to task, edit, rotate status, reschedule directly from agenda)
* **Data Portability:** Robust Import/Export (CSV/JSON/TOML) and automatic SQLite maintenance (VACUUM/Snapshots).
* Integrate with Gorae / Bori

### Recurring Task

**Recurrence needs some NLP feature to parse and calculate next due date**

- Clearer input model: Allow every X days/weeks/months + optional weekday selector (e.g., every 2 weeks on Mon), while keeping a raw rule fallback.
- Next occurrence preview: Show "Next: YYYY‑MM‑DD" in metadata and in the recurring list so users trust the schedule.
- Skip/shift controls: Add ]/[ to shift next occurrence and a s key to skip just one cycle.
- Completion behavior toggle: Choose whether completing a recurring task creates a new instance or just updates the due date in place.
- End conditions: Support "until date" or "after N occurrences."
- Exception dates: Let users add one‑off skip dates (holidays, vacations).
- Human‑readable labels: Store a normalized rule and a display label (e.g., weekly:mon,wed → "Weekly on Mon/Wed").

## DB and Task Sharing

* Supabase and create API

## AI Features

* **Natural Language Intake:** Convert "Buy milk tomorrow at 5pm" into a structured task with a due date and tags.
* **Strategic Advisory:** AI analyzes your task list to suggest the most efficient order of operations (e.g., "Group these three errands together to save time").
* **Automated Project Planning:** Generate a multi-step task breakdown from a single high-level goal (e.g., "Plan a 3-day hiking trip").
* Writing features: email or report. 


This is a comprehensive and well-thought-out feature set. It strikes a great balance between a "power user" CLI/TUI application and modern AI-driven productivity.

I have polished your existing list for clarity and professional terminology, then added a section of "Next-Level Ideas" to further differentiate your project.


## New Ideas to Consider

### 1. Workflow & Productivity

* **Dependency Tracking:** Mark tasks as "blocked by" another task. The AI can then use this data to suggest a valid path forward.
* **Recurring Logic:** Support for complex recurrences (e.g., "Every 3rd Tuesday" or "2 weeks after completion").
* **Pomodoro Integration:** A built-in timer in the status bar that links directly to the active task for time-tracking.
* **Git-style "Undo":** A command-line history (reflog) so you can revert accidental bulk deletions or project moves.

### 2. Expanded AI Capabilities

* **Complexity Estimation:** Let the AI estimate how many "Story Points" or minutes a task will take based on its title and description.
* **Stale Task Detection:** An AI "nag" that identifies tasks that have been sitting idle for weeks and suggests either deleting them, breaking them down, or rescheduling.
* **Contextual Tagging:** Automatically suggest tags based on the content of the task (e.g., recognizing "Email" and "Boss" and suggesting `@comm` or `@work`).

### 3. Analytics & Reporting

* **Velocity Tracking:** A simple dashboard showing tasks completed per day/week to help you understand your actual capacity.
* **Workload Heatmap:** A visual representation (perhaps in the calendar view) showing which days are dangerously over-scheduled.

### 4. Technical "Pro" Features

* **Hook System:** Allow users to run scripts on certain events (e.g., `on_task_complete` triggers a script that updates a Slack status).
* **Sync Backend:** Optional end-to-end encrypted sync between multiple machines using a simple central relay or Git-based syncing.


# Jira 

- Two-way sync modes: read-only (safe) vs. bidirectional (opt-in), with a clear "source of truth" toggle.
- Task mapping: Jira Issue → Bada Task fields (summary, status, priority, due, labels, assignee, reporter).
- Filters: sync by JQL (e.g., "assigned to me AND status != Done"), per-project toggles.
- Status actions: Bada status changes → Jira transitions (configurable mapping).
- Comment/notes bridge: append Bada notes to Jira comments (or vice versa), with opt-in prefixes.
- Offline queue: local actions stored and pushed when online; show "pending sync" badge.
- Daily view: "Today" panel showing Jira due/overdue issues alongside local tasks.
- Quick commands: :jira sync, :jira open ISSUE-123, :jira assign me.
- Rate-limit and batching: avoid API spamming; show last sync time and errors.
- Auth: API token + base URL stored in config; support per-user profiles.


## RoadMap

Here's a productivity‑first roadmap for Bada that stays personal, fast, and calm:

Phase 1: Capture & Clarity
- Quick capture everywhere: global "inbox" + minimal friction add flow
- Better defaults: smarter default filter, recent tasks, quick tags
- Lightweight notes: faster note edit/preview, auto‑save

Phase 2: Focus & Flow

- Focus mode: single task focus with minimal UI
- Time‑blocking hints: "today" section, due/overdue clarity
- Gentle reminders: daily review screen and "next 3 days" digest

Phase 3: Review & Reflection

- Weekly review view: stale tasks, overdue, and "someday"
- Simple stats: completed today/this week, streaks (optional)
- Archive hygiene: bulk cleanup and restore improvements

Phase 4: Organization

- Topics/tags hygiene: rename/merge tooling, quick re-tagging
- Saved searches: "contexts" like @home, @deepwork
- Priority & energy pairing: low/medium/high effort tagging

Phase 5: Quality of Life

- Undo history for destructive actions
- Export/import backups (CSV/JSON)
- Theming polish + keyboard ergonomics refinements
- If you want, I can turn this into a GitHub milestones list with issue titles and acceptance criteria.
