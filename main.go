package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── DATA STRUCTURES ───────────────────────────────────────────────────────────

type Subtask struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type Todo struct {
	Title       string    `json:"title"`
	Done        bool      `json:"done"`
	Description string    `json:"description"`
	Subtasks    []Subtask `json:"subtasks"`
}

type GroupFile struct {
	Title string `json:"title"`
	Todos []Todo `json:"todos"`
}

// Old format for migration
type OldTodo struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
	Group string `json:"group"`
}
type OldAppState struct {
	Todos     []OldTodo `json:"todos"`
	Groups    []string  `json:"groups"`
	LastGroup string    `json:"last_group"`
}

// ─── STATE MACHINE ──────────────────────────────────────────────────────────────

type sessionState int

const (
	stateViewWorkspaces sessionState = iota
	stateViewGroups
	stateViewTasks
	stateTaskDetails
	stateGitConsole
)

type inputTarget int

const (
	inputNone inputTarget = iota
	inputAddWorkspace
	inputAddGroup
	inputAddTask
	inputAddSubtask
	inputRenameWorkspace
	inputRenameGroup
	inputRenameTask
	inputRenameTaskTitle
	inputEditDescription
)

// ─── MODEL ──────────────────────────────────────────────────────────────────────

type model struct {
	state     sessionState
	prevState sessionState // for git console return

	workspaces       []string
	workspaceCursor  int
	currentWorkspace string

	groups       []string
	groupCursor  int
	currentGroup string

	tasks      GroupFile
	taskCursor int

	subtaskCursor int

	// Git Console
	gitViewport viewport.Model
	gitInput    textinput.Model
	gitHistory  string

	// All view collapse
	collapsed    map[string]bool
	allViewItems []allViewEntry

	// Input
	input         textinput.Model
	inputMode     inputTarget
	adding        bool
	confirmDelete bool

	// Display
	compactMode bool
	quitting    bool
	width       int
	height      int
}

type allViewEntry struct {
	isHeader  bool
	groupName string
	taskIndex int
}

// ─── STYLING ────────────────────────────────────────────────────────────────────

var (
	palette = []lipgloss.Color{
		"#FFB6C1", "#87CEFA", "#90EE90", "#FFD700", "#FFA07A",
		"#DDA0DD", "#F0E68C", "#00CED1", "#FF69B4", "#6495ED",
		"#32CD32", "#FF4500", "#BA55D3", "#00FA9A", "#4169E1",
		"#DC143C", "#00BFFF", "#9370DB", "#3CB371", "#FF6347",
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

	faintStyle = lipgloss.NewStyle().Faint(true)
	boldStyle  = lipgloss.NewStyle().Bold(true)
)

func getColor(index int) lipgloss.Color {
	if index <= 0 {
		return lipgloss.Color("250")
	}
	return palette[(index-1)%len(palette)]
}

// ─── PERSISTENCE ────────────────────────────────────────────────────────────────

func getDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "todo_data"
	}
	return filepath.Join(filepath.Dir(exe), "todo_data")
}

func getBackupDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfg, "todotui", "backup")
}

func ensureDir(path string) {
	_ = os.MkdirAll(path, 0755)
}

func loadWorkspaces() []string {
	dir := getDataDir()
	ensureDir(dir)
	ensureDir(filepath.Join(dir, "HOME"))

	allPath := filepath.Join(dir, "HOME", "ALL.json")
	if _, err := os.Stat(allPath); os.IsNotExist(err) {
		g := GroupFile{Title: "ALL", Todos: []Todo{}}
		data, _ := json.MarshalIndent(g, "", "  ")
		_ = os.WriteFile(allPath, data, 0644)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"HOME"}
	}

	ws := []string{}
	hasHome := false
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			name := strings.ToUpper(e.Name())
			if name == "HOME" {
				hasHome = true
			}
			ws = append(ws, name)
		}
	}
	if !hasHome {
		ws = append([]string{"HOME"}, ws...)
	} else {
		sorted := []string{"HOME"}
		for _, w := range ws {
			if w != "HOME" {
				sorted = append(sorted, w)
			}
		}
		ws = sorted
	}
	return ws
}

func loadGroups(workspace string) []string {
	dir := filepath.Join(getDataDir(), workspace)
	ensureDir(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if workspace == "HOME" {
			return []string{"ALL"}
		}
		return []string{}
	}

	groups := []string{}
	hasAll := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if workspace == "HOME" && strings.ToUpper(name) == "ALL" {
				hasAll = true
				groups = append(groups, "ALL")
			} else {
				groups = append(groups, name)
			}
		}
	}

	if workspace == "HOME" && !hasAll {
		groups = append([]string{"ALL"}, groups...)
		g := GroupFile{Title: "ALL", Todos: []Todo{}}
		data, _ := json.MarshalIndent(g, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "ALL.json"), data, 0644)
	} else if workspace == "HOME" {
		sorted := []string{"ALL"}
		for _, g := range groups {
			if g != "ALL" {
				sorted = append(sorted, g)
			}
		}
		groups = sorted
	}
	return groups
}

func loadGroupFile(workspace, group string) GroupFile {
	path := filepath.Join(getDataDir(), workspace, group+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return GroupFile{Title: group, Todos: []Todo{}}
	}
	var gf GroupFile
	if err := json.Unmarshal(data, &gf); err != nil {
		return GroupFile{Title: group, Todos: []Todo{}}
	}
	return gf
}

func saveGroupFile(workspace, group string, gf GroupFile) {
	primary := filepath.Join(getDataDir(), workspace)
	ensureDir(primary)
	data, err := json.MarshalIndent(gf, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(primary, group+".json"), data, 0644)

	backup := getBackupDir()
	if backup != "" {
		bDir := filepath.Join(backup, workspace)
		ensureDir(bDir)
		_ = os.WriteFile(filepath.Join(bDir, group+".json"), data, 0644)
	}
}

func deleteGroupFile(workspace, group string) {
	_ = os.Remove(filepath.Join(getDataDir(), workspace, group+".json"))
	backup := getBackupDir()
	if backup != "" {
		_ = os.Remove(filepath.Join(backup, workspace, group+".json"))
	}
}

func createWorkspace(name string) {
	name = strings.ToUpper(name)
	ensureDir(filepath.Join(getDataDir(), name))
	backup := getBackupDir()
	if backup != "" {
		ensureDir(filepath.Join(backup, name))
	}
}

func deleteWorkspace(name string) {
	_ = os.RemoveAll(filepath.Join(getDataDir(), name))
	backup := getBackupDir()
	if backup != "" {
		_ = os.RemoveAll(filepath.Join(backup, name))
	}
}

func countGroupTasks(workspace, group string) (done, total int) {
	gf := loadGroupFile(workspace, group)
	total = len(gf.Todos)
	for _, t := range gf.Todos {
		if t.Done {
			done++
		}
	}
	return
}

func countWorkspaceTasks(workspace string) (done, total int) {
	groups := loadGroups(workspace)
	for _, g := range groups {
		d, t := countGroupTasks(workspace, g)
		done += d
		total += t
	}
	return
}

// ─── MIGRATION ──────────────────────────────────────────────────────────────────

func migrateOldData() {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return
	}
	oldPath := filepath.Join(cfg, "todotui", "todos.json")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return
	}

	var oldState OldAppState
	if err := json.Unmarshal(data, &oldState); err != nil {
		return
	}
	if len(oldState.Todos) == 0 && len(oldState.Groups) == 0 {
		return
	}

	grouped := map[string][]Todo{}
	for _, ot := range oldState.Todos {
		g := ot.Group
		if g == "" || g == "All" {
			g = "ALL"
		}
		grouped[g] = append(grouped[g], Todo{
			Title: ot.Title,
			Done:  ot.Done,
		})
	}

	for _, gName := range oldState.Groups {
		if gName == "" || gName == "All" {
			gName = "ALL"
		}
		if _, ok := grouped[gName]; !ok {
			grouped[gName] = []Todo{}
		}
	}

	for gName, todos := range grouped {
		gf := GroupFile{Title: gName, Todos: todos}
		saveGroupFile("HOME", gName, gf)
	}

	_ = os.Rename(oldPath, oldPath+".migrated")
}

// ─── GIT HELPERS ────────────────────────────────────────────────────────────────

func runGitCmd(args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = getDataDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out) + "\n" + err.Error()
	}
	return string(out)
}

// ─── INIT ───────────────────────────────────────────────────────────────────────

func initialModel() model {
	migrateOldData()

	ti := textinput.New()
	ti.Placeholder = "..."
	ti.Focus()
	ti.Prompt = ""

	gi := textinput.New()
	gi.Placeholder = "command..."
	gi.Prompt = "git > "
	gi.Focus()
	gi.CharLimit = 256

	vp := viewport.New(70, 10)
	vp.SetContent("")

	ws := loadWorkspaces()

	return model{
		state:            stateViewWorkspaces,
		workspaces:       ws,
		currentWorkspace: "HOME",
		input:            ti,
		gitInput:         gi,
		gitViewport:      vp,
		collapsed:        make(map[string]bool),
	}
}

func (m model) Init() tea.Cmd {
	return tea.EnterAltScreen
}

// ─── ALL VIEW HELPERS ───────────────────────────────────────────────────────────

func (m *model) buildAllViewItems() {
	m.allViewItems = nil
	groups := loadGroups(m.currentWorkspace)
	for _, gName := range groups {
		if gName == "ALL" {
			continue
		}
		gf := loadGroupFile(m.currentWorkspace, gName)
		m.allViewItems = append(m.allViewItems, allViewEntry{isHeader: true, groupName: gName})
		if !m.collapsed[gName] {
			for i := range gf.Todos {
				m.allViewItems = append(m.allViewItems, allViewEntry{groupName: gName, taskIndex: i})
			}
		}
	}
}

// ─── UPDATE ─────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, tea.Quit
	}
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Resize git viewport
		vpW := clamp(m.width-10, 40, 70)
		vpH := clamp(m.height-12, 5, 40)
		m.gitViewport.Width = vpW
		m.gitViewport.Height = vpH
		return m, nil

	case tea.KeyMsg:
		// ── INPUT MODE ──
		if m.inputMode != inputNone {
			return m.handleInput(msg)
		}
		// ── CONFIRM DELETE ──
		if m.confirmDelete {
			return m.handleConfirmDelete(msg)
		}
		// ── GIT CONSOLE ──
		if m.state == stateGitConsole {
			return m.handleGitConsole(msg)
		}
		// ── TASK DETAILS ──
		if m.state == stateTaskDetails {
			return m.handleTaskDetails(msg)
		}
		// ── GLOBAL KEYS ──
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "g":
			m.prevState = m.state
			m.state = stateGitConsole
			m.gitHistory = ""
			m.gitViewport.SetContent(lipgloss.NewStyle().Faint(true).Render("  Type a git command and press Enter. (e.g. status, log --oneline -5)"))
			m.gitViewport.GotoBottom()
			m.gitInput.Reset()
			m.gitInput.Focus()
			return m, textinput.Blink
		case "tab":
			m.compactMode = !m.compactMode
			if m.compactMode {
				return m, tea.ExitAltScreen
			}
			return m, tea.EnterAltScreen
		}
		// ── STATE-SPECIFIC ──
		switch m.state {
		case stateViewWorkspaces:
			return m.handleWorkspaces(msg)
		case stateViewGroups:
			return m.handleGroups(msg)
		case stateViewTasks:
			return m.handleTasks(msg)
		}
	}
	return m, cmd
}

// ── INPUT HANDLER ───────────────────────────────────────────────────────────────

func (m model) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		val := strings.TrimSpace(m.input.Value())

		switch m.inputMode {
		case inputAddWorkspace:
			if val != "" {
				name := strings.ToUpper(val)
				createWorkspace(name)
				m.workspaces = loadWorkspaces()
			}
		case inputAddGroup:
			if val != "" {
				gf := GroupFile{Title: val, Todos: []Todo{}}
				saveGroupFile(m.currentWorkspace, val, gf)
				m.groups = loadGroups(m.currentWorkspace)
			}
		case inputAddTask:
			if val != "" {
				m.tasks.Todos = append(m.tasks.Todos, Todo{Title: val})
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			}
		case inputAddSubtask:
			if val != "" && m.taskCursor < len(m.tasks.Todos) {
				m.tasks.Todos[m.taskCursor].Subtasks = append(m.tasks.Todos[m.taskCursor].Subtasks, Subtask{Title: val})
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			}
		case inputRenameWorkspace:
			if val != "" && m.workspaceCursor > 0 && m.workspaceCursor < len(m.workspaces) {
				oldName := m.workspaces[m.workspaceCursor]
				newName := strings.ToUpper(val)
				// Cancel if identical
				if newName != oldName {
					oldPath := filepath.Join(getDataDir(), oldName)
					newPath := filepath.Join(getDataDir(), newName)
					_ = os.Rename(oldPath, newPath)
					m.workspaces = loadWorkspaces()
				}
			}
		case inputRenameGroup:
			if val != "" && m.groupCursor < len(m.groups) {
				oldName := m.groups[m.groupCursor]
				if !(m.currentWorkspace == "HOME" && oldName == "ALL") && val != oldName {
					gf := loadGroupFile(m.currentWorkspace, oldName)
					gf.Title = val
					saveGroupFile(m.currentWorkspace, val, gf)
					deleteGroupFile(m.currentWorkspace, oldName)
					m.groups = loadGroups(m.currentWorkspace)
				}
			}
		case inputRenameTask, inputRenameTaskTitle:
			if val != "" && m.taskCursor < len(m.tasks.Todos) {
				oldTitle := m.tasks.Todos[m.taskCursor].Title
				if val != oldTitle {
					m.tasks.Todos[m.taskCursor].Title = val
					saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
				}
			}
		case inputEditDescription:
			if m.taskCursor < len(m.tasks.Todos) {
				m.tasks.Todos[m.taskCursor].Description = val
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			}
		}
		m.inputMode = inputNone
		m.input.Reset()
		return m, nil

	case tea.KeyEsc:
		m.inputMode = inputNone
		m.input.Reset()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// ── CONFIRM DELETE ──────────────────────────────────────────────────────────────

func (m model) handleConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+x":
		switch m.state {
		case stateViewWorkspaces:
			if m.workspaceCursor > 0 {
				deleteWorkspace(m.workspaces[m.workspaceCursor])
				m.workspaces = loadWorkspaces()
				if m.workspaceCursor >= len(m.workspaces) {
					m.workspaceCursor = len(m.workspaces) - 1
				}
			}
		case stateViewGroups:
			if m.groupCursor < len(m.groups) {
				name := m.groups[m.groupCursor]
				if !(m.currentWorkspace == "HOME" && name == "ALL") {
					deleteGroupFile(m.currentWorkspace, name)
					m.groups = loadGroups(m.currentWorkspace)
					if m.groupCursor >= len(m.groups) {
						m.groupCursor = len(m.groups) - 1
					}
				}
			}
		case stateViewTasks:
			if m.taskCursor < len(m.tasks.Todos) {
				m.tasks.Todos = append(m.tasks.Todos[:m.taskCursor], m.tasks.Todos[m.taskCursor+1:]...)
				if m.taskCursor >= len(m.tasks.Todos) && m.taskCursor > 0 {
					m.taskCursor--
				}
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			}
		case stateTaskDetails:
			if m.taskCursor < len(m.tasks.Todos) {
				task := &m.tasks.Todos[m.taskCursor]
				if m.subtaskCursor < len(task.Subtasks) {
					task.Subtasks = append(task.Subtasks[:m.subtaskCursor], task.Subtasks[m.subtaskCursor+1:]...)
					if m.subtaskCursor >= len(task.Subtasks) && m.subtaskCursor > 0 {
						m.subtaskCursor--
					}
					saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
				}
			}
		}
		m.confirmDelete = false
	default:
		m.confirmDelete = false
	}
	return m, nil
}

// ── GIT CONSOLE HANDLER ────────────────────────────────────────────────────────

func (m model) handleGitConsole(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.state = m.prevState
		return m, nil
	case tea.KeyEnter:
		rawCmd := strings.TrimSpace(m.gitInput.Value())
		if rawCmd == "" {
			return m, nil
		}
		m.gitInput.Reset()

		// Strip leading "git " if user typed it
		cleanCmd := rawCmd
		if strings.HasPrefix(strings.ToLower(cleanCmd), "git ") {
			cleanCmd = strings.TrimSpace(cleanCmd[4:])
		}

		args := strings.Fields(cleanCmd)
		output := runGitCmd(args...)
		output = strings.TrimRight(output, "\n\r ")

		// Append to history
		entry := fmt.Sprintf("$ git %s\n%s\n", cleanCmd, output)
		if m.gitHistory == "" {
			m.gitHistory = entry
		} else {
			m.gitHistory += "\n" + entry
		}
		m.gitViewport.SetContent(m.gitHistory)
		m.gitViewport.GotoBottom()
		return m, nil
	}
	// Pass key events to the text input
	var cmd tea.Cmd
	m.gitInput, cmd = m.gitInput.Update(msg)
	return m, cmd
}

// ── WORKSPACE HANDLER ───────────────────────────────────────────────────────────

func (m model) handleWorkspaces(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.workspaceCursor > 0 {
			m.workspaceCursor--
		}
	case "down", "j":
		if m.workspaceCursor < len(m.workspaces)-1 {
			m.workspaceCursor++
		}
	case "enter", "right", "l":
		if m.workspaceCursor < len(m.workspaces) {
			m.currentWorkspace = m.workspaces[m.workspaceCursor]
			m.groups = loadGroups(m.currentWorkspace)
			m.groupCursor = 0
			m.state = stateViewGroups
		}
	case "n":
		m.inputMode = inputAddWorkspace
		m.input.Placeholder = "New Workspace (UPPERCASE)..."
		m.input.Reset()
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		if m.workspaceCursor > 0 {
			m.inputMode = inputRenameWorkspace
			m.input.SetValue(m.workspaces[m.workspaceCursor])
			m.input.Focus()
			return m, textinput.Blink
		}
	case "ctrl+x":
		if m.workspaceCursor > 0 {
			m.confirmDelete = true
		}
	case "shift+up", "K":
		if m.workspaceCursor > 1 {
			m.workspaces[m.workspaceCursor], m.workspaces[m.workspaceCursor-1] = m.workspaces[m.workspaceCursor-1], m.workspaces[m.workspaceCursor]
			m.workspaceCursor--
		}
	case "shift+down", "J":
		if m.workspaceCursor > 0 && m.workspaceCursor < len(m.workspaces)-1 {
			m.workspaces[m.workspaceCursor], m.workspaces[m.workspaceCursor+1] = m.workspaces[m.workspaceCursor+1], m.workspaces[m.workspaceCursor]
			m.workspaceCursor++
		}
	}
	return m, nil
}

// ── GROUP HANDLER ───────────────────────────────────────────────────────────────

func (m model) handleGroups(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "left", "h":
		m.workspaces = loadWorkspaces()
		m.state = stateViewWorkspaces
	case "up", "k":
		if m.groupCursor > 0 {
			m.groupCursor--
		}
	case "down", "j":
		if m.groupCursor < len(m.groups)-1 {
			m.groupCursor++
		}
	case "enter", "right", "l":
		if m.groupCursor < len(m.groups) {
			m.currentGroup = m.groups[m.groupCursor]
			if m.currentWorkspace == "HOME" && m.currentGroup == "ALL" {
				m.buildAllViewItems()
			}
			m.tasks = loadGroupFile(m.currentWorkspace, m.currentGroup)
			m.taskCursor = 0
			m.state = stateViewTasks
		}
	case "n":
		m.inputMode = inputAddGroup
		m.input.Placeholder = "New Group..."
		m.input.Reset()
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		if !(m.currentWorkspace == "HOME" && m.groupCursor == 0) {
			m.inputMode = inputRenameGroup
			m.input.SetValue(m.groups[m.groupCursor])
			m.input.Focus()
			return m, textinput.Blink
		}
	case "ctrl+x":
		if !(m.currentWorkspace == "HOME" && m.groupCursor == 0) && m.groupCursor < len(m.groups) {
			m.confirmDelete = true
		}
	case "shift+up", "K":
		minIdx := 0
		if m.currentWorkspace == "HOME" {
			minIdx = 1
		}
		if m.groupCursor > minIdx {
			m.groups[m.groupCursor], m.groups[m.groupCursor-1] = m.groups[m.groupCursor-1], m.groups[m.groupCursor]
			m.groupCursor--
		}
	case "shift+down", "J":
		minIdx := 0
		if m.currentWorkspace == "HOME" {
			minIdx = 1
		}
		if m.groupCursor >= minIdx && m.groupCursor < len(m.groups)-1 {
			m.groups[m.groupCursor], m.groups[m.groupCursor+1] = m.groups[m.groupCursor+1], m.groups[m.groupCursor]
			m.groupCursor++
		}
	}
	return m, nil
}

// ── TASK HANDLER ────────────────────────────────────────────────────────────────

func (m model) handleTasks(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	isAllView := m.currentWorkspace == "HOME" && m.currentGroup == "ALL"

	if isAllView {
		return m.handleAllViewTasks(msg)
	}

	switch msg.String() {
	case "esc", "left", "h":
		m.groups = loadGroups(m.currentWorkspace)
		m.state = stateViewGroups
	case "up", "k":
		if m.taskCursor > 0 {
			m.taskCursor--
		}
	case "down", "j":
		if m.taskCursor < len(m.tasks.Todos)-1 {
			m.taskCursor++
		}
	case " ":
		if m.taskCursor < len(m.tasks.Todos) {
			m.tasks.Todos[m.taskCursor].Done = !m.tasks.Todos[m.taskCursor].Done
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	case "enter", "right":
		if m.taskCursor < len(m.tasks.Todos) {
			m.subtaskCursor = 0
			m.state = stateTaskDetails
		}
	case "n":
		m.inputMode = inputAddTask
		m.input.Placeholder = "New Task..."
		m.input.Reset()
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		if m.taskCursor < len(m.tasks.Todos) {
			m.inputMode = inputRenameTask
			m.input.SetValue(m.tasks.Todos[m.taskCursor].Title)
			m.input.Focus()
			return m, textinput.Blink
		}
	case "ctrl+x":
		if m.taskCursor < len(m.tasks.Todos) {
			if len(m.tasks.Todos[m.taskCursor].Subtasks) > 0 {
				m.confirmDelete = true
			} else {
				// Direct delete for tasks without subtasks
				m.tasks.Todos = append(m.tasks.Todos[:m.taskCursor], m.tasks.Todos[m.taskCursor+1:]...)
				if m.taskCursor >= len(m.tasks.Todos) && m.taskCursor > 0 {
					m.taskCursor--
				}
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			}
		}
	case "shift+up", "K":
		if m.taskCursor > 0 {
			m.tasks.Todos[m.taskCursor], m.tasks.Todos[m.taskCursor-1] = m.tasks.Todos[m.taskCursor-1], m.tasks.Todos[m.taskCursor]
			m.taskCursor--
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	case "shift+down", "J":
		if m.taskCursor < len(m.tasks.Todos)-1 {
			m.tasks.Todos[m.taskCursor], m.tasks.Todos[m.taskCursor+1] = m.tasks.Todos[m.taskCursor+1], m.tasks.Todos[m.taskCursor]
			m.taskCursor++
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	}
	return m, nil
}

// ── ALL VIEW TASK HANDLER ───────────────────────────────────────────────────────

func (m model) handleAllViewTasks(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "left", "h":
		m.groups = loadGroups(m.currentWorkspace)
		m.state = stateViewGroups
	case "up", "k":
		if m.taskCursor > 0 {
			m.taskCursor--
		}
	case "down", "j":
		if m.taskCursor < len(m.allViewItems)-1 {
			m.taskCursor++
		}
	case " ":
		if m.taskCursor < len(m.allViewItems) {
			entry := m.allViewItems[m.taskCursor]
			if entry.isHeader {
				m.collapsed[entry.groupName] = !m.collapsed[entry.groupName]
				m.buildAllViewItems()
				if m.taskCursor >= len(m.allViewItems) {
					m.taskCursor = len(m.allViewItems) - 1
				}
			} else {
				gf := loadGroupFile(m.currentWorkspace, entry.groupName)
				if entry.taskIndex < len(gf.Todos) {
					gf.Todos[entry.taskIndex].Done = !gf.Todos[entry.taskIndex].Done
					saveGroupFile(m.currentWorkspace, entry.groupName, gf)
					m.buildAllViewItems()
				}
			}
		}
	case "enter":
		if m.taskCursor < len(m.allViewItems) {
			entry := m.allViewItems[m.taskCursor]
			if entry.isHeader {
				m.collapsed[entry.groupName] = !m.collapsed[entry.groupName]
				m.buildAllViewItems()
			}
		}
	}
	return m, nil
}

// ── TASK DETAILS HANDLER ────────────────────────────────────────────────────────

func (m model) handleTaskDetails(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.taskCursor >= len(m.tasks.Todos) {
		m.state = stateViewTasks
		return m, nil
	}
	task := &m.tasks.Todos[m.taskCursor]

	switch msg.String() {
	case "esc", "left":
		m.state = stateViewTasks
	case "up", "k":
		if m.subtaskCursor > 0 {
			m.subtaskCursor--
		}
	case "down", "j":
		if m.subtaskCursor < len(task.Subtasks)-1 {
			m.subtaskCursor++
		}
	case " ", "enter":
		if m.subtaskCursor < len(task.Subtasks) {
			task.Subtasks[m.subtaskCursor].Done = !task.Subtasks[m.subtaskCursor].Done
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	case "n":
		m.inputMode = inputAddSubtask
		m.input.Placeholder = "New Subtask..."
		m.input.Reset()
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		m.inputMode = inputRenameTaskTitle
		m.input.SetValue(task.Title)
		m.input.Focus()
		return m, textinput.Blink
	case "d":
		m.inputMode = inputEditDescription
		m.input.SetValue(task.Description)
		m.input.Focus()
		return m, textinput.Blink
	case "ctrl+x":
		if m.subtaskCursor < len(task.Subtasks) {
			task.Subtasks = append(task.Subtasks[:m.subtaskCursor], task.Subtasks[m.subtaskCursor+1:]...)
			if m.subtaskCursor >= len(task.Subtasks) && m.subtaskCursor > 0 {
				m.subtaskCursor--
			}
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	case "shift+up", "K":
		if m.subtaskCursor > 0 {
			task.Subtasks[m.subtaskCursor], task.Subtasks[m.subtaskCursor-1] = task.Subtasks[m.subtaskCursor-1], task.Subtasks[m.subtaskCursor]
			m.subtaskCursor--
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	case "shift+down", "J":
		if m.subtaskCursor < len(task.Subtasks)-1 {
			task.Subtasks[m.subtaskCursor], task.Subtasks[m.subtaskCursor+1] = task.Subtasks[m.subtaskCursor+1], task.Subtasks[m.subtaskCursor]
			m.subtaskCursor++
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	}
	return m, nil
}

// ─── VIEW ───────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder
	contentHeight := m.height - 6
	if contentHeight < 5 {
		contentHeight = 5
	}

	switch m.state {
	case stateViewWorkspaces:
		m.viewWorkspaces(&s, contentHeight)
	case stateViewGroups:
		m.viewGroups(&s, contentHeight)
	case stateViewTasks:
		if m.currentWorkspace == "HOME" && m.currentGroup == "ALL" {
			m.viewAllTasks(&s, contentHeight)
		} else {
			m.viewTasks(&s, contentHeight)
		}
	case stateTaskDetails:
		m.viewTaskDetails(&s, contentHeight)
	case stateGitConsole:
		m.viewGitConsole(&s, contentHeight)
	}

	content := s.String()

	// ── COMPACT MODE ──
	if m.compactMode {
		return content + "\n"
	}

	// ── FULL MODE (Boxed) ──
	borderColor := lipgloss.Color("62")
	switch m.state {
	case stateViewGroups:
		borderColor = getColor(m.workspaceCursor)
	case stateViewTasks, stateTaskDetails:
		borderColor = getColor(m.groupCursor)
	case stateGitConsole:
		borderColor = lipgloss.Color("#FFD700")
	}

	w := m.width
	h := m.height
	if w == 0 {
		w = 80
	}
	if h == 0 {
		h = 24
	}

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center,
		appStyle.Copy().BorderForeground(borderColor).Width(clamp(w-4, 40, 70)).Render(content))
}

// ── VIEW: WORKSPACES ────────────────────────────────────────────────────────────

func (m model) viewWorkspaces(s *strings.Builder, maxH int) {
	s.WriteString(titleStyle.Render("  Workspaces") + "\n\n")

	visibleStart, visibleEnd := scrollWindow(m.workspaceCursor, len(m.workspaces), maxH-4)

	for i := visibleStart; i < visibleEnd; i++ {
		ws := m.workspaces[i]
		cursor := "  "
		if m.workspaceCursor == i {
			cursor = "> "
		}

		done, total := countWorkspaceTasks(ws)
		label := fmt.Sprintf("%s (%d/%d)", ws, done, total)

		color := getColor(i)
		style := lipgloss.NewStyle().Foreground(color)
		if m.workspaceCursor == i {
			style = style.Bold(true).Background(lipgloss.Color("236")).Padding(0, 1)
		}

		if m.confirmDelete && m.workspaceCursor == i {
			label += " [DELETE? Ctrl+x / any]"
			style = style.Foreground(lipgloss.Color("196"))
		}

		s.WriteString(cursor + style.Render(label) + "\n")
	}

	if visibleEnd < len(m.workspaces) {
		s.WriteString(faintStyle.Render(fmt.Sprintf("  ... +%d more", len(m.workspaces)-visibleEnd)) + "\n")
	}

	s.WriteString("\n")
	if m.inputMode == inputAddWorkspace || m.inputMode == inputRenameWorkspace {
		s.WriteString("  " + m.input.View())
	} else {
		s.WriteString(faintStyle.Render("  ↑↓: Nav • →/Enter: Open • n: New • r: Rename • Ctrl+x: Del • g: Git"))
	}
}

// ── VIEW: GROUPS ────────────────────────────────────────────────────────────────

func (m model) viewGroups(s *strings.Builder, maxH int) {
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  Groups"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(getColor(m.workspaceCursor)).Bold(true).Render(m.currentWorkspace),
	)
	s.WriteString(header + "\n\n")

	visibleStart, visibleEnd := scrollWindow(m.groupCursor, len(m.groups), maxH-4)

	for i := visibleStart; i < visibleEnd; i++ {
		g := m.groups[i]
		cursor := "  "
		if m.groupCursor == i {
			cursor = "> "
		}

		done, total := countGroupTasks(m.currentWorkspace, g)
		label := fmt.Sprintf("%s (%d/%d)", g, done, total)

		color := getColor(i)
		style := lipgloss.NewStyle().Foreground(color)
		if m.groupCursor == i {
			style = style.Bold(true).Background(lipgloss.Color("236")).Padding(0, 1)
		}

		if m.confirmDelete && m.groupCursor == i {
			label += " [DELETE? Ctrl+x / any]"
			style = style.Foreground(lipgloss.Color("196"))
		}

		s.WriteString(cursor + style.Render(label) + "\n")
	}

	if visibleEnd < len(m.groups) {
		s.WriteString(faintStyle.Render(fmt.Sprintf("  ... +%d more", len(m.groups)-visibleEnd)) + "\n")
	}

	s.WriteString("\n")
	if m.inputMode == inputAddGroup || m.inputMode == inputRenameGroup {
		s.WriteString("  " + m.input.View())
	} else {
		s.WriteString(faintStyle.Render("  ←: Back • ↑↓: Nav • →/Enter: Open • n: New • r: Rename • Ctrl+x: Del"))
	}
}

// ── VIEW: TASKS ─────────────────────────────────────────────────────────────────

func (m model) viewTasks(s *strings.Builder, maxH int) {
	color := getColor(m.groupCursor)
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  Tasks"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(m.currentWorkspace+" > "+m.currentGroup),
	)
	s.WriteString(header + "\n\n")

	if len(m.tasks.Todos) == 0 {
		s.WriteString(faintStyle.Render("  No tasks. Press 'n' to add one.") + "\n")
	} else {
		visibleStart, visibleEnd := scrollWindow(m.taskCursor, len(m.tasks.Todos), maxH-4)

		for i := visibleStart; i < visibleEnd; i++ {
			t := m.tasks.Todos[i]
			cursor := "  "
			if m.taskCursor == i {
				cursor = "> "
			}

			check := "[ ]"
			if t.Done {
				check = "[x]"
			}
			subCount := ""
			if len(t.Subtasks) > 0 {
				subDone := 0
				for _, st := range t.Subtasks {
					if st.Done {
						subDone++
					}
				}
				subCount = fmt.Sprintf(" [%d/%d]", subDone, len(t.Subtasks))
			}

			tStyle := lipgloss.NewStyle()
			if t.Done {
				tStyle = tStyle.Strikethrough(true).Faint(true).Foreground(lipgloss.Color("240"))
			} else {
				tStyle = tStyle.Foreground(lipgloss.Color("255"))
			}
			if m.taskCursor == i {
				tStyle = tStyle.Foreground(color)
				cursor = lipgloss.NewStyle().Foreground(color).Render(cursor)
			}

			if m.confirmDelete && m.taskCursor == i {
				label := fmt.Sprintf("%s %s%s [DELETE? Ctrl+x / any]", check, t.Title, subCount)
				s.WriteString(fmt.Sprintf("%s%s\n", cursor, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(label)))
			} else {
				label := fmt.Sprintf("%s %s%s", check, t.Title, subCount)
				s.WriteString(fmt.Sprintf("%s%s\n", cursor, tStyle.Render(label)))
			}
		}

		if visibleEnd < len(m.tasks.Todos) {
			s.WriteString(faintStyle.Render(fmt.Sprintf("  ... +%d more", len(m.tasks.Todos)-visibleEnd)) + "\n")
		}
	}

	s.WriteString("\n")
	if m.inputMode == inputAddTask || m.inputMode == inputRenameTask {
		s.WriteString("  " + m.input.View())
	} else {
		s.WriteString(faintStyle.Render("  ←: Back • Space: ✓ • →/Enter: Details • n: New • r: Rename • Ctrl+x: Del • K/J: Reorder"))
	}
}

// ── VIEW: ALL VIEW ──────────────────────────────────────────────────────────────

func (m model) viewAllTasks(s *strings.Builder, maxH int) {
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  All Tasks"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true).Render("HOME"),
	)
	s.WriteString(header + "\n\n")

	if len(m.allViewItems) == 0 {
		s.WriteString(faintStyle.Render("  No tasks in any group.") + "\n")
	} else {
		visibleStart, visibleEnd := scrollWindow(m.taskCursor, len(m.allViewItems), maxH-4)

		for i := visibleStart; i < visibleEnd; i++ {
			entry := m.allViewItems[i]
			cursor := "  "
			if m.taskCursor == i {
				cursor = "> "
			}

			if entry.isHeader {
				arrow := "▼"
				if m.collapsed[entry.groupName] {
					arrow = "▶"
				}
				done, total := countGroupTasks(m.currentWorkspace, entry.groupName)
				gIdx := indexOf(m.groups, entry.groupName)
				color := getColor(gIdx)
				style := lipgloss.NewStyle().Foreground(color).Bold(true)
				if m.taskCursor == i {
					style = style.Background(lipgloss.Color("236")).Padding(0, 1)
				}
				label := fmt.Sprintf("%s %s (%d/%d)", arrow, entry.groupName, done, total)
				s.WriteString(cursor + style.Render(label) + "\n")
			} else {
				gf := loadGroupFile(m.currentWorkspace, entry.groupName)
				if entry.taskIndex < len(gf.Todos) {
					t := gf.Todos[entry.taskIndex]
					check := "[ ]"
					if t.Done {
						check = "[x]"
					}
					tStyle := lipgloss.NewStyle()
					if t.Done {
						tStyle = tStyle.Strikethrough(true).Faint(true).Foreground(lipgloss.Color("240"))
					} else {
						tStyle = tStyle.Foreground(lipgloss.Color("255"))
					}
					gIdx := indexOf(m.groups, entry.groupName)
					color := getColor(gIdx)
					if m.taskCursor == i {
						tStyle = tStyle.Foreground(color)
						cursor = lipgloss.NewStyle().Foreground(color).Render(cursor)
					}
					label := fmt.Sprintf("    %s %s", check, t.Title)
					s.WriteString(fmt.Sprintf("%s%s\n", cursor, tStyle.Render(label)))
				}
			}
		}

		if visibleEnd < len(m.allViewItems) {
			s.WriteString(faintStyle.Render(fmt.Sprintf("  ... +%d more", len(m.allViewItems)-visibleEnd)) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(faintStyle.Render("  ←: Back • Space: Toggle • ↑↓: Nav"))
}

func indexOf(slice []string, val string) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return 0
}

// ── VIEW: TASK DETAILS ──────────────────────────────────────────────────────────

func (m model) viewTaskDetails(s *strings.Builder, maxH int) {
	if m.taskCursor >= len(m.tasks.Todos) {
		s.WriteString("No task selected.\n")
		return
	}
	task := m.tasks.Todos[m.taskCursor]
	color := getColor(m.groupCursor)

	s.WriteString(titleStyle.Render("  Task Details") + "\n\n")

	// Title
	titleLabel := lipgloss.NewStyle().Foreground(color).Bold(true).Render(task.Title)
	s.WriteString("  Title: " + titleLabel + "\n")

	// Description
	desc := task.Description
	if desc == "" {
		desc = lipgloss.NewStyle().Faint(true).Render("(no description)")
	}
	s.WriteString("  Desc:  " + desc + "\n\n")

	// Subtasks
	s.WriteString(boldStyle.Foreground(color).Render("  Subtasks") + "\n")
	if len(task.Subtasks) == 0 {
		s.WriteString(faintStyle.Render("    No subtasks. Press 'n' to add.") + "\n")
	} else {
		for i, st := range task.Subtasks {
			cursor := "  "
			if m.subtaskCursor == i {
				cursor = "> "
			}
			check := "[ ]"
			if st.Done {
				check = "[x]"
			}
			stStyle := lipgloss.NewStyle()
			if st.Done {
				stStyle = stStyle.Strikethrough(true).Faint(true).Foreground(lipgloss.Color("240"))
			} else {
				stStyle = stStyle.Foreground(lipgloss.Color("255"))
			}
			if m.subtaskCursor == i {
				stStyle = stStyle.Foreground(color)
			}
			s.WriteString(fmt.Sprintf("  %s%s\n", cursor, stStyle.Render(check+" "+st.Title)))
		}
	}

	s.WriteString("\n")
	if m.inputMode == inputAddSubtask || m.inputMode == inputRenameTaskTitle || m.inputMode == inputEditDescription {
		s.WriteString("  " + m.input.View())
	} else {
		s.WriteString(faintStyle.Render("  ←/Esc: Back • r: Rename • d: Description • n: Add Subtask • Space: ✓ • Ctrl+x: Del • K/J: Reorder"))
	}
}

// ── VIEW: GIT CONSOLE ───────────────────────────────────────────────────────────

func (m model) viewGitConsole(s *strings.Builder, maxH int) {
	header := titleStyle.Copy().
		Background(lipgloss.Color("#FFD700")).
		Foreground(lipgloss.Color("0")).
		Render("  Git Console")
	s.WriteString(header + "\n\n")

	// Viewport (scrollable output)
	vpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
	s.WriteString(vpStyle.Render(m.gitViewport.View()) + "\n\n")

	// Input prompt
	s.WriteString("  " + m.gitInput.View() + "\n\n")

	s.WriteString(faintStyle.Render("  Ctrl+c / Esc: Close • Enter: Run command"))
}

// ─── HELPERS ────────────────────────────────────────────────────────────────────

func clamp(val, lo, hi int) int {
	if val < lo {
		return lo
	}
	if val > hi {
		return hi
	}
	return val
}

// scrollWindow returns the visible [start, end) range for a list of `total`
// items, keeping `cursor` centered when possible.
func scrollWindow(cursor, total, maxVisible int) (int, int) {
	if maxVisible <= 0 {
		maxVisible = 10
	}
	if total <= maxVisible {
		return 0, total
	}
	half := maxVisible / 2
	start := cursor - half
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > total {
		end = total
		start = end - maxVisible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// ─── MAIN ───────────────────────────────────────────────────────────────────────

func main() {
	// Suppress timestamp from git output
	_ = time.Now()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
