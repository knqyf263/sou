package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/knqyf263/sou/container"
	"github.com/knqyf263/sou/ui"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	version = "dev"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize slog
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("failed to get cache directory: %w", err)
	}

	// Create sou directory in cache
	souCacheDir := filepath.Join(cacheDir, "sou")
	if err := os.MkdirAll(souCacheDir, 0o755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	logFile, err := os.OpenFile(filepath.Join(souCacheDir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	// Configure slog to write to the file
	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "show version")
	flag.Parse()

	if showVersion {
		fmt.Printf("sou version %s\n", version)
		return nil
	}

	if flag.NArg() != 1 {
		return fmt.Errorf("usage: sou <image-name>")
	}

	// Setup signal handling for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Ensure cleanup on program exit
	defer cleanup()

	imageName := flag.Arg(0)

	// Create and run program with initial model
	model, cmd := ui.NewModel(imageName)
	p := tea.NewProgram(
		&model,
		tea.WithAltScreen(),
	)

	// Run the initial command
	if cmd != nil {
		go func() {
			p.Send(cmd())
		}()
	}

	// Handle signals
	go func() {
		<-sigChan
		cleanup()
		p.Kill()
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running program: %w", err)
	}

	return nil
}

func cleanup() {
	if err := container.CleanupCache(); err != nil {
		slog.Error("failed to clean up cache", "error", err)
	}
}
