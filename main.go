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

// --- DATA STRUCTURES ---

type Todo struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
	Group string `json:"group"`
}

type AppState struct {
	Todos     []Todo   `json:"todos"`
	Groups    []string `json:"groups"` // Index 0 is Default/Inbox
	LastGroup string   `json:"last_group"`
}

// --- STATE MANAGEMENT ---

type sessionState int

const (
	viewGroups sessionState = iota
	viewTasks
	renaming
	deletingGroup
)

type model struct {
	state       AppState
	cursor      int // Task cursor
	groupCursor int // Group cursor
	input       textinput.Model

	mode        sessionState
	compactMode bool

	// Input/Confirmation State
	adding     bool
	renameType int // 0: Group, 1: Task

	quitting bool
	err      error
	width    int
	height   int
}

// --- STYLING & COLORS ---

var (
	// Expanded Palette (20+ colors)
	palette = []lipgloss.Color{
		lipgloss.Color("#FFB6C1"), // LightPink
		lipgloss.Color("#87CEFA"), // LightSkyBlue
		lipgloss.Color("#90EE90"), // LightGreen
		lipgloss.Color("#FFD700"), // Gold
		lipgloss.Color("#FFA07A"), // LightSalmon
		lipgloss.Color("#DDA0DD"), // Plum
		lipgloss.Color("#F0E68C"), // Khaki
		lipgloss.Color("#00CED1"), // DarkTurquoise
		lipgloss.Color("#FF69B4"), // HotPink
		lipgloss.Color("#6495ED"), // CornflowerBlue
		lipgloss.Color("#32CD32"), // LimeGreen
		lipgloss.Color("#FF4500"), // OrangeRed
		lipgloss.Color("#BA55D3"), // MediumOrchid
		lipgloss.Color("#00FA9A"), // MediumSpringGreen
		lipgloss.Color("#4169E1"), // RoyalBlue
		lipgloss.Color("#DC143C"), // Crimson
		lipgloss.Color("#00BFFF"), // DeepSkyBlue
		lipgloss.Color("#9370DB"), // MediumPurple
		lipgloss.Color("#3CB371"), // MediumSeaGreen
		lipgloss.Color("#FF6347"), // Tomato
	}

	appStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62"))

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62")).
			Padding(0, 1).
			Bold(true)
)

// Get deterministic color for a group based on its index
func getGroupColor(index int) lipgloss.Color {
	if index < 0 {
		return lipgloss.Color("255") // Fallback White
	}
	return palette[index%len(palette)]
}

// Find group index by name (helper for coloring)
func (s AppState) getGroupIndex(name string) int {
	for i, g := range s.Groups {
		if g == name {
			return i
		}
	}
	return 0 // Default to 0 if not found
}

// --- PERSISTENCE ---

func getTodoPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	todoDir := filepath.Join(configDir, "todotui")
	if err := os.MkdirAll(todoDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(todoDir, "todos.json"), nil
}

func loadTodos() AppState {
	defaultState := AppState{
		Todos:     []Todo{},
		Groups:    []string{"All"}, // Rename "General" to "All" or user preference. Index 0 is Aggregator.
		LastGroup: "All",
	}

	path, err := getTodoPath()
	if err != nil {
		return defaultState
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return defaultState
	}

	var state AppState
	// Try AppState (New Format)
	if err := json.Unmarshal(data, &state); err == nil {
		if len(state.Groups) == 0 {
			state.Groups = []string{"All"}
		}
		if state.LastGroup == "" {
			state.LastGroup = state.Groups[0]
		}
		// Ensure tasks map to valid groups (or default to Index 0)
		for i := range state.Todos {
			if state.Todos[i].Group == "" {
				state.Todos[i].Group = state.Groups[0]
			}
		}
		return state
	}

	// Fallback/Migration would go here, but omitted for brevity as we are rewriting.
	// Assuming Migration done or fresh start for this specific request context.
	// Actually, let's keep basic migration just in case.
	var oldTodos []Todo
	if err := json.Unmarshal(data, &oldTodos); err == nil {
		for i := range oldTodos {
			oldTodos[i].Group = "All"
		}
		return AppState{
			Todos:     oldTodos,
			Groups:    []string{"All"},
			LastGroup: "All",
		}
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

// --- INIT ---

func initialModel() model {
	state := loadTodos()
	ti := textinput.New()
	ti.Placeholder = "New task..."
	ti.Focus()
	ti.Prompt = ""

	return model{
		state:       state,
		input:       ti,
		mode:        viewTasks,
		compactMode: false,
	}
}

func (m model) Init() tea.Cmd {
	return tea.EnterAltScreen
}

// --- UPDATE ---

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
		// 1. INPUT HANDLING (Adding / Renaming)
		if m.adding || m.mode == renaming {
			switch msg.Type {
			case tea.KeyEnter:
				val := m.input.Value()
				if val != "" {
					if m.mode == renaming {
						// RENAME LOGIC
						if m.renameType == 0 { // Group
							oldName := m.state.Groups[m.groupCursor]
							m.state.Groups[m.groupCursor] = val
							// Update Tasks
							for i := range m.state.Todos {
								if m.state.Todos[i].Group == oldName {
									m.state.Todos[i].Group = val
								}
							}
							// Update LastGroup
							if m.state.LastGroup == oldName {
								m.state.LastGroup = val
							}
							m.mode = viewGroups
						} else { // Task
							// Rename currently selected task
							// We need to find it based on current View logic
							targetIdx := m.getTaskIndexAtCursor()
							if targetIdx != -1 {
								m.state.Todos[targetIdx].Title = val
							}
							m.mode = viewTasks
						}
					} else { // Adding
						// ADD LOGIC
						if m.mode == viewGroups {
							// Add Group
							m.state.Groups = append(m.state.Groups, val)
							m.state.LastGroup = val
							m.mode = viewTasks
						} else {
							// Add Task
							targetGroup := m.state.LastGroup

							// CRITICAL FIX: If viewing "All" (Index 0), assign to "All" (Index 0)
							// Actually, LastGroup keeps track of what we are viewing.
							// So if LastGroup == Groups[0], we assign to Groups[0].
							m.state.Todos = append(m.state.Todos, Todo{Title: val, Done: false, Group: targetGroup})
						}
						m.adding = false
					}
					saveTodos(m.state)
					m.input.Reset()
				} else {
					// Empty input cancels
					m.adding = false
					if m.mode == renaming {
						if m.renameType == 0 {
							m.mode = viewGroups
						} else {
							m.mode = viewTasks
						}
					}
				}
				return m, nil

			case tea.KeyEsc:
				m.adding = false
				m.input.Reset()
				if m.mode == renaming {
					if m.renameType == 0 {
						m.mode = viewGroups
					} else {
						m.mode = viewTasks
					}
				}
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// 2. CONFIRMATION (Deletion)
		if m.mode == deletingGroup {
			switch msg.String() {
			case "ctrl+x":
				targetGroup := m.state.Groups[m.groupCursor]
				// Cascade delete tasks
				newTodos := []Todo{}
				for _, t := range m.state.Todos {
					if t.Group != targetGroup {
						newTodos = append(newTodos, t)
					}
				}
				m.state.Todos = newTodos
				// Delete group
				m.state.Groups = append(m.state.Groups[:m.groupCursor], m.state.Groups[m.groupCursor+1:]...)
				// Reset navigation
				if m.groupCursor >= len(m.state.Groups) {
					m.groupCursor = len(m.state.Groups) - 1
				}
				if len(m.state.Groups) > 0 {
					m.state.LastGroup = m.state.Groups[0]
				}
				m.mode = viewGroups
				saveTodos(m.state)
				return m, nil
			case "n", "esc":
				m.mode = viewGroups
				return m, nil
			}
			return m, nil
		}

		// 3. NAVIGATION & COMMANDS
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

		// ARROWS / VI KEYS
		case "up", "k":
			if m.mode == viewGroups {
				if m.groupCursor > 0 {
					m.groupCursor--
				}
			} else {
				if m.cursor > 0 {
					m.cursor--
				}
			}
		case "down", "j":
			if m.mode == viewGroups {
				if m.groupCursor < len(m.state.Groups)-1 {
					m.groupCursor++
				}
			} else {
				// Cap cursor at filtered list length
				count := m.getFilteredTaskCount()
				if m.cursor < count-1 {
					m.cursor++
				}
			}
		case "left", "h":
			m.mode = viewGroups
		case "right", "l":
			// Enter group
			m.state.LastGroup = m.state.Groups[m.groupCursor]
			m.mode = viewTasks
			m.cursor = 0 // Reset cursor when entering

		case " ":
			// Toggle Done
			if m.mode == viewTasks {
				targetIdx := m.getTaskIndexAtCursor()
				if targetIdx != -1 {
					m.state.Todos[targetIdx].Done = !m.state.Todos[targetIdx].Done
					saveTodos(m.state)
				}
			}

		case "enter":
			if m.mode == viewGroups {
				m.state.LastGroup = m.state.Groups[m.groupCursor]
				m.mode = viewTasks
				m.cursor = 0
			} else {
				// Toggle Done on Enter too? Or maybe Edit?
				// Standard is usually toggle or edit. Let's keep toggle for now.
				targetIdx := m.getTaskIndexAtCursor()
				if targetIdx != -1 {
					m.state.Todos[targetIdx].Done = !m.state.Todos[targetIdx].Done
					saveTodos(m.state)
				}
			}

		case "n":
			m.adding = true
			m.input.Placeholder = "New..."
			if m.mode == viewGroups {
				m.input.Placeholder = "New Group..."
			}
			m.input.Focus()
			return m, textinput.Blink

		case "r":
			m.mode = renaming
			if m.mode == viewGroups {
				m.renameType = 0
				m.input.SetValue(m.state.Groups[m.groupCursor])
			} else {
				m.renameType = 1
				targetIdx := m.getTaskIndexAtCursor()
				if targetIdx != -1 {
					m.input.SetValue(m.state.Todos[targetIdx].Title)
				}
			}
			m.input.Focus()
			return m, textinput.Blink

		case "x":
			if m.mode == viewGroups {
				// Group Deletion
				if m.groupCursor == 0 {
					return m, nil
				} // Protected Index 0

				// Check for content
				hasTasks := false
				target := m.state.Groups[m.groupCursor]
				for _, t := range m.state.Todos {
					if t.Group == target {
						hasTasks = true
						break
					}
				}
				if hasTasks {
					m.mode = deletingGroup
				} else {
					// Empty? Delete now
					m.state.Groups = append(m.state.Groups[:m.groupCursor], m.state.Groups[m.groupCursor+1:]...)
					if m.groupCursor >= len(m.state.Groups) {
						m.groupCursor = len(m.state.Groups) - 1
					}
					saveTodos(m.state)
				}
			} else {
				// Task Deletion: Global Delete
				targetIdx := m.getTaskIndexAtCursor()
				if targetIdx != -1 {
					m.state.Todos = append(m.state.Todos[:targetIdx], m.state.Todos[targetIdx+1:]...)
					if m.cursor > 0 {
						m.cursor--
					}
					saveTodos(m.state)
				}
			}
		}
	}
	return m, cmd
}

// --- HELPERS ---

// Calculates how many tasks are visible in the current view
func (m model) getFilteredTaskCount() int {
	isAll := (m.state.LastGroup == m.state.Groups[0])
	if isAll {
		return len(m.state.Todos)
	}
	count := 0
	for _, t := range m.state.Todos {
		if t.Group == m.state.LastGroup {
			count++
		}
	}
	return count
}

// Gets the actual index in m.state.Todos for the item under the cursor in the filtered view
func (m model) getTaskIndexAtCursor() int {
	isAll := (m.state.LastGroup == m.state.Groups[0])

	// If viewing "All", it's straight 1:1 map?
	// YES. The aggregation view shows EVERY task.
	// We just need to ensure m.cursor is valid.
	if isAll {
		if m.cursor >= 0 && m.cursor < len(m.state.Todos) {
			return m.cursor
		}
		return -1
	}

	// Filtered view
	filteredIdx := 0
	for i, t := range m.state.Todos {
		if t.Group == m.state.LastGroup {
			if filteredIdx == m.cursor {
				return i
			}
			filteredIdx++
		}
	}
	return -1
}

// --- VIEW ---

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder

	// GROUP VIEW / DELETING / RENAMING (If Group)
	if m.mode == viewGroups || (m.mode == renaming && m.renameType == 0) || m.mode == deletingGroup {
		s.WriteString(titleStyle.Render("Groups") + "\n\n")

		for i, g := range m.state.Groups {
			cursor := "  "
			if m.groupCursor == i {
				cursor = "> "
			}

			// Color logic
			color := getGroupColor(i)
			style := lipgloss.NewStyle().Foreground(color)

			if m.groupCursor == i {
				style = lipgloss.NewStyle().Background(lipgloss.Color("57")).Foreground(color).Bold(true).Padding(0, 1)
			}

			// Render content
			content := g
			if m.mode == renaming && m.groupCursor == i {
				content = m.input.View()
			}

			// Delete Confirm
			if m.mode == deletingGroup && m.groupCursor == i {
				content += " [DELETE? Ctrl+x / n]"
				style = style.Foreground(lipgloss.Color("196"))
			}

			s.WriteString(fmt.Sprintf("%s%s\n", cursor, style.Render(content)))
		}

		s.WriteString("\n")
		// Footer for Groups
		if m.adding {
			s.WriteString("New Group: " + m.input.View())
		} else {
			s.WriteString(lipgloss.NewStyle().Faint(true).Render("Tab: Toggle View • ←/→: Nav • n: Add • r: Rename • x: Delete"))
		}

	} else {
		// TASK VIEW
		currentGroup := m.state.LastGroup
		isAll := (currentGroup == m.state.Groups[0])

		// Header
		// Use color of the current group
		groupIndex := m.state.getGroupIndex(currentGroup)
		groupColor := getGroupColor(groupIndex)

		header := lipgloss.JoinHorizontal(lipgloss.Left,
			titleStyle.Render("Tasks"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(" > "),
			lipgloss.NewStyle().Foreground(groupColor).Bold(true).Render(currentGroup),
		)
		s.WriteString(header + "\n\n")

		// Render List
		filteredIdx := 0
		hasTasks := false

		// We iterate ALL tasks.
		// If "All" view (Index 0): Show Everything.
		// Else: Show only matching.
		for _, t := range m.state.Todos {
			if !isAll && t.Group != currentGroup {
				continue
			}
			hasTasks = true

			cursor := "  "
			if m.cursor == filteredIdx && !m.adding {
				cursor = "> "
			}

			// Determine display style
			tStyle := lipgloss.NewStyle()
			if t.Done {
				tStyle = tStyle.Strikethrough(true).Faint(true)
			} else {
				tStyle = tStyle.Foreground(lipgloss.Color("255"))
			}

			// Active Item Highlight
			if m.cursor == filteredIdx && !m.adding {
				tStyle = tStyle.Foreground(groupColor)
				cursor = lipgloss.NewStyle().Foreground(groupColor).Render(cursor)
			}

			// Rename Input Overlay
			title := t.Title
			if m.mode == renaming && m.renameType == 1 && m.cursor == filteredIdx {
				title = m.input.View()
			}

			// Group Tag (Only in "All" view)
			var suffix string
			if isAll {
				// Find group color for this task
				tIdx := m.state.getGroupIndex(t.Group)
				c := getGroupColor(tIdx)
				suffix = lipgloss.NewStyle().Foreground(c).Faint(true).Render(fmt.Sprintf(" [%s]", t.Group))
			}

			s.WriteString(fmt.Sprintf("%s%s%s\n", cursor, tStyle.Render(title), suffix))

			filteredIdx++
		}

		if !hasTasks {
			s.WriteString(lipgloss.NewStyle().Faint(true).Render("  No tasks found.") + "\n")
		}

		s.WriteString("\n")
		// Footer for Tasks
		if m.adding {
			s.WriteString("  " + m.input.View())
		} else {
			s.WriteString(lipgloss.NewStyle().Faint(true).Render("Tab: Toggle View • ←/→: Nav • n: Add • Space: Done • x: Delete"))
		}
	}

	content := s.String()

	// Full vs Compact
	if !m.compactMode {
		// Border matches current group color logic IF in task view, else Default
		borderColor := lipgloss.Color("62")
		if m.mode == viewTasks {
			idx := m.state.getGroupIndex(m.state.LastGroup)
			borderColor = getGroupColor(idx)
		}

		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			appStyle.Copy().BorderForeground(borderColor).Render(content))
	}
	return content
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
