package main

import (
	"context"
	"fmt"
	"os"

	"m3u8-saver/internal/app"
	"m3u8-saver/internal/config"
	"m3u8-saver/internal/inventory"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 2 && isHelpArg(os.Args[1]) {
		printUsage(os.Stdout)
		return nil
	}

	if len(os.Args) == 3 && os.Args[1] == "inventory" {
		return inventory.Run(os.Args[2], os.Stdout)
	}

	if len(os.Args) != 2 {
		printUsage(os.Stderr)
		return fmt.Errorf("config file path is required")
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

func isHelpArg(value string) bool {
	return value == "-h" || value == "--help"
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  m3u8-saver <config.json>")
	fmt.Fprintln(output, "  m3u8-saver inventory <outDir>")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Options:")
	fmt.Fprintln(output, "  -h, --help   Show this help")
}
