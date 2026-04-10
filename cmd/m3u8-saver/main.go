package main

import (
	"context"
	"fmt"
	"os"

	"m3u8-saver/internal/app"
	"m3u8-saver/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: m3u8-saver <config.json>")
	}

	cfg, err := config.Load(os.Args[1])
	if err != nil {
		return err
	}

	application, err := app.New(cfg, os.Stdout)
	if err != nil {
		return err
	}

	return application.Run(context.Background())
}
