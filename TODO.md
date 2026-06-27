# Bada — UI TODO

Tracking UI/UX work. Impact / effort tags are rough guides.

## Current focus / next ideas

- [ ] **Fuzzy search** — improve search with fuzzy matching and clearer result
      highlighting.
- [ ] **Agenda and calendar views** — improve the agenda flow and calendar view
      readability/interaction.
- [ ] **Gantt view improvements** — continue improving timeline readability,
      navigation, zooming, and task/date presentation.
- [ ] Topic Navigation bar

## Done


## Task list readability

- [ ] Monday app style editing (Project-based progress customization)
- [ ] Relative due dates (`today`, `tomorrow`, `in 3d`, `2d ago`), colored — high / low
- [ ] Priority heat: color the `Pn` cell by level (or dots/bars) — med / low
- [ ] Due-today coloring (amber, between future and overdue) — med / low
- [ ] Tags as colored chips instead of `[a,b]` — low / low
- [ ] Zebra striping on alternating rows — low / low
- [ ] Sort indicator (`▲/▼`) on the active sort column header — med / low

## Layout & structure

- [ ] Two-pane layout: list left, live detail/metadata panel right — high / high
- [ ] Slim top app header bar (name + date + counts) — med / low
- [ ] Responsive columns (drop Tags/Pri when narrow, widen Title when wide) — med / med
- [ ] Scroll affordances: scrollbar or `▲ N more / ▼ N more`, count in panel title — med / low

## Feedback & affordances

- [ ] Modal confirm dialogs (centered boxes) for delete/purge — med / med
- [ ] Transient, auto-clearing toasts for actions — med / med
- [ ] Friendlier empty states ("No tasks — press `a` to add") — low / low
- [ ] Mouse support (click-to-select rows, wheel scroll) — med / med

## Color & theme

- [ ] Adaptive theme (`lipgloss.AdaptiveColor` / detect light vs dark) or dark default — high / low
- [ ] More theme tokens: priority levels, due-today, tags — low / low

## Search & filtering

- [ ] Live incremental search with match highlighting — high / med
- [ ] Active-filter chips bar (done/topic/search) — med / med

## Calendar & Gantt

- [ ] Calendar load heatmap + per-day priority dots — med / med
- [ ] Gantt: color bars by status/priority, weekend shading, clearer "today" line — med / med

## Features (planned — owner: @han)

- [ ] **Job/task detail customization** — let users customize the task detail view
      (which fields show, their order/labels) — high / med
- [ ] **Project-level task management** — manage tasks by project levels
      (a project grouping above topics, with per-project views/filters) — high / high

---

Top picks: relative due dates, adaptive/dark default, scroll affordances + count in
title, and the two-pane detail panel for one bigger transformative change.
