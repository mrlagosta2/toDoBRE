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
	Group string `json:"group"` // New field
}

type AppState struct {
	Todos     []Todo   `json:"todos"`
	Groups    []string `json:"groups"`
	LastGroup string   `json:"last_group"`
}

type model struct {
	state       AppState
	cursor      int // Task cursor
	groupCursor int // Group cursor
	input       textinput.Model
	adding      bool // adding a task
	addingGroup bool // adding a group
	quitting    bool
	viewMode    int // 0: Groups, 1: Tasks
	err         error
	width       int
	height      int
}

const (
	ViewGroups = iota
	ViewTasks
)

// Styling
var (
	// Compact Mode Styles (Raw) - Kept for legacy/simplicity if needed, but we focus on Groups now
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

	groupSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("57")).
				Padding(0, 1).
				Bold(true)
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

func loadTodos() AppState {
	defaultState := AppState{
		Todos:     []Todo{},
		Groups:    []string{}, // "ALL" is implicit
		LastGroup: "ALL",
	}

	path, err := getTodoPath()
	if err != nil {
		return defaultState
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return defaultState
	}

	// Try unmarshalling as AppState first
	var state AppState
	if err := json.Unmarshal(data, &state); err == nil {
		// Basic validation: if it has Groups or LastGroup or Todos (and not just empty), accept it.
		// Use a heuristic: if strict JSON unmarshal worked, it might be the new format.
		// However, old format []Todo might technically unmarshal into empty AppState if fields mismatch?
		// Actually json.Unmarshal ignores unknown fields.
		// Let's try unmarshalling into a generic map to check structure or just try []Todo if State looks empty.
		// Better approach: Try []Todo first (migration), if that fails (structure mismatch), assume it's AppState?
		// Or try AppState, check if LastGroup is set (it should be if saved correctly).

		// Let's rely on checking if it unmarshals successfully as []Todo.
		var oldTodos []Todo
		if errOld := json.Unmarshal(data, &oldTodos); errOld == nil {
			// It MIGHT be the old format.
			// But wait, AppState is an object {}, []Todo is a list [].
			// json.Unmarshal will fail if we try to unmarshal a list into a struct.
			// So relying on error is safe!
		}
	}

	// Correct Logic:
	// 1. Try to unmarshal as []Todo (Old Format - Array)
	var oldTodos []Todo
	if err := json.Unmarshal(data, &oldTodos); err == nil {
		// It IS a list. valid old format. Migrate.
		// Assign all existing todos to "General"
		for i := range oldTodos {
			oldTodos[i].Group = "General"
		}
		// Return new state
		return AppState{
			Todos:     oldTodos,
			Groups:    []string{"General"},
			LastGroup: "General",
		}
	}

	// 2. Try AppState (New Format - Object)
	if err := json.Unmarshal(data, &state); err == nil {
		if state.LastGroup == "" {
			state.LastGroup = "ALL"
		}
		return state
	}

	return defaultState
}

func saveTodos(state AppState) {
	path, err := getTodoPath()
	if err != nil {
		return
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

// Generate a consistent color for a group name
func getGroupColor(name string) lipgloss.Color {
	colors := []string{
		"#FFB6C1", // LightPink
		"#87CEFA", // LightSkyBlue
		"#90EE90", // LightGreen
		"#FFD700", // Gold
		"#FFA07A", // LightSalmon
		"#DDA0DD", // Plum
		"#F0E68C", // Khaki
		"#00CED1", // DarkTurquoise
	}
	hash := 0
	for _, c := range name {
		hash = int(c) + ((hash << 5) - hash)
	}
	if hash < 0 {
		hash = -hash
	}
	return lipgloss.Color(colors[hash%len(colors)])
}

func initialModel() model {
	state := loadTodos()

	ti := textinput.New()
	ti.Placeholder = "New task..."
	ti.Focus()
	ti.Prompt = ""

	m := model{
		state:    state,
		input:    ti,
		adding:   false,
		viewMode: ViewTasks, // Default start
	}

	// Handle Auto-Resume logic
	if state.LastGroup == "ALL" {
		m.viewMode = ViewTasks // redundant but explicit
		// Filter logic happens in View/Update, but we need to set state so we know what we are viewing.
		// Actually LastGroup is already in m.state.
	} else {
		// Find if the group still exists
		exists := false
		for _, g := range state.Groups {
			if g == state.LastGroup {
				exists = true
				break
			}
		}
		if !exists {
			m.state.LastGroup = "ALL"
		}
	}

	return m
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
		// HANDLE INPUT MODES
		if m.adding || m.addingGroup {
			switch msg.Type {
			case tea.KeyEnter:
				val := m.input.Value()
				if val != "" {
					if m.addingGroup {
						// Add Group
						m.state.Groups = append(m.state.Groups, val)
						m.state.LastGroup = val
						m.viewMode = ViewTasks
						m.addingGroup = false
					} else {
						// Add Task
						group := m.state.LastGroup
						if group == "ALL" {
							group = "General"
						}
						m.state.Todos = append(m.state.Todos, Todo{Title: val, Done: false, Group: group})
						// Move cursor to bottom of list? Or stay. let's stay logic or set to end.
						// We need to know where it appears in the filtered list.
						// Simple: Reset cursor or don't worry for now.
						m.adding = false
					}
					saveTodos(m.state)
					m.input.Reset()
				} else {
					m.adding = false
					m.addingGroup = false
				}
				return m, nil
			case tea.KeyEsc:
				m.adding = false
				m.addingGroup = false
				m.input.Reset()
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// HANDLE NAVIGATION KEYS
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "tab":
			// Cycle views? Or just use h/l
			if m.viewMode == ViewGroups {
				m.viewMode = ViewTasks
			} else {
				m.viewMode = ViewGroups
			}

		// Navigation
		case "up", "k":
			if m.viewMode == ViewGroups {
				if m.groupCursor > 0 {
					m.groupCursor--
				}
			} else {
				if m.cursor > 0 {
					m.cursor--
				}
			}
		case "down", "j":
			if m.viewMode == ViewGroups {
				// Groups list is Groups + "ALL"
				if m.groupCursor < len(m.state.Groups) { // +1 for ALL, so max index is len
					m.groupCursor++
				}
			} else {
				// We need filtered count to cap cursor
				filteredCount := 0
				for _, t := range m.state.Todos {
					if m.state.LastGroup == "ALL" || t.Group == m.state.LastGroup {
						filteredCount++
					}
				}
				if m.cursor < filteredCount-1 {
					m.cursor++
				}
			}

		// Mode Switching
		case "left", "h":
			if m.viewMode == ViewTasks {
				m.viewMode = ViewGroups
			}
		case "right", "l":
			if m.viewMode == ViewGroups {
				// Select the group at cursor
				m.selectGroupAtCursor()
				m.viewMode = ViewTasks
				m.cursor = 0 // Reset task cursor
				saveTodos(m.state)
			} else {
				// In Task view, l could toggle? Or nothing.
			}

		case "enter":
			if m.viewMode == ViewGroups {
				m.selectGroupAtCursor()
				m.viewMode = ViewTasks
				m.cursor = 0
				saveTodos(m.state)
			} else {
				// Toggle Task Done
				m.toggleTaskAtCursor()
				saveTodos(m.state)
			}

		case "n":
			if m.viewMode == ViewGroups {
				m.addingGroup = true
				m.input.Placeholder = "New Group Name..."
			} else {
				m.adding = true
				m.input.Placeholder = "New Task..."
			}
			m.input.Focus()
			return m, textinput.Blink

		case "x":
			if m.viewMode == ViewTasks {
				m.deleteTaskAtCursor()
				saveTodos(m.state)
			}
		}
	}
	return m, cmd
}

// Helpers/Actions
func (m *model) selectGroupAtCursor() {
	// Logic: 0 is ALL. 1..N are Groups[0..N-1]
	if m.groupCursor == 0 {
		m.state.LastGroup = "ALL"
	} else {
		idx := m.groupCursor - 1
		if idx >= 0 && idx < len(m.state.Groups) {
			m.state.LastGroup = m.state.Groups[idx]
		}
	}
}

func (m *model) toggleTaskAtCursor() {
	filteredIdx := 0
	targetIdx := -1

	for i, t := range m.state.Todos {
		if m.state.LastGroup == "ALL" || t.Group == m.state.LastGroup {
			if filteredIdx == m.cursor {
				targetIdx = i
				break
			}
			filteredIdx++
		}
	}

	if targetIdx != -1 {
		m.state.Todos[targetIdx].Done = !m.state.Todos[targetIdx].Done
	}
}

func (m *model) deleteTaskAtCursor() {
	filteredIdx := 0
	targetIdx := -1

	for i, t := range m.state.Todos {
		if m.state.LastGroup == "ALL" || t.Group == m.state.LastGroup {
			if filteredIdx == m.cursor {
				targetIdx = i
				break
			}
			filteredIdx++
		}
	}

	if targetIdx != -1 {
		m.state.Todos = append(m.state.Todos[:targetIdx], m.state.Todos[targetIdx+1:]...)
		// Adjust cursor if needed
		if m.cursor > 0 {
			m.cursor--
		}
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder

	if m.viewMode == ViewGroups {
		// --- GROUPS VIEW ---
		s.WriteString(titleStyle.Render("Groups"))
		s.WriteString("\n\n")

		// List "ALL" first
		cursor := "  "
		if m.groupCursor == 0 {
			cursor = "> "
		}

		itemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205")) // Default pinkish
		if m.groupCursor == 0 {
			itemStyle = groupSelectedStyle
		}

		line := fmt.Sprintf("%s%s", cursor, "ALL")
		s.WriteString(itemStyle.Render(line) + "\n")

		// List Custom Groups
		for i, g := range m.state.Groups {
			cursor = "  "
			if m.groupCursor == i+1 {
				cursor = "> "
			}

			// Dynamic Color
			color := getGroupColor(g)
			gStyle := lipgloss.NewStyle().Foreground(color)

			if m.groupCursor == i+1 {
				gStyle = groupSelectedStyle // Highlight selected row background
				// Maybe keep fg color?
				// gStyle = gStyle.Background(lipgloss.Color("57")).Foreground(color) // contrast issues?
				// Let's stick to simple selection style for now, or just colored text
				gStyle = lipgloss.NewStyle().Background(lipgloss.Color("57")).Foreground(color).Padding(0, 1).Bold(true)
			}

			line := fmt.Sprintf("%s%s", cursor, g)
			s.WriteString(gStyle.Render(line) + "\n")
		}

		s.WriteString("\n")
		if m.addingGroup {
			s.WriteString(fmt.Sprintf("New Group: %s", m.input.View()))
		} else {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Press 'n' to add group • 'l'/'enter' to select"))
		}

	} else {
		// --- TASKS VIEW ---

		// Header: My Tasks > [Group]
		groupName := m.state.LastGroup
		groupColor := getGroupColor(groupName)
		if groupName == "ALL" {
			groupColor = lipgloss.Color("205")
		}

		header := lipgloss.JoinHorizontal(lipgloss.Left,
			titleStyle.Render("My Tasks"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(" > "),
			lipgloss.NewStyle().Foreground(groupColor).Bold(true).Render(groupName),
		)
		s.WriteString(header + "\n\n")

		// Filter Tasks & Render
		filteredIdx := 0
		hasTasks := false

		for _, t := range m.state.Todos {
			if m.state.LastGroup != "ALL" && t.Group != m.state.LastGroup {
				continue
			}
			hasTasks = true

			cursor := "  "
			if m.cursor == filteredIdx && !m.adding {
				cursor = "> "
			}

			// Task Title
			title := t.Title
			tStyle := lipgloss.NewStyle()

			if t.Done {
				tStyle = tStyle.Strikethrough(true).Foreground(lipgloss.Color("240"))
			} else {
				tStyle = tStyle.Foreground(lipgloss.Color("255"))
			}

			if m.cursor == filteredIdx && !m.adding {
				tStyle = tStyle.Foreground(lipgloss.Color("205")) // Selection Color
				cursor = cursorStyle.Render(cursor)
			}

			// Render Line
			// If ALL view, show group tag
			var groupTag string
			if m.state.LastGroup == "ALL" {
				c := getGroupColor(t.Group)
				groupTag = lipgloss.NewStyle().Foreground(c).Faint(true).Render(fmt.Sprintf(" [%s]", t.Group))
			}

			line := fmt.Sprintf("%s%s%s", cursor, tStyle.Render(title), groupTag)
			s.WriteString(line + "\n")

			filteredIdx++
		}

		if !hasTasks {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  No tasks here."))
			s.WriteString("\n")
		}

		s.WriteString("\n")
		if m.adding {
			s.WriteString(fmt.Sprintf("  %s", m.input.View()))
		} else {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Press 'n' to add • 'x' to delete • 'h' for groups"))
		}
	}

	// Window styling container logic
	// We can wrap the whole thing in appStyle
	content := s.String()
	window := appStyle.Render(content)

	// Center
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, window)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
