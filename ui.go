package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type selectionModel struct {
	choices  []string
	cursor   int
	selected int
	done     bool
}

func (m selectionModel) Init() tea.Cmd {
	return nil
}

func (m selectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.selected = m.cursor
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectionModel) View() string {
	s := "Choose a codespace:\n\n"

	for i, choice := range m.choices {
		cursor := " "
		if m.cursor == i {
			cursor = ">"
		}
		s += fmt.Sprintf("%s %s\n", cursor, choice)
	}

	s += "\nPress q to quit.\n"
	return s
}

func showSelection(options []string) (int, error) {
	model := selectionModel{
		choices: options,
	}

	p := tea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return -1, fmt.Errorf("selection failed: %w", err)
	}

	result := finalModel.(selectionModel)
	if !result.done {
		return -1, fmt.Errorf("no selection made")
	}

	return result.selected, nil
}
