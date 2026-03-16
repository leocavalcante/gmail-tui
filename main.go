package main

import (
	"log"

	"github.com/rdx40/gmail-tui/api"
	"github.com/rdx40/gmail-tui/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	client, err := api.NewClient()
	if err != nil {
		log.Fatalf("Failed to initialize Gmail client: %v", err)
	}

	p := tea.NewProgram(ui.New(client), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}
