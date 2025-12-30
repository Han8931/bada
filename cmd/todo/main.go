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
