package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Groups    []string `json:"groups"` // Index 0 is Default/Inbox/All
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
	cursor      int // Task cursor (in the VISIBLE list)
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
	if index == 0 {
		return lipgloss.Color("250") // Light Gray for Default Group (Index 0)
	}
	if index < 0 {
		return lipgloss.Color("255") // Fallback White (shouldn't happen)
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
		if state.LastGroup == "" { // keep basic sanitization, though we ignore it for view mode
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

	// Fallback/Migration logic
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
		mode:        viewGroups, // FORCE START IN GROUP VIEW
		compactMode: false,
	}
}

func (m model) Init() tea.Cmd {
	return tea.EnterAltScreen
}

// --- HELPER: SORTING & FILTERING ---

// Returns a sorted list of INDICES into m.state.Todos
// Filtered by current group (or All)
// Sorted by Done status (Active first)
func (m model) getVisibleTasks() []int {
	var visible []int
	isAll := (m.state.LastGroup == m.state.Groups[0])

	for i, t := range m.state.Todos {
		if isAll || t.Group == m.state.LastGroup {
			visible = append(visible, i)
		}
	}

	// STABLE SORT: Active (!Done) before Done
	sort.SliceStable(visible, func(i, j int) bool {
		t1 := m.state.Todos[visible[i]]
		t2 := m.state.Todos[visible[j]]

		if t1.Done != t2.Done {
			return !t1.Done // If t1 is not done (true), it comes before t2 (false)
		}
		return false // Maintain original order otherwise
	})

	return visible
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
							if m.state.LastGroup == oldName {
								m.state.LastGroup = val
							}
							m.mode = viewGroups
						} else { // Task
							visible := m.getVisibleTasks()
							if m.cursor < len(visible) {
								realIdx := visible[m.cursor]
								m.state.Todos[realIdx].Title = val
							}
							m.mode = viewTasks
						}
					} else { // Adding
						// ADD LOGIC
						if m.mode == viewGroups {
							// Add Group
							m.state.Groups = append(m.state.Groups, val)
							m.state.LastGroup = val
							m.mode = viewTasks // Auto-enter new group? Sure.
							m.cursor = 0
						} else {
							// Add Task
							targetGroup := m.state.LastGroup
							// If in "All" view (Index 0), assign to "All" (Index 0)
							// Actually, LastGroup == m.state.Groups[0] already handles this.
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
				visibleCount := len(m.getVisibleTasks()) // This is slightly expensive to recalc, but safe for TUI sizes
				if m.cursor < visibleCount-1 {
					m.cursor++
				}
			}
		case "left", "h":
			m.mode = viewGroups
		case "right", "l":
			if m.mode == viewGroups {
				// Enter group
				m.state.LastGroup = m.state.Groups[m.groupCursor]
				m.mode = viewTasks
				m.cursor = 0
			}
			// In viewTasks, right does nothing or stays

		case " ":
			// Toggle Done
			if m.mode == viewTasks {
				visible := m.getVisibleTasks()
				if m.cursor < len(visible) {
					realIdx := visible[m.cursor]
					m.state.Todos[realIdx].Done = !m.state.Todos[realIdx].Done
					saveTodos(m.state)
				}
			}

		case "enter":
			if m.mode == viewGroups {
				m.state.LastGroup = m.state.Groups[m.groupCursor]
				m.mode = viewTasks
				m.cursor = 0
			} else {
				// ViewTasks: Toggle on Enter too?
				visible := m.getVisibleTasks()
				if m.cursor < len(visible) {
					realIdx := visible[m.cursor]
					m.state.Todos[realIdx].Done = !m.state.Todos[realIdx].Done
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
			if m.mode == viewGroups {
				m.renameType = 0
				m.input.SetValue(m.state.Groups[m.groupCursor])
				m.mode = renaming
			} else if m.mode == viewTasks {
				m.renameType = 1
				visible := m.getVisibleTasks()
				if m.cursor < len(visible) {
					realIdx := visible[m.cursor]
					m.input.SetValue(m.state.Todos[realIdx].Title)
					m.mode = renaming
				}
			}
			// Only focus if we actually entered renaming mode?
			// Check if we set mode to renaming?
			if m.mode == renaming {
				m.input.Focus()
				return m, textinput.Blink
			}

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
				// Task Deletion: Global Delete in "All" too
				visible := m.getVisibleTasks()
				if m.cursor < len(visible) {
					realIdx := visible[m.cursor]
					// Remove from slice
					m.state.Todos = append(m.state.Todos[:realIdx], m.state.Todos[realIdx+1:]...)

					// Cursor fix: if we delete the last item, move cursor up
					if m.cursor >= len(visible)-1 && m.cursor > 0 {
						m.cursor--
					}
					saveTodos(m.state)
				}
			}
		}
	}
	return m, cmd
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
				content = m.input.View() // Edit in place
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
		groupIndex := m.state.getGroupIndex(currentGroup)
		groupColor := getGroupColor(groupIndex)

		header := lipgloss.JoinHorizontal(lipgloss.Left,
			titleStyle.Render("Tasks"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(" > "),
			lipgloss.NewStyle().Foreground(groupColor).Bold(true).Render(currentGroup),
		)
		s.WriteString(header + "\n\n")

		// Render List (Using Sorted Helper)
		visibleIndices := m.getVisibleTasks()

		if len(visibleIndices) == 0 {
			s.WriteString(lipgloss.NewStyle().Faint(true).Render("  No tasks found.") + "\n")
		} else {
			for i, realIdx := range visibleIndices {
				t := m.state.Todos[realIdx]

				cursor := "  "
				// Check against View List Cursor
				if m.cursor == i && !m.adding {
					cursor = "> "
				}

				// Style
				tStyle := lipgloss.NewStyle()
				if t.Done {
					tStyle = tStyle.Strikethrough(true).Faint(true).Foreground(lipgloss.Color("240"))
				} else {
					tStyle = tStyle.Foreground(lipgloss.Color("255"))
				}

				// Active Item Highlight
				if m.cursor == i && !m.adding {
					tStyle = tStyle.Foreground(groupColor)
					cursor = lipgloss.NewStyle().Foreground(groupColor).Render(cursor)
				}

				// Rename Input Overlay
				title := t.Title
				if m.mode == renaming && m.renameType == 1 && m.cursor == i {
					title = m.input.View()
				}

				// Group Tag (Only in "All" view) -> [Group] Title
				var prefix string
				if isAll {
					tIdx := m.state.getGroupIndex(t.Group)
					c := getGroupColor(tIdx)
					prefix = lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("[%s] ", t.Group))
				}

				s.WriteString(fmt.Sprintf("%s%s%s\n", cursor, prefix, tStyle.Render(title)))
			}
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
