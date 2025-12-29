# Bada (바다, "sea")


<p align="center">
  <img src="assets/icon.svg" alt="Bada logo" width="180">
</p>


> **bada** means "sea" (바다) in Korean — a place where important things gather and flow.

**bada** is a Vim-first TUI todo app for capturing what matters, organizing it into projects/tags, and finishing with calm focus.


## Daily Use

- Rename: `r` (shows current/new), `Enter` saves, `Esc` cancels.
- Priority: `+` / `-` (max 10).
- Due shift: `]` / `[` (+1d/-1d).
- Sort: `s` then `d/p/t/a/s` (due/priority/created/auto/state).
- `gg` / `G` bindings (jump to top / bottom)
- Search: `/` opens a query prompt; `Enter` applies, `Esc` cancels (submit empty to clear).
- Notes: `Enter` to preview notes, `e` to edit notes inside the preview (works for tasks or topic rows; not available for RecentlyAdded/RecentlyDone).
- Reminder report: opens on launch; type `:agenda` to view again (shows overdue/today/next 3d pending tasks).

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
- The UI shows a “Next: YYYY-MM-DD” preview for recurring tasks.

## Theme

Edit the `[theme]` section in `config.toml` (see `config.example.toml`) to customize colors for headings, accents, status bar, and selection highlight.

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


# Todo

## Basic Features

* Update help command
* Agenda reporting config (+7 or +3)
* **Temporal Views:** A traditional list view plus a **Gantt view** to visualize deadlines.
* **Data Portability:** Robust Import/Export (CSV/JSON/TOML) and automatic SQLite maintenance (VACUUM/Snapshots).
* Integrate with Gorae / Bori

### Recurring Task

**Recurrence needs some NLP feature to parse and calculate next due date**

- Clearer input model: Allow every X days/weeks/months + optional weekday selector (e.g., every 2 weeks on Mon), while keeping a raw rule fallback.
- Next occurrence preview: Show “Next: YYYY‑MM‑DD” in metadata and in the recurring list so users trust the schedule.
- Skip/shift controls: Add ]/[ to shift next occurrence and a s key to skip just one cycle.
- Completion behavior toggle: Choose whether completing a recurring task creates a new instance or just updates the due date in place.
- End conditions: Support “until date” or “after N occurrences.”
- Exception dates: Let users add one‑off skip dates (holidays, vacations).
- Human‑readable labels: Store a normalized rule and a display label (e.g., weekly:mon,wed → “Weekly on Mon/Wed”).

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
