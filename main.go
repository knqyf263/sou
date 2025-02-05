package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/knqyf263/sou/container"
	"github.com/knqyf263/sou/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Println("Usage: sou <image-name>")
		return
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
		fmt.Printf("Error running program: %v\n", err)
		return
	}
}

func cleanup() {
	if err := container.CleanupCache(); err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning up cache: %v\n", err)
	}
}
