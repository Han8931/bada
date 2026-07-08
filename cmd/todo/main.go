package main

import (
	"errors"
	"fmt"
	"os"

	"bada/internal/config"
	"bada/internal/storage"
	"bada/internal/ui"
)

func main() {
	configPath := config.ResolveConfigPath()
	firstLaunch := false
	if _, err := os.Stat(configPath); err != nil {
		firstLaunch = errors.Is(err, os.ErrNotExist)
	}
	cfg, err := config.LoadOrCreate(configPath)
	if err != nil {
		fmt.Printf("failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Older installs kept the DB in the cache directory; move it to the data
	// dir so cache cleanup can't wipe task history. On failure, keep running
	// against the old location.
	if _, err := config.MigrateCacheData(&cfg, configPath); err != nil {
		fmt.Printf("warning: could not move data out of the cache directory: %v\n", err)
	}

	store, err := storage.Open(cfg.DBPath, cfg.TrashDir)
	if err != nil {
		fmt.Printf("failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := ui.Run(store, cfg, configPath, firstLaunch); err != nil {
		fmt.Printf("error running program: %v\n", err)
		os.Exit(1)
	}
}
