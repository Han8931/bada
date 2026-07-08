package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateCacheDataMovesDBAndTrash(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))

	oldDB := filepath.Join(DefaultCacheDir(), DefaultDBName)
	oldTrash := filepath.Join(DefaultCacheDir(), DefaultTrashDir)
	if err := os.MkdirAll(oldTrash, 0o755); err != nil {
		t.Fatalf("mkdir old trash: %v", err)
	}
	if err := os.WriteFile(oldDB, []byte("db-bytes"), 0o644); err != nil {
		t.Fatalf("write old db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldTrash, "t.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write old trash entry: %v", err)
	}

	cfgPath := DefaultConfigPath()
	cfg := defaultConfig()
	cfg.DBPath = oldDB
	cfg.TrashDir = oldTrash

	moved, err := MigrateCacheData(&cfg, cfgPath)
	if err != nil {
		t.Fatalf("MigrateCacheData: %v", err)
	}
	if !moved {
		t.Fatal("expected migration to report a change")
	}
	if cfg.DBPath != DefaultDBPath() || cfg.TrashDir != DefaultTrashPath() {
		t.Errorf("config not repointed: db=%q trash=%q", cfg.DBPath, cfg.TrashDir)
	}
	if data, err := os.ReadFile(cfg.DBPath); err != nil || string(data) != "db-bytes" {
		t.Errorf("db not moved intact: %v %q", err, data)
	}
	if _, err := os.Stat(filepath.Join(cfg.TrashDir, "t.json")); err != nil {
		t.Errorf("trash entry not moved: %v", err)
	}
	if _, err := os.Stat(oldDB); !os.IsNotExist(err) {
		t.Errorf("old db still present (err %v)", err)
	}
	// The rewritten config must load with the new paths.
	loaded, err := LoadOrCreate(cfgPath)
	if err != nil {
		t.Fatalf("LoadOrCreate after migration: %v", err)
	}
	if loaded.DBPath != cfg.DBPath || loaded.TrashDir != cfg.TrashDir {
		t.Errorf("saved config paths = %q/%q, want %q/%q", loaded.DBPath, loaded.TrashDir, cfg.DBPath, cfg.TrashDir)
	}
}

func TestMigrateCacheDataLeavesCustomPathsAlone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))

	custom := filepath.Join(root, "elsewhere", "my.db")
	cfg := defaultConfig()
	cfg.DBPath = custom
	cfg.TrashDir = filepath.Join(root, "elsewhere", "trash")

	moved, err := MigrateCacheData(&cfg, DefaultConfigPath())
	if err != nil {
		t.Fatalf("MigrateCacheData: %v", err)
	}
	if moved {
		t.Error("migration should not touch custom paths")
	}
	if cfg.DBPath != custom {
		t.Errorf("custom db path changed to %q", cfg.DBPath)
	}
}
