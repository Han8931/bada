# Bada — UI TODO

Tracking UI/UX work. Impact / effort tags are rough guides.

## Current focus / next ideas

- [ ] Connect to ClaudeCode with Qrush
- [ ] **Refactor UI by view** — continue splitting the big files (`ui.go`,
      `ui_views.go` ~2.2k, `ui_actions.go`) into focused files for list,
      calendar, gantt, stats, metadata modal, notes, search, recurrence, and
      date helpers. (Agenda is already out in `agenda_view.go`.)
- [ ] **Agenda and calendar views** — improve calendar readability.
- [ ] **Gantt view improvements** — continue improving timeline readability,
      navigation, zooming, and task/date presentation.
- [ ] Topic Navigation bar

## Task list readability

- [ ] Monday app style editing (Project-based progress customization)
- [ ] Due-today coloring (amber, between future and overdue) — med / low
      (rows are currently colored only by selection/done state)
- [ ] Zebra striping on alternating rows — low / low
      (`stripeLine` in `style.go` already handles background re-application)

## Layout & structure

- [ ] Split storage code by responsibility (`schema.go`, `tasks.go`, `topics.go`,
      `trash.go`, `time.go`) — still one `storage.go` — med / med
- [ ] Two-pane layout: list left, live detail/metadata panel right — high / high
      (bottom-pinned detail pane exists; side panel still open)
- [ ] Slim top app header bar (name + date + counts) — med / low
- [ ] Responsive columns (drop columns when narrow, widen Title when wide) — med / med
- [ ] Scroll affordances in the list: scrollbar or `▲ N more / ▼ N more`, count in
      panel title — med / low (calendar cells already show `+N more`)

## Feedback & affordances

- [ ] Transient, auto-clearing toasts — med / med (status line exists but
      messages don't auto-clear)
- [ ] Mouse support (click-to-select rows, wheel scroll) — med / med

## Search & filtering

- [ ] Search match highlighting — med / med (live incremental fuzzy find is in;
      matched characters aren't highlighted yet)

## Calendar & Gantt

- [ ] Calendar load heatmap — med / med (per-day dots colored by task state are
      in; day-load heatmap still open)
- [ ] Gantt: color bars by status/priority, weekend shading — med / med

## Quality & maintainability

- [ ] Broaden config tests to defaults, save/load roundtrip, missing fields, and
      `[agenda].upcoming_days` — high / low (`config_test.go` currently only
      covers the cache-dir migration)
- [ ] Introduce a testable clock/`now` helper for agenda, calendar, recurrence,
      and relative due-date behavior — med / med
- [ ] Add versioned SQLite migrations via `PRAGMA user_version` — med / med

## Features (planned — owner: @han)

- [ ] **Job/task detail customization** — let users customize the task detail view
      (which fields show, their order/labels) — high / med
- [ ] **Project-level task management** — manage tasks by project levels
      (a project grouping above topics, with per-project views/filters) — high / high
- [ ] **Git log follow-ups** — now that projects can be linked to a repo
      (`:gitlog`), possible next steps: filter the log by author or path, show
      the current branch and dirty state in the projects overview, and link a
      task to the commits that closed it — med / med
- [ ] **Notifications** — let users get notified about due/overdue tasks and
      reminders. Open questions to settle first: delivery channel (in-app toast /
      terminal bell / OS notification via `osascript`/`notify-send`), whether a
      background daemon or cron entry is needed for notifications while bada
      isn't running, per-task vs. per-project lead times, and a `[notifications]`
      config section (enable, lead time, quiet hours, channel) — high / high

## Done

- [x] **Agenda refactor** — agenda/report rendering, selection, and actions live
      in `internal/ui/agenda_view.go`; shared task actions (status rotate, notes,
      edit, reschedule, jump) extracted to `ui_actions.go` and reused by list,
      agenda, and board.
- [x] **Fuzzy search** — live incremental fuzzy find with a preview cursor.
- [x] Relative due dates (`today`, `tomorrow`, `in 3d`, `2d ago`) in list, board,
      and agenda (`relativeDueCell`).
- [x] Priority cell colored by level (`priorityStyle` flag + badge).
- [x] Sort indicator (`▴/▾`) on the active sort column header.
- [x] Friendlier empty state ("No tasks yet — press `a` to add one.").
- [x] Confirm dialogs for delete/purge/topic removal.
- [x] Standardized timezone semantics (local wall-clock dates, instant
      timestamps) + storage tests; data moved out of the cache dir.
- [x] Theme selection and column-width tuning.
- [x] Board (kanban) view, workflow view, and I Ching daily lesson.
- [x] **Projects as first-class** — create a project without tagging a task
      (`n` / `:project new <name>`), delete one (`D`), link a local git repo
      (`g`), and browse its commits in the `:gitlog` view (`Enter` shows a
      commit, `r` refreshes, `m` pages). `internal/git` wraps the git binary
      read-only; the repo path lives in `topic_notes.repo_path`.
- [x] Agenda row interaction + readability (upcoming window, grouped days,
      priority markers, relative due labels, agenda footer hints).
- [x] ~~Active-filter chips bar~~ — superseded: filter/search state is shown in
      the panel title and status bar by design (see comment in `ui_views.go`).
- [x] ~~Tags as colored chips~~ — obsolete: tags are no longer a list column
      (list shows topic/assignee/reporter instead).

---

Top picks: due-today coloring and list scroll affordances as quick wins, config
test broadening for safety, then the two-pane detail panel or the storage split
for one bigger structural change.
