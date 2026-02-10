package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Todo struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type model struct {
	todos       []Todo
	cursor      int
	input       textinput.Model
	adding      bool
	quitting    bool
	compactMode bool
	err         error
	width       int
	height      int
}

// Styling
var (
	// Compact Mode Styles (Raw)
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	// Full Mode Styles
	appStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62"))

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62")).
			Padding(0, 1).
			Bold(true)

	listHeaderStyle = lipgloss.NewStyle().
			MarginLeft(2)
)

func getTodoPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	todoDir := filepath.Join(configDir, "todotui")
	// Ensure directory exists
	if err := os.MkdirAll(todoDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(todoDir, "todos.json"), nil
}

func loadTodos() []Todo {
	var todos []Todo

	path, err := getTodoPath()
	if err != nil {
		return []Todo{}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Todo{}
		}
		return []Todo{}
	}
	if err := json.Unmarshal(data, &todos); err != nil {
		return []Todo{}
	}
	return todos
}

func saveTodos(todos []Todo) {
	path, err := getTodoPath()
	if err != nil {
		return
	}

	data, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func initialModel() model {
	todos := loadTodos()
	ti := textinput.New()
	ti.Placeholder = "Task name..."
	ti.Focus()
	ti.Prompt = ""

	return model{
		todos:       todos,
		cursor:      0,
		input:       ti,
		adding:      false,
		compactMode: false, // Default to Full Mode
	}
}

func (m model) Init() tea.Cmd {
	return tea.EnterAltScreen
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, tea.Quit
	}

	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		if m.adding {
			switch msg.Type {
			case tea.KeyEnter:
				if m.input.Value() != "" {
					m.todos = append(m.todos, Todo{Title: m.input.Value(), Done: false})
					saveTodos(m.todos)
					m.input.Reset()
					m.adding = false
					m.cursor = len(m.todos) - 1
				} else {
					m.adding = false
				}
				return m, nil
			case tea.KeyEsc:
				m.adding = false
				m.input.Reset()
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		} else {
			// Navigation Mode
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "tab":
				m.compactMode = !m.compactMode
				if m.compactMode {
					return m, tea.ExitAltScreen
				}
				return m, tea.EnterAltScreen
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.todos)-1 {
					m.cursor++
				}
			case "n":
				m.adding = true
				m.input.Focus()
				return m, textinput.Blink
			case "x":
				if len(m.todos) > 0 {
					m.todos = append(m.todos[:m.cursor], m.todos[m.cursor+1:]...)
					if m.cursor >= len(m.todos) && m.cursor > 0 {
						m.cursor--
					}
					saveTodos(m.todos)
				}
			case "space", "enter":
				// Optional: Toggle Done
				// if len(m.todos) > 0 {
				// 	m.todos[m.cursor].Done = !m.todos[m.cursor].Done
				// 	saveTodos(m.todos)
				// }
			}
		}
	}
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder

	// Render Todo List Content
	var listContent strings.Builder
	for i, t := range m.todos {
		cursor := "  "
		if m.cursor == i && !m.adding {
			cursor = "> "
			if m.compactMode {
				cursor = cursorStyle.Render(cursor)
			} else {
				// In Full Mode, we can use a different indicator or keeping it simple
				cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render("> ")
			}
		}

		title := t.Title
		if m.cursor == i && !m.adding {
			if m.compactMode {
				title = selectedStyle.Render(title)
			} else {
				title = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render(title)
			}
		}

		fmt.Fprintf(&listContent, "%s%s\n", cursor, title)
	}

	// Render Input or Footer
	var footerContent string
	if m.adding {
		footerContent = fmt.Sprintf("  %s", m.input.View())
	} else {
		if len(m.todos) == 0 {
			footerContent = "No tasks. Press 'n' to add."
		} else {
			if !m.compactMode {
				footerContent = "\nPress 'n' to add • 'x' to delete • 'q' to quit • 'Tab' to toggle view"
			}
		}
	}

	if m.compactMode {
		// Compact Mode: Raw rendering
		s.WriteString(listContent.String())
		s.WriteString(footerContent)
		if !m.adding && len(m.todos) > 0 {
			// Add a little hint about tab in compact mode if you want, but sticking to "raw"
		}
		s.WriteString("\n") // Ensure newline at end
		return s.String()
	}

	// Full Mode: Styled Container
	// Build the main view
	mainView := listContent.String() + "\n" + footerContent

	// Create a window-like feel
	// Calculate dynamic height/width if needed, or just let lipgloss handle it

	window := appStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("My Tasks"),
			"\n",
			mainView,
		),
	)

	// Center the window in the available space
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, window)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
