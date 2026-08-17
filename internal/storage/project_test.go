package storage

import (
	"database/sql"
	"testing"
	"time"
)

func TestCreateTopicRegistersProjectWithoutTasks(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateTopic("bada"); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	all, err := s.AllTopicMeta()
	if err != nil {
		t.Fatalf("AllTopicMeta: %v", err)
	}
	if _, ok := all["bada"]; !ok {
		t.Fatalf("created project missing from AllTopicMeta: %v", all)
	}
}

func TestCreateTopicIsIdempotentAndKeepsMetadata(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateTopic("bada"); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if err := s.UpdateTopicMeta("bada", "a sea of tasks", sql.NullTime{}, false); err != nil {
		t.Fatalf("UpdateTopicMeta: %v", err)
	}
	// Re-creating an existing project must not wipe what's already on it.
	if err := s.CreateTopic("bada"); err != nil {
		t.Fatalf("CreateTopic (repeat): %v", err)
	}

	meta, err := s.TopicMeta("bada")
	if err != nil {
		t.Fatalf("TopicMeta: %v", err)
	}
	if meta.Description != "a sea of tasks" {
		t.Fatalf("description clobbered: %q", meta.Description)
	}
}

func TestCreateTopicRejectsEmptyName(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateTopic("   "); err == nil {
		t.Fatal("expected an error for a blank project name")
	}
}

func TestSetTopicRepoRoundTrip(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetTopicRepo("bada", "/Users/han/Project/bada"); err != nil {
		t.Fatalf("SetTopicRepo: %v", err)
	}
	meta, err := s.TopicMeta("bada")
	if err != nil {
		t.Fatalf("TopicMeta: %v", err)
	}
	if meta.RepoPath != "/Users/han/Project/bada" {
		t.Fatalf("RepoPath = %q, want /Users/han/Project/bada", meta.RepoPath)
	}

	all, err := s.AllTopicMeta()
	if err != nil {
		t.Fatalf("AllTopicMeta: %v", err)
	}
	if all["bada"].RepoPath != "/Users/han/Project/bada" {
		t.Fatalf("AllTopicMeta RepoPath = %q", all["bada"].RepoPath)
	}

	if err := s.SetTopicRepo("bada", ""); err != nil {
		t.Fatalf("SetTopicRepo (clear): %v", err)
	}
	meta, err = s.TopicMeta("bada")
	if err != nil {
		t.Fatalf("TopicMeta after clear: %v", err)
	}
	if meta.RepoPath != "" {
		t.Fatalf("RepoPath = %q, want empty after clearing", meta.RepoPath)
	}
}

func TestSetTopicRepoPreservesOtherMetadata(t *testing.T) {
	s := newTestStore(t)

	target := sql.NullTime{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), Valid: true}
	if err := s.UpdateTopicMeta("bada", "ship it", target, true); err != nil {
		t.Fatalf("UpdateTopicMeta: %v", err)
	}
	if err := s.UpdateTopicNote("bada", "notes body"); err != nil {
		t.Fatalf("UpdateTopicNote: %v", err)
	}
	if err := s.SetTopicRepo("bada", "/tmp/repo"); err != nil {
		t.Fatalf("SetTopicRepo: %v", err)
	}

	meta, err := s.TopicMeta("bada")
	if err != nil {
		t.Fatalf("TopicMeta: %v", err)
	}
	if meta.Description != "ship it" || !meta.Archived || meta.Notes != "notes body" {
		t.Fatalf("SetTopicRepo disturbed other fields: %+v", meta)
	}
	if !meta.TargetDate.Valid || !meta.TargetDate.Time.Equal(target.Time) {
		t.Fatalf("target date lost: %+v", meta.TargetDate)
	}
}

func TestRenameTopicCarriesRepoPath(t *testing.T) {
	s := newTestStore(t)
	id := mustAdd(t, s, "wire up git log")
	if err := s.UpdateTaskMetadata(id, "bada", "", "", "", "", 0, sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, false); err != nil {
		t.Fatalf("UpdateTaskMetadata: %v", err)
	}
	if err := s.SetTopicRepo("bada", "/tmp/repo"); err != nil {
		t.Fatalf("SetTopicRepo: %v", err)
	}

	if _, err := s.RenameTopic("bada", "bada-tui"); err != nil {
		t.Fatalf("RenameTopic: %v", err)
	}

	meta, err := s.TopicMeta("bada-tui")
	if err != nil {
		t.Fatalf("TopicMeta: %v", err)
	}
	if meta.RepoPath != "/tmp/repo" {
		t.Fatalf("RepoPath after rename = %q, want /tmp/repo", meta.RepoPath)
	}
}

func TestDeleteTopicRemovesProjectRegistration(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateTopic("bada"); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if err := s.SetTopicRepo("bada", "/tmp/repo"); err != nil {
		t.Fatalf("SetTopicRepo: %v", err)
	}
	if _, err := s.DeleteTopic("bada"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}

	all, err := s.AllTopicMeta()
	if err != nil {
		t.Fatalf("AllTopicMeta: %v", err)
	}
	if _, ok := all["bada"]; ok {
		t.Fatal("deleted project still registered")
	}
}
