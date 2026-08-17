# Bada User Guide

This guide covers installation, everyday keyboard workflows, views, projects,
custom workflows, recurrence, themes, storage, and maintenance. For a short
project overview, see the [README](README.md).

## Contents

- [Quick Start](#quick-start)
- [Views](#views)
- [Daily Use](#daily-use)
- [Create / Edit Task dialog](#create--edit-task-dialog)
- [Projects & custom workflows](#projects--custom-workflows)
- [Recurrence Syntax](#recurrence-syntax)
- [Theme](#theme)
- [Data locations](#data-locations)
- [Trash](#trash)
- [Install](#install-linux)
- [Uninstall](#uninstall-linux)

## Quick Start

Bada currently supports Linux and macOS and requires Go 1.25.5 or newer. From a
source checkout:

```bash
./install.sh
bada
```

To run it without installing:

```bash
go build -o bin/bada ./cmd/todo
./bin/bada
```

Bada stores tasks locally; see [Data locations](#data-locations) for the config,
database, and trash paths.


## Views

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
  (see **Theme** below for the full set; `Tab` cycles the names).

Quick filter commands:

- `:overdue` — show overdue unfinished tasks.
- `:pending` — show pending tasks.
- `:in-progress` / `:progress` — show in-progress tasks.
- `:done` / `:completed` — show completed tasks.
- `:today` — show unfinished tasks due today.
- `:week` — show unfinished tasks due in the next 7 days.
- `:all` / `:clear` / `:reset` — return to the original unfiltered list.


## Daily Use

- Add a task: `a` opens the **Create Task** dialog (see below).
- Status: `r` rotates the task through its status pipeline. By default that is
  `PENDING` → `IN-PROGRESS` → `DONE`, but a project (topic) can define its own
  workflow — see **Projects & custom workflows** below.
- Select: `space` multi-selects rows (auto-advances) so `D` acts on the whole
  selection.
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
- Agenda controls: use `j/k` to select, `Enter` for details, `r` to rotate
  status, `e` to edit, `[`/`]` to reschedule, `g` to jump to the task list, and
  `z` to fold the header. Scope the list to a topic first to filter the agenda to
  that project (`Agenda · <topic>`).
- Help: `?` (or `:help`) opens the full keybinding reference.
- Other views: `:calendar` (month grid) and `:gantt` (timeline).
- Command line: `:` opens it. `Tab` completes the command name (and cycles
  palette names after `:theme`), `↑`/`↓` walk previously run commands, `Esc`
  cancels.
- From a filtered/searched/topic-scoped list, `Esc` or `q` returns to the
  original list; when already on the original list, `q` quits.

### Create / Edit Task dialog

`a` (add) and `e` (edit) open the same centered metadata dialog over a dimmed
task list. New tasks initially show **Title**, **Topic**, **Priority**, and
**Due**, plus a `▸ More details` row. Existing tasks open with all fields
visible. Use `space`, `→`, or `Enter` on **More details** to reveal **Tags**,
**Assignee**, **Reporter**, **Start**, **End**, **Timezone**, **Recurrence**,
**Interval**, and **Notes**.

- Move between fields with `↑`/`↓` or `Tab`/`Shift+Tab`; type straight into text
  fields.
- `Ctrl+S` saves from anywhere. `Enter` normally saves, but expands **More
  details** or accepts an open autocomplete selection when applicable. `Esc`
  cancels and discards.
- `Ctrl+N` / `Ctrl+P` cycle autocomplete suggestions. On **Topic** and **Tags**,
  `Tab` instead opens a dropdown of previously-used values — `↑`/`↓` pick,
  `Enter` accepts, `Esc` closes, and typing keeps filtering it.
- **Priority** is a stepper (`‹ P3 ›`) driven by `+`/`-` or `←`/`→` rather than
  typing.
- **Due** is a date stepper: `←`/`→` pick the year/month/day/hour/minute part,
  digits type the selected part directly (and auto-advance), `+`/`-` step it, and
  `x` clears the due date entirely. New tasks start with today prefilled.
- The dialog's **Notes** field is single-line; use `n` (or `v`) in a task's detail
  view for the full multi-line note editor.

## Projects & custom workflows

Topics double as **projects**. Open the **projects overview** with `:projects`
(aliases `:topics`, and `:dashboard` for muscle memory) to manage them:

- **Create a project** with `n` (or `:project new <name>`). A project normally
  comes into being the moment you tag a task with it, but this registers one
  up front — useful when you want the project, its repo, and its workflow set
  up before filing any work under it. Empty projects show in the overview with
  a `0/0` bar.
- **Scope** to a project with `Enter` (filters the task list to that topic).
- **Delete a project** with `D` (confirms first). Its tasks are kept — they just
  lose the topic.
- **Link a git repo** with `g`: point the project at a local repository path
  (`~` is expanded). `Tab` completes the path against the filesystem — it fills
  in the unambiguous part and lists the matching directories underneath the
  prompt, marking the ones that are already git repositories with `●`, so you
  can find the directory without leaving bada. Tab again to descend into a
  completed directory. bada validates the path and stores the repository's top
  level, so pointing at any subdirectory works. Press `g` and submit an empty
  value to unlink. The linked repo shows in the project's detail panel.
- **Browse its commits** with `L`, or `:gitlog` (`:gitlog <project>` picks one
  explicitly; with no argument it uses the scoped project). The log lists each
  commit's short sha, date, author, and subject. `j`/`k` move, `Enter` opens
  `git show --stat` for the selected commit, `r` refreshes, and `m` loads
  another 100 commits. History is read in the background, so a large repository
  never blocks the UI. bada only ever *reads* your repository.
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
- **Pick a preset:** set `preset = "<name>"` in the `[theme]` section of
  `config.toml`, using one of the palette names listed above.
- **Hand-tune colors:** edit any key in `[theme]` to customize headings, accents,
  status bar, selection highlight, etc. Individual keys override the chosen preset,
  so you can start from `preset = "purple"` and adjust a single hue.

The UI is framed in rounded panels with a colored table-header bar, a status-dot
legend, and a key-hint footer. Two notable theme keys drive the framing:

- `border` — color of the rounded panel frames.
- `status_alt_bg` / `status_alt_fg` — the table-header bar and the key-hint chips.

See `config.example.toml` for the full `[theme]` section and explicit palette
values.

## Data locations

- Config: `$XDG_CONFIG_HOME/bada/config.toml` (default `~/.config/bada/config.toml`).
- Database and trash: `$XDG_DATA_HOME/bada` (default `~/.local/share/bada`).
  Older installs kept these under `~/.cache/bada`; on startup bada moves them to
  the data directory and updates the config automatically.

## Trash

- Deleted tasks are archived as JSON snapshots in `trash_dir` (default `~/.local/share/bada/trash`).
- Press `T` to open Trash; `space` multi-selects (auto-advances), `u` restores selected/current, `P` purges (with confirm), `esc`/`q` exits.
- Status bar shows cursor, selection count, and trash path; clear the folder to purge manually if needed.

## Install (Linux)

The installer builds from source using the Go version listed in
[Quick Start](#quick-start).

```bash
./install.sh
```

Options:

```bash
./install.sh --prefix /usr/local --bin-name bada
```

The installer does a clean rebuild every time and warns if another `bada`
earlier on your `PATH` (e.g. a stale copy in `~/go/bin` or `~/.local/bin`)
would shadow the freshly built binary — that is the usual cause of an "old
version keeps running" after an update.

To verify the checkout:

```bash
go test ./...
```

## Uninstall (Linux)

```bash
./uninstall.sh
```

Removes the installed binary, the local build artifact, and any other `bada`
copies found on your `PATH`. Use the same `--prefix`/`--bin-name` you installed
with. To also delete user data (config, DB, and trash):

```bash
./uninstall.sh --purge
```


## Roadmap

Implementation work and UI/UX improvements are tracked in [TODO.md](TODO.md).
The larger product directions are:

- **Portability:** CSV/JSON/TOML import and export, database maintenance, and
  snapshots.
- **Recurrence:** skip controls, completion behavior, end conditions, exception
  dates, richer rules, and clearer human-readable labels.
- **Focus and review:** focus mode, reminders, weekly review, workload views,
  saved searches, and organization tools.
- **Extensibility:** hooks, optional encrypted sync, APIs, and integrations with
  Gorae, Bori, and Jira.
- **Assisted planning:** optional natural-language capture, task breakdowns,
  contextual tagging, stale-task detection, and complexity estimates.

These items are proposals rather than currently available features.
