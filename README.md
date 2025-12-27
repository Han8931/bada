# Bada (바다, "sea")


<p align="center">
  <img src="assets/icon.svg" alt="Bada logo" width="180">
</p>


> **bada** means "sea" (바다) in Korean — a place where important things gather and flow.

**bada** is a Vim-first TUI todo app for capturing what matters, organizing it into projects/tags, and finishing with calm focus.



## Todo

Here are some focused feature ideas to grow the app:
- Filtering & search: quick filters (all/active/done), project/tag filters, text search on title/tags.
- Sorting: by due date, priority, created time; optionally pin tasks.
- Quick edits in list: inline rename (r), change priority (+/-), shift due date (+1d/-1d).
- Recurring templates: set recurrence rules (daily/weekly/weekday-only) with auto-generation when completed.
- Reminders: flag overdue/soon-due tasks, optional notification hook (stdout or simple command hook).
- Bulk actions: multi-select (mark done/delete/move project) using visual selection.
- Import/export: plain text/CSV/TOML export; simple import to seed tasks.
- Persistence niceties: backups/versioned snapshots of the SQLite DB, vacuum command.
- Theming: light/dark presets, configurable border/separator characters in the split view.
- UX polish: status bar with counts (total/active/done/overdue), help overlay (?), and a footer showing current filter/sort.
