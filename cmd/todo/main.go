package main

import (
	"fmt"
	"os"

	"bada/internal/config"
	"bada/internal/storage"
	"bada/internal/ui"
)

func main() {
	cfg, err := config.LoadOrCreate(config.DefaultConfigFileName)
	if err != nil {
		fmt.Printf("failed to load config: %v\n", err)
		os.Exit(1)
	}

	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		fmt.Printf("failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := ui.Run(store, cfg); err != nil {
		fmt.Printf("error running program: %v\n", err)
		os.Exit(1)
	}
}
