package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"todotui/ai"
)

// ─── DATA STRUCTURES ───────────────────────────────────────────────────────────

type Subtask struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// AIMessage represents a single message in the AI conversation history.
type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Todo struct {
	Title          string      `json:"title"`
	Done           bool        `json:"done"`
	Description    string      `json:"description"`
	Subtasks       []Subtask   `json:"subtasks"`
	Today          bool        `json:"today,omitempty"`
	AIConversation []AIMessage `json:"ai_conversation,omitempty"`
}

type GroupFile struct {
	Title string `json:"title"`
	Todos []Todo `json:"todos"`
}

// meta.json per workspace — stores custom group ordering and metadata
type GroupMeta struct {
	Name        string `json:"name"`
	IsFavorite  bool   `json:"is_favorite,omitempty"`
	ColorOffset int    `json:"color_offset,omitempty"`
}
type WorkspaceMeta struct {
	Groups []GroupMeta `json:"groups"`
}

type todayEntry struct {
	workspace string
	group     string
	taskIndex int
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

type undoState struct {
	workspace string
	group     string
	tasks     GroupFile
}

// ─── STATE MACHINE ──────────────────────────────────────────────────────────────

type sessionState int

const (
	stateViewWorkspaces sessionState = iota
	stateViewGroups
	stateViewTasks
	stateTaskDetails
	stateGitConsole
	stateTodayView
	stateAIPanel
	stateAIPriorityModal
	stateAIGlobalConsole
	stateAIScanModal
	stateAIConfirmAction
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
	inputRenameSubtask
)

// ─── MODEL ──────────────────────────────────────────────────────────────────────

type model struct {
	state     sessionState
	prevState sessionState // for git console return

	workspaces       []string
	workspaceCursor  int
	currentWorkspace string

	groups        []string
	groupCursor   int
	currentGroup  string
	workspaceMeta WorkspaceMeta // loaded alongside groups

	tasks      GroupFile
	taskCursor int

	subtaskCursor int

	// Git Console
	gitViewport viewport.Model
	gitInput    textinput.Model
	gitHistory  string

	// All/Favorites view collapse
	collapsed    map[string]bool
	allViewItems []allViewEntry

	// Today view
	todayItems []todayEntry

	// Input
	input         textinput.Model
	inputMode     inputTarget
	adding        bool
	confirmDelete bool

	// AI Agent Panel
	aiInput          textinput.Model
	aiMessages       []AIMessage  // current task's conversation (mirrors task.AIConversation)
	aiScrollOffset   int
	aiWaiting        bool         // true while waiting for API response
	aiSpinner        spinner.Model
	aiConfig         ai.Config    // loaded on startup
	aiPendingSubtasks []string    // parsed subtasks awaiting user confirmation

	// AI Priority Modal
	aiPriorityResult string // the AI's priority recommendation text
	aiPriorityError  string

	// AI Global Console
	aiGlobalError string

	// AI Scan Modal
	aiScanResult      string
	aiScanError       string
	aiScanSuggestions []ai.ScanSuggestion
	aiScanCursor      int

	pendingAction *ai.ParsedAction

	// Undo Stack
	undoStack []undoState

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
	idx := (index - 1) % len(palette)
	if idx < 0 {
		idx += len(palette)
	}
	return palette[idx]
}

func (m model) getGroupColor(index int, groupName string) lipgloss.Color {
	offset := 0
	if !isVirtualGroup(groupName) {
		for _, gm := range m.workspaceMeta.Groups {
			if gm.Name == groupName {
				offset = gm.ColorOffset
				break
			}
		}
	}
	return getColor(index + offset)
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

// ── META.JSON PERSISTENCE ───────────────────────────────────────────────────────

func loadWorkspaceMeta(workspace string) WorkspaceMeta {
	path := filepath.Join(getDataDir(), workspace, "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceMeta{}
	}
	var meta WorkspaceMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return WorkspaceMeta{}
	}
	return meta
}

func saveWorkspaceMeta(workspace string, meta WorkspaceMeta) {
	dir := filepath.Join(getDataDir(), workspace)
	ensureDir(dir)
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644)
}

func isVirtualGroup(name string) bool {
	return name == "ALL" || name == "FAVORITES"
}

// loadGroupsWithMeta reads meta.json for ordering, reconciles with disk files,
// then injects virtual groups (ALL at 0, FAVORITES at 1 if any favorites exist).
// Returns the display list and the reconciled meta.
func loadGroupsWithMeta(workspace string) ([]string, WorkspaceMeta) {
	dir := filepath.Join(getDataDir(), workspace)
	ensureDir(dir)

	// 1. Scan disk for actual .json files (exclude meta.json)
	entries, _ := os.ReadDir(dir)
	diskFiles := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") && strings.ToLower(e.Name()) != "meta.json" {
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			diskFiles[name] = true
		}
	}

	// 2. Load meta
	meta := loadWorkspaceMeta(workspace)

	// 3. Reconcile: keep only entries that exist on disk, in meta order
	reconciled := []GroupMeta{}
	seen := map[string]bool{}
	for _, gm := range meta.Groups {
		if diskFiles[gm.Name] && !isVirtualGroup(gm.Name) {
			reconciled = append(reconciled, gm)
			seen[gm.Name] = true
		}
	}

	// 4. Append any new files not yet in meta
	for name := range diskFiles {
		if !seen[name] && !isVirtualGroup(name) {
			reconciled = append(reconciled, GroupMeta{Name: name})
		}
	}

	meta.Groups = reconciled
	saveWorkspaceMeta(workspace, meta)

	// 5. Build display list: inject virtual groups
	groups := []string{"ALL"}

	// Check if any favorites exist
	hasFav := false
	for _, gm := range meta.Groups {
		if gm.IsFavorite {
			hasFav = true
			break
		}
	}
	if hasFav {
		groups = append(groups, "FAVORITES")
	}

	// Append real groups in meta order
	for _, gm := range meta.Groups {
		groups = append(groups, gm.Name)
	}
	return groups, meta
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

func (m *model) pushUndoState() {
	if m.currentWorkspace == "" || m.currentGroup == "" || isVirtualGroup(m.currentGroup) {
		return
	}
	// Deep copy via JSON
	data, err := json.Marshal(m.tasks)
	if err != nil {
		return
	}
	var copiedGroup GroupFile
	json.Unmarshal(data, &copiedGroup)

	state := undoState{
		workspace: m.currentWorkspace,
		group:     m.currentGroup,
		tasks:     copiedGroup,
	}
	m.undoStack = append(m.undoStack, state)
	// keep last 20 states
	if len(m.undoStack) > 20 {
		m.undoStack = m.undoStack[1:]
	}
}

func (m *model) popUndoState() {
	if len(m.undoStack) == 0 {
		return
	}
	last := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]

	// Restore file
	saveGroupFile(last.workspace, last.group, last.tasks)
	
	// If we are currently in that workspace/group, reload tasks
	if m.currentWorkspace == last.workspace && m.currentGroup == last.group {
		m.tasks = last.tasks
		if m.taskCursor >= len(m.tasks.Todos) {
			m.taskCursor = len(m.tasks.Todos) - 1
			if m.taskCursor < 0 {
				m.taskCursor = 0
			}
		}
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
	groups, _ := loadGroupsWithMeta(workspace)
	for _, g := range groups {
		if isVirtualGroup(g) {
			continue
		}
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
	ti.Prompt = ""

	aiTi := textinput.New()
	aiTi.Placeholder = "Pergunte ao agente AI..."
	aiTi.Prompt = "Você > "
	aiTi.CharLimit = 500

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))

	gi := textinput.New()
	gi.Placeholder = "command..."
	gi.Prompt = "git > "
	gi.CharLimit = 256

	vp := viewport.New(70, 10)
	vp.SetContent("")

	ws := loadWorkspaces()

	return model{
		state:            stateViewWorkspaces,
		workspaces:       ws,
		currentWorkspace: "HOME",
		input:            ti,
		aiInput:          aiTi,
		aiSpinner:        sp,
		aiConfig:         ai.LoadConfig(),
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
	for _, gm := range m.workspaceMeta.Groups {
		gf := loadGroupFile(m.currentWorkspace, gm.Name)
		m.allViewItems = append(m.allViewItems, allViewEntry{isHeader: true, groupName: gm.Name})
		if !m.collapsed[gm.Name] {
			for i := range gf.Todos {
				m.allViewItems = append(m.allViewItems, allViewEntry{groupName: gm.Name, taskIndex: i})
			}
		}
	}
}

func (m *model) buildFavViewItems() {
	m.allViewItems = nil
	for _, gm := range m.workspaceMeta.Groups {
		if !gm.IsFavorite {
			continue
		}
		gf := loadGroupFile(m.currentWorkspace, gm.Name)
		m.allViewItems = append(m.allViewItems, allViewEntry{isHeader: true, groupName: gm.Name})
		if !m.collapsed[gm.Name] {
			for i := range gf.Todos {
				m.allViewItems = append(m.allViewItems, allViewEntry{groupName: gm.Name, taskIndex: i})
			}
		}
	}
}

func (m *model) buildTodayItems() {
	m.todayItems = nil
	for _, ws := range m.workspaces {
		groups, meta := loadGroupsWithMeta(ws)
		_ = groups
		for _, gm := range meta.Groups {
			gf := loadGroupFile(ws, gm.Name)
			for i, t := range gf.Todos {
				if t.Today {
					m.todayItems = append(m.todayItems, todayEntry{workspace: ws, group: gm.Name, taskIndex: i})
				}
			}
		}
	}
}

// ─── AI MESSAGE TYPES ───────────────────────────────────────────────────────────

// aiResponseMsg is returned by the async OpenAI call for the chat panel.
type aiResponseMsg struct {
	Content string
	Err     error
}

// aiPriorityResponseMsg is returned by the async OpenAI call for priority recommendation.
type aiPriorityResponseMsg struct {
	Content string
	Err     error
}

// sendAIRequest returns a tea.Cmd that calls the OpenAI API asynchronously.
func sendAIRequest(config ai.Config, messages []ai.ChatMessage) tea.Cmd {
	return func() tea.Msg {
		resp, err := ai.CallOpenAI(config.OpenAIAPIKey, config.OpenAIModel, messages)
		if err != nil {
			return aiResponseMsg{Err: err}
		}
		return aiResponseMsg{Content: resp}
	}
}

// sendAIPriorityRequest returns a tea.Cmd for the priority recommendation.
func sendAIPriorityRequest(config ai.Config, messages []ai.ChatMessage) tea.Cmd {
	return func() tea.Msg {
		resp, err := ai.CallOpenAI(config.OpenAIAPIKey, config.OpenAIModel, messages)
		if err != nil {
			return aiPriorityResponseMsg{Err: err}
		}
		return aiPriorityResponseMsg{Content: resp}
	}
}

// buildAIChatMessages converts model state into the []ai.ChatMessage slice
// needed for the OpenAI API call, including the system prompt.
func (m model) buildAIChatMessages() []ai.ChatMessage {
	task := m.tasks.Todos[m.taskCursor]

	// Build subtask info
	subtaskInfos := make([]ai.SubtaskInfo, len(task.Subtasks))
	for i, st := range task.Subtasks {
		subtaskInfos[i] = ai.SubtaskInfo{Title: st.Title, Done: st.Done}
	}

	systemPrompt := ai.BuildTaskSystemPrompt(
		m.currentWorkspace, m.currentGroup,
		task.Title, task.Description, subtaskInfos,
	)

	msgs := []ai.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}

	// Add conversation history
	for _, msg := range m.aiMessages {
		msgs = append(msgs, ai.ChatMessage{Role: msg.Role, Content: msg.Content})
	}

	return msgs
}

// ─── UPDATE ─────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, tea.Quit
	}

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

	case aiResponseMsg:
		m.aiWaiting = false
		if msg.Err != nil {
			// Show error as a system message in the chat
			m.aiMessages = append(m.aiMessages, AIMessage{Role: "system", Content: "⚠ " + msg.Err.Error()})
		} else {
			m.aiMessages = append(m.aiMessages, AIMessage{Role: "assistant", Content: msg.Content})
			// Check if AI proposed subtasks
			proposed := ai.ParseSubtaskProposal(msg.Content)
			if len(proposed) > 0 {
				m.aiPendingSubtasks = proposed
			}
		}
		// Persist conversation to task
		if m.taskCursor < len(m.tasks.Todos) {
			m.tasks.Todos[m.taskCursor].AIConversation = m.aiMessages
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
		// Auto-scroll to bottom
		m.aiScrollOffset = 0
		return m, nil

	case aiPriorityResponseMsg:
		m.aiWaiting = false
		if msg.Err != nil {
			m.aiPriorityError = msg.Err.Error()
			m.aiPriorityResult = ""
		} else {
			m.aiPriorityResult = msg.Content
			m.aiPriorityError = ""
		}
		return m, nil

	case aiScanResponseMsg:
		m.aiWaiting = false
		if msg.Err != nil {
			m.aiScanError = msg.Err.Error()
			m.aiScanResult = ""
		} else {
			m.aiScanResult = msg.Content
			m.aiScanError = ""
			m.aiScanSuggestions = ai.ParseScanResponse(msg.Content)
			m.aiScanCursor = 0
		}
		return m, nil

	case aiGlobalResponseMsg:
		m.aiWaiting = false
		if msg.Err != nil {
			m.aiGlobalError = msg.Err.Error()
		} else {
			m.aiGlobalError = ""
			m.aiMessages = append(m.aiMessages, AIMessage{Role: "assistant", Content: msg.Content})
			m.aiScrollOffset = 0
			// Parse action
			action := ai.ParseAction(msg.Content)
			if action != nil {
				m.pendingAction = action
				m.prevState = m.state // stateAIGlobalConsole
				m.state = stateAIConfirmAction
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.aiWaiting {
			var cmd tea.Cmd
			m.aiSpinner, cmd = m.aiSpinner.Update(msg)
			return m, cmd
		}
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
		// ── AI PANEL ──
		if m.state == stateAIPanel {
			return m.handleAIPanel(msg)
		}
		// ── AI PRIORITY MODAL ──
		if m.state == stateAIPriorityModal {
			return m.handleAIPriorityModal(msg)
		}
		// ── AI SCAN MODAL ──
		if m.state == stateAIScanModal {
			return m.handleAIScanModal(msg)
		}
		// ── AI GLOBAL CONSOLE ──
		if m.state == stateAIGlobalConsole {
			return m.handleAIGlobalConsole(msg)
		}
		// ── AI CONFIRM ACTION ──
		if m.state == stateAIConfirmAction {
			return m.handleAIConfirmAction(msg)
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
		case "u":
			m.popUndoState()
			return m, nil
		case "C":
			if !m.aiConfig.AIEnabled {
				return m, nil
			}
			if m.aiConfig.OpenAIAPIKey == "" {
				m.aiGlobalError = "AI não configurada. Defina OPENAI_API_KEY ou adicione a chave no config.json"
			} else {
				m.aiGlobalError = ""
			}
			m.prevState = m.state
			m.state = stateAIGlobalConsole
			m.aiInput.Reset()
			cmd := m.aiInput.Focus()
			return m, cmd
		case "g":
			m.prevState = m.state
			m.state = stateGitConsole
			m.gitHistory = ""
			m.gitViewport.SetContent(lipgloss.NewStyle().Faint(true).Render("  Type a git command and press Enter. (e.g. status, log --oneline -5)"))
			m.gitViewport.GotoBottom()
			m.gitInput.Reset()
			cmd := m.gitInput.Focus()
			return m, cmd
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
		case stateTodayView:
			return m.handleTodayView(msg)
		}

	default:
		// Forward non-key messages (cursor blink, etc.) to the active input
		var cmd tea.Cmd
		if m.inputMode != inputNone {
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.state == stateGitConsole {
			m.gitInput, cmd = m.gitInput.Update(msg)
			return m, cmd
		}
		if m.state == stateAIPanel || m.state == stateAIGlobalConsole {
			m.aiInput, cmd = m.aiInput.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// ── INPUT HANDLER ───────────────────────────────────────────────────────────────

func (m model) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		val := strings.TrimSpace(m.input.Value())
		m.input.Blur()

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
				m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
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
					err := os.Rename(oldPath, newPath)
					if err == nil {
						backup := getBackupDir()
						if backup != "" {
							_ = os.Rename(filepath.Join(backup, oldName), filepath.Join(backup, newName))
						}
						m.workspaces = loadWorkspaces()
					}
				}
			}
		case inputRenameGroup:
			if val != "" && m.groupCursor < len(m.groups) {
				oldName := m.groups[m.groupCursor]
				if !isVirtualGroup(oldName) && val != oldName {
					oldPath := filepath.Join(getDataDir(), m.currentWorkspace, oldName+".json")
					newPath := filepath.Join(getDataDir(), m.currentWorkspace, val+".json")
					err := os.Rename(oldPath, newPath)
					if err == nil {
						backup := getBackupDir()
						if backup != "" {
							bDir := filepath.Join(backup, m.currentWorkspace)
							_ = os.Rename(filepath.Join(bDir, oldName+".json"), filepath.Join(bDir, val+".json"))
						}

						gf := loadGroupFile(m.currentWorkspace, val)
						gf.Title = val
						saveGroupFile(m.currentWorkspace, val, gf)

						// Update meta.json entry name
						for i, gm := range m.workspaceMeta.Groups {
							if gm.Name == oldName {
								m.workspaceMeta.Groups[i].Name = val
								break
							}
						}
						saveWorkspaceMeta(m.currentWorkspace, m.workspaceMeta)
						m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
					}
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
		case inputRenameSubtask:
			if val != "" && m.taskCursor < len(m.tasks.Todos) {
				task := &m.tasks.Todos[m.taskCursor]
				if m.subtaskCursor < len(task.Subtasks) {
					task.Subtasks[m.subtaskCursor].Title = val
					saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
				}
			}
		}
		m.inputMode = inputNone
		m.input.Reset()
		return m, nil

	case tea.KeyEsc:
		m.input.Blur()
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
				if !isVirtualGroup(name) {
					deleteGroupFile(m.currentWorkspace, name)
					m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
					if m.groupCursor >= len(m.groups) {
						m.groupCursor = len(m.groups) - 1
					}
				}
			}
		case stateViewTasks:
			if m.taskCursor < len(m.tasks.Todos) {
				m.pushUndoState()
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
					m.pushUndoState()
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
			m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
			m.groupCursor = 0
			m.state = stateViewGroups
		}
	case "n":
		m.inputMode = inputAddWorkspace
		m.input.Placeholder = "New Workspace (UPPERCASE)..."
		m.input.Reset()
		cmd := m.input.Focus()
		return m, cmd
	case "r":
		if m.workspaceCursor > 0 {
			m.inputMode = inputRenameWorkspace
			m.input.SetValue(m.workspaces[m.workspaceCursor])
			cmd := m.input.Focus()
			return m, cmd
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
	case "T":
		// Open Today view
		m.buildTodayItems()
		m.taskCursor = 0
		m.state = stateTodayView
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
			if m.currentGroup == "ALL" {
				m.buildAllViewItems()
			} else if m.currentGroup == "FAVORITES" {
				m.buildFavViewItems()
			}
			if !isVirtualGroup(m.currentGroup) {
				m.tasks = loadGroupFile(m.currentWorkspace, m.currentGroup)
			}
			m.taskCursor = 0
			m.state = stateViewTasks
		}
	case "n":
		m.inputMode = inputAddGroup
		m.input.Placeholder = "New Group..."
		m.input.Reset()
		cmd := m.input.Focus()
		return m, cmd
	case "r":
		if m.groupCursor < len(m.groups) && !isVirtualGroup(m.groups[m.groupCursor]) {
			m.inputMode = inputRenameGroup
			m.input.SetValue(m.groups[m.groupCursor])
			cmd := m.input.Focus()
			return m, cmd
		}
	case "ctrl+x":
		if m.groupCursor < len(m.groups) && !isVirtualGroup(m.groups[m.groupCursor]) {
			m.confirmDelete = true
		}
	case "f":
		// Toggle favorite on selected group
		if m.groupCursor < len(m.groups) && !isVirtualGroup(m.groups[m.groupCursor]) {
			gName := m.groups[m.groupCursor]
			for i, gm := range m.workspaceMeta.Groups {
				if gm.Name == gName {
					m.workspaceMeta.Groups[i].IsFavorite = !gm.IsFavorite
					break
				}
			}
			saveWorkspaceMeta(m.currentWorkspace, m.workspaceMeta)
			m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
		}
	case "c":
		// Randomize color for selected group
		if m.groupCursor < len(m.groups) && !isVirtualGroup(m.groups[m.groupCursor]) {
			gName := m.groups[m.groupCursor]
			for i, gm := range m.workspaceMeta.Groups {
				if gm.Name == gName {
					m.workspaceMeta.Groups[i].ColorOffset += rand.Intn(len(palette)-1) + 1
					break
				}
			}
			saveWorkspaceMeta(m.currentWorkspace, m.workspaceMeta)
			m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
		}
	case "shift+up", "K":
		// Only reorder real groups (skip virtual)
		if m.groupCursor < len(m.groups) && !isVirtualGroup(m.groups[m.groupCursor]) {
			prev := m.groupCursor - 1
			if prev >= 0 && !isVirtualGroup(m.groups[prev]) {
				m.groups[m.groupCursor], m.groups[prev] = m.groups[prev], m.groups[m.groupCursor]
				m.groupCursor = prev
				// Persist order: rebuild meta from current groups order
				m.syncGroupsToMeta()
			}
		}
	case "shift+down", "J":
		if m.groupCursor < len(m.groups) && !isVirtualGroup(m.groups[m.groupCursor]) {
			next := m.groupCursor + 1
			if next < len(m.groups) && !isVirtualGroup(m.groups[next]) {
				m.groups[m.groupCursor], m.groups[next] = m.groups[next], m.groups[m.groupCursor]
				m.groupCursor = next
				m.syncGroupsToMeta()
			}
		}
	}
	return m, nil
}

// syncGroupsToMeta rebuilds meta group order from the display list (skipping virtual groups)
func (m *model) syncGroupsToMeta() {
	// Build a lookup of existing meta entries to preserve IsFavorite etc.
	metaMap := map[string]GroupMeta{}
	for _, gm := range m.workspaceMeta.Groups {
		metaMap[gm.Name] = gm
	}
	newOrder := []GroupMeta{}
	for _, name := range m.groups {
		if isVirtualGroup(name) {
			continue
		}
		if gm, ok := metaMap[name]; ok {
			newOrder = append(newOrder, gm)
		} else {
			newOrder = append(newOrder, GroupMeta{Name: name})
		}
	}
	m.workspaceMeta.Groups = newOrder
	saveWorkspaceMeta(m.currentWorkspace, m.workspaceMeta)
}

// ── TASK HANDLER ────────────────────────────────────────────────────────────────

func (m model) handleTasks(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	isVirtual := isVirtualGroup(m.currentGroup)

	if isVirtual {
		return m.handleAllViewTasks(msg)
	}

	switch msg.String() {
	case "esc", "left", "h":
		m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
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
		cmd := m.input.Focus()
		return m, cmd
	case "r":
		if m.taskCursor < len(m.tasks.Todos) {
			m.inputMode = inputRenameTask
			m.input.SetValue(m.tasks.Todos[m.taskCursor].Title)
			cmd := m.input.Focus()
			return m, cmd
		}
	case "ctrl+x":
		if m.taskCursor < len(m.tasks.Todos) {
			if len(m.tasks.Todos[m.taskCursor].Subtasks) > 0 {
				m.confirmDelete = true
			} else {
				// Direct delete for tasks without subtasks
				m.pushUndoState()
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
	case "t":
		if m.taskCursor < len(m.tasks.Todos) {
			m.tasks.Todos[m.taskCursor].Today = !m.tasks.Todos[m.taskCursor].Today
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
		}
	case "A":
		// AI Priority Recommendation
		if !m.aiConfig.AIEnabled {
			return m, nil
		}
		if m.aiConfig.OpenAIAPIKey == "" {
			m.aiPriorityError = "AI não configurada. Defina OPENAI_API_KEY ou adicione a chave no config.json"
			m.aiPriorityResult = ""
			m.state = stateAIPriorityModal
			return m, nil
		}
		if len(m.tasks.Todos) == 0 {
			return m, nil
		}
		// Build task info list
		taskInfos := make([]ai.TaskInfo, len(m.tasks.Todos))
		for i, t := range m.tasks.Todos {
			taskInfos[i] = ai.TaskInfo{Title: t.Title, Done: t.Done, Today: t.Today}
		}
		sysPrompt := ai.BuildPrioritySystemPrompt(m.currentWorkspace, m.currentGroup, taskInfos)
		msgs := []ai.ChatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: "Qual tarefa devo fazer primeiro e por quê?"},
		}
		m.aiWaiting = true
		m.aiPriorityResult = ""
		m.aiPriorityError = ""
		m.state = stateAIPriorityModal
		return m, tea.Batch(sendAIPriorityRequest(m.aiConfig, msgs), m.aiSpinner.Tick)
	case "S":
		// AI Task Scanner
		if !m.aiConfig.AIEnabled {
			return m, nil
		}
		if m.aiConfig.OpenAIAPIKey == "" {
			m.aiScanError = "AI não configurada. Defina OPENAI_API_KEY ou adicione a chave no config.json"
			m.aiScanResult = ""
			m.state = stateAIScanModal
			return m, nil
		}
		if len(m.tasks.Todos) == 0 {
			return m, nil
		}
		taskInfos := make([]ai.TaskInfo, len(m.tasks.Todos))
		for i, t := range m.tasks.Todos {
			taskInfos[i] = ai.TaskInfo{Title: t.Title, Done: t.Done, Today: t.Today}
		}
		sysPrompt := ai.BuildScanSystemPrompt(m.currentWorkspace, m.currentGroup, taskInfos)
		msgs := []ai.ChatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: "Por favor, analise essas tarefas e sugira melhorias ou divisão de subtarefas."},
		}
		m.aiWaiting = true
		m.aiScanResult = ""
		m.aiScanError = ""
		m.state = stateAIScanModal
		return m, tea.Batch(sendAIScanRequest(m.aiConfig, msgs), m.aiSpinner.Tick)
	}
	return m, nil
}

// ── ALL VIEW TASK HANDLER ───────────────────────────────────────────────────────

func (m model) handleAllViewTasks(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "left", "h":
		m.groups, m.workspaceMeta = loadGroupsWithMeta(m.currentWorkspace)
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
				if m.currentGroup == "FAVORITES" {
					m.buildFavViewItems()
				} else {
					m.buildAllViewItems()
				}
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
				if m.currentGroup == "FAVORITES" {
					m.buildFavViewItems()
				} else {
					m.buildAllViewItems()
				}
			}
		}
	}
	return m, nil
}

// ── TODAY VIEW HANDLER ──────────────────────────────────────────────────────────

func (m model) handleTodayView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "left", "h":
		m.state = stateViewWorkspaces
	case "up", "k":
		if m.taskCursor > 0 {
			m.taskCursor--
		}
	case "down", "j":
		if m.taskCursor < len(m.todayItems)-1 {
			m.taskCursor++
		}
	case " ":
		if m.taskCursor < len(m.todayItems) {
			e := m.todayItems[m.taskCursor]
			gf := loadGroupFile(e.workspace, e.group)
			if e.taskIndex < len(gf.Todos) {
				gf.Todos[e.taskIndex].Done = !gf.Todos[e.taskIndex].Done
				saveGroupFile(e.workspace, e.group, gf)
				m.buildTodayItems()
				if m.taskCursor >= len(m.todayItems) && m.taskCursor > 0 {
					m.taskCursor = len(m.todayItems) - 1
				}
			}
		}
	case "t":
		// Un-mark from Today
		if m.taskCursor < len(m.todayItems) {
			e := m.todayItems[m.taskCursor]
			gf := loadGroupFile(e.workspace, e.group)
			if e.taskIndex < len(gf.Todos) {
				gf.Todos[e.taskIndex].Today = false
				saveGroupFile(e.workspace, e.group, gf)
				m.buildTodayItems()
				if m.taskCursor >= len(m.todayItems) && m.taskCursor > 0 {
					m.taskCursor = len(m.todayItems) - 1
				}
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
		cmd := m.input.Focus()
		return m, cmd
	case "r":
		// Subtask rename if cursor is on a subtask, otherwise rename task title
		if len(task.Subtasks) > 0 && m.subtaskCursor < len(task.Subtasks) {
			m.inputMode = inputRenameSubtask
			m.input.SetValue(task.Subtasks[m.subtaskCursor].Title)
		} else {
			m.inputMode = inputRenameTaskTitle
			m.input.SetValue(task.Title)
		}
		cmd := m.input.Focus()
		return m, cmd
	case "R":
		// Always rename the parent task title
		m.inputMode = inputRenameTaskTitle
		m.input.SetValue(task.Title)
		cmd := m.input.Focus()
		return m, cmd
	case "d":
		m.inputMode = inputEditDescription
		m.input.Placeholder = "Type the description..."
		m.input.SetValue(task.Description)
		cmd := m.input.Focus()
		return m, cmd
	case "ctrl+x":
		if m.subtaskCursor < len(task.Subtasks) {
			m.pushUndoState()
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
	case "a":
		// Open AI Agent Panel
		if !m.aiConfig.AIEnabled {
			return m, nil
		}
		if m.aiConfig.OpenAIAPIKey == "" {
			// Show error as a message in the AI panel
			m.aiMessages = []AIMessage{
				{Role: "system", Content: "⚠ AI não configurada. Defina OPENAI_API_KEY ou adicione a chave no config.json"},
			}
			m.aiPendingSubtasks = nil
			m.aiScrollOffset = 0
			m.aiInput.Reset()
			m.state = stateAIPanel
			cmd := m.aiInput.Focus()
			return m, cmd
		}
		// Load existing conversation from task
		m.aiMessages = task.AIConversation
		if m.aiMessages == nil {
			m.aiMessages = []AIMessage{}
		}
		m.aiPendingSubtasks = nil
		m.aiScrollOffset = 0
		m.aiInput.Reset()
		m.state = stateAIPanel
		cmd := m.aiInput.Focus()

		// If fresh conversation, trigger proactive AI analysis
		if len(m.aiMessages) == 0 {
			initMsg := ai.BuildInitialAnalysisMessage()
			m.aiMessages = append(m.aiMessages, AIMessage{Role: "user", Content: initMsg})
			m.aiWaiting = true
			apiMsgs := m.buildAIChatMessages()
			return m, tea.Batch(cmd, sendAIRequest(m.aiConfig, apiMsgs), m.aiSpinner.Tick)
		}
		return m, cmd
	}
	return m, nil
}

// ── AI PANEL HANDLER ────────────────────────────────────────────────────────────

func (m model) handleAIPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.aiInput.Blur()
		m.state = stateTaskDetails
		return m, nil
	case tea.KeyEnter:
		if m.aiWaiting {
			// Don't send while waiting for response
			return m, nil
		}
		userText := strings.TrimSpace(m.aiInput.Value())
		if userText == "" {
			return m, nil
		}
		m.aiInput.Reset()

		// Check if user is confirming pending subtasks
		if len(m.aiPendingSubtasks) > 0 && ai.IsConfirmation(userText) {
			// Add subtasks programmatically
			if m.taskCursor < len(m.tasks.Todos) {
				for _, title := range m.aiPendingSubtasks {
					m.tasks.Todos[m.taskCursor].Subtasks = append(
						m.tasks.Todos[m.taskCursor].Subtasks,
						Subtask{Title: title, Done: false},
					)
				}
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)

				// Add confirmation message to chat
				confirmMsg := fmt.Sprintf("✅ %d subtarefas adicionadas com sucesso!", len(m.aiPendingSubtasks))
				m.aiMessages = append(m.aiMessages, AIMessage{Role: "user", Content: userText})
				m.aiMessages = append(m.aiMessages, AIMessage{Role: "system", Content: confirmMsg})
				m.aiPendingSubtasks = nil

				// Persist conversation
				m.tasks.Todos[m.taskCursor].AIConversation = m.aiMessages
				saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			}
			m.aiScrollOffset = 0
			return m, nil
		}

		// Normal message — send to AI
		m.aiPendingSubtasks = nil
		m.aiMessages = append(m.aiMessages, AIMessage{Role: "user", Content: userText})
		m.aiWaiting = true
		apiMsgs := m.buildAIChatMessages()
		return m, tea.Batch(sendAIRequest(m.aiConfig, apiMsgs), m.aiSpinner.Tick)

	case tea.KeyUp:
		// Scroll up in conversation
		m.aiScrollOffset++
		return m, nil
	case tea.KeyDown:
		// Scroll down in conversation
		if m.aiScrollOffset > 0 {
			m.aiScrollOffset--
		}
		return m, nil
	}

	// Forward other keys to text input
	var cmd tea.Cmd
	m.aiInput, cmd = m.aiInput.Update(msg)
	return m, cmd
}

// ── AI PRIORITY MODAL HANDLER ───────────────────────────────────────────────────

func (m model) handleAIPriorityModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.aiWaiting {
		// While waiting, only allow Esc to cancel
		if msg.Type == tea.KeyEsc {
			m.aiWaiting = false
			m.state = stateViewTasks
		}
		return m, nil
	}
	// Any key closes the modal
	m.state = stateViewTasks
	return m, nil
}

// ── AI SCAN MODAL HANDLER ───────────────────────────────────────────────────────

func (m model) handleAIScanModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.aiWaiting {
		if msg.Type == tea.KeyEsc {
			m.aiWaiting = false
			m.state = stateViewTasks
		}
		return m, nil
	}
	
	if msg.Type == tea.KeyEsc {
		m.state = stateViewTasks
		return m, nil
	}

	if len(m.aiScanSuggestions) > 0 {
		switch msg.String() {
		case "up", "k":
			if m.aiScanCursor > 0 {
				m.aiScanCursor--
			}
		case "down", "j":
			if m.aiScanCursor < len(m.aiScanSuggestions)-1 {
				m.aiScanCursor++
			}
		case "enter":
			sug := m.aiScanSuggestions[m.aiScanCursor]
			m.applyScanSuggestion(sug)
			m.aiScanSuggestions = append(m.aiScanSuggestions[:m.aiScanCursor], m.aiScanSuggestions[m.aiScanCursor+1:]...)
			if m.aiScanCursor >= len(m.aiScanSuggestions) && m.aiScanCursor > 0 {
				m.aiScanCursor--
			}
		}
	} else if msg.Type == tea.KeyEnter {
		m.state = stateViewTasks
	}

	return m, nil
}

func (m *model) applyScanSuggestion(sug ai.ScanSuggestion) {
	m.pushUndoState()
	for i, t := range m.tasks.Todos {
		if t.Title == sug.OriginalTitle {
			if sug.Type == "RENAME" {
				m.tasks.Todos[i].Title = sug.Suggestion
			} else if sug.Type == "SUBTASKS" {
				subNames := strings.Split(sug.Suggestion, "|")
				for _, sn := range subNames {
					m.tasks.Todos[i].Subtasks = append(m.tasks.Todos[i].Subtasks, Subtask{Title: sn, Done: false})
				}
			}
			saveGroupFile(m.currentWorkspace, m.currentGroup, m.tasks)
			break
		}
	}
}

// ── AI CONFIRM ACTION HANDLER ───────────────────────────────────────────────────

func (m model) handleAIConfirmAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	str := strings.ToLower(msg.String())
	if str == "y" || str == "s" {
		m.executePendingAction()
		m.pendingAction = nil
		m.state = m.prevState // return to global console
		m.aiMessages = append(m.aiMessages, AIMessage{Role: "system", Content: "Ação executada com sucesso. Informe ao usuário e continue."})
		// Auto-send a message to update the AI on the success
		apiMsgs := make([]ai.ChatMessage, len(m.aiMessages))
		for i, ms := range m.aiMessages {
			apiMsgs[i] = ai.ChatMessage{Role: ms.Role, Content: ms.Content}
		}
		m.aiWaiting = true
		return m, tea.Batch(sendAIGlobalRequest(m.aiConfig, apiMsgs), m.aiSpinner.Tick)
	} else if str == "n" || msg.Type == tea.KeyEsc {
		m.pendingAction = nil
		m.state = m.prevState // return to global console
		m.aiMessages = append(m.aiMessages, AIMessage{Role: "system", Content: "O usuário recusou a execução da ação."})
		return m, nil
	}
	return m, nil
}

func (m *model) executePendingAction() {
	if m.pendingAction == nil {
		return
	}
	action := m.pendingAction

	// Check if group exists
	groupExists := false
	for _, g := range m.groups {
		if g == action.Group {
			groupExists = true
			break
		}
	}
	if !groupExists {
		return
	}
	gf := loadGroupFile(m.currentWorkspace, action.Group)

	switch action.Type {
	case "ADD_TASK":
		m.pushUndoState()
		gf.Todos = append(gf.Todos, Todo{Title: action.Title})
		saveGroupFile(m.currentWorkspace, action.Group, gf)
	case "DELETE_TASK":
		m.pushUndoState()
		for i, t := range gf.Todos {
			if t.Title == action.Title {
				gf.Todos = append(gf.Todos[:i], gf.Todos[i+1:]...)
				saveGroupFile(m.currentWorkspace, action.Group, gf)
				break
			}
		}
	case "RENAME_TASK":
		m.pushUndoState()
		for i, t := range gf.Todos {
			if t.Title == action.OldTitle {
				gf.Todos[i].Title = action.NewTitle
				saveGroupFile(m.currentWorkspace, action.Group, gf)
				break
			}
		}
	case "MARK_DONE":
		m.pushUndoState()
		for i, t := range gf.Todos {
			if t.Title == action.Title {
				gf.Todos[i].Done = true
				saveGroupFile(m.currentWorkspace, action.Group, gf)
				break
			}
		}
	}

	if m.currentGroup == action.Group {
		m.tasks = gf
	}
}

// ── GLOBAL AI CONSOLE HANDLER ───────────────────────────────────────────────────

func (m model) handleAIGlobalConsole(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.aiInput.Blur()
		m.state = m.prevState
		return m, nil
	case tea.KeyEnter:
		if m.aiWaiting {
			return m, nil
		}
		userText := strings.TrimSpace(m.aiInput.Value())
		if userText == "" {
			return m, nil
		}
		m.aiInput.Reset()
		
		if len(m.aiMessages) == 0 {
			allTasks := make(map[string][]ai.TaskInfo)
			for _, g := range m.groups {
				if isVirtualGroup(g) {
					continue
				}
				gf := loadGroupFile(m.currentWorkspace, g)
				var tasks []ai.TaskInfo
				for _, t := range gf.Todos {
					tasks = append(tasks, ai.TaskInfo{Title: t.Title, Done: t.Done, Today: t.Today})
				}
				allTasks[g] = tasks
			}
			m.aiMessages = append(m.aiMessages, AIMessage{Role: "system", Content: ai.BuildOmniSystemPrompt(m.workspaces, m.currentWorkspace, m.currentGroup, allTasks)})
		}
		m.aiMessages = append(m.aiMessages, AIMessage{Role: "user", Content: userText})
		m.aiWaiting = true
		
		apiMsgs := make([]ai.ChatMessage, len(m.aiMessages))
		for i, ms := range m.aiMessages {
			apiMsgs[i] = ai.ChatMessage{Role: ms.Role, Content: ms.Content}
		}

		return m, tea.Batch(sendAIGlobalRequest(m.aiConfig, apiMsgs), m.aiSpinner.Tick)
	case tea.KeyUp:
		m.aiScrollOffset++
		return m, nil
	case tea.KeyDown:
		if m.aiScrollOffset > 0 {
			m.aiScrollOffset--
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.aiInput, cmd = m.aiInput.Update(msg)
	return m, cmd
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
		if isVirtualGroup(m.currentGroup) {
			m.viewAllTasks(&s, contentHeight)
		} else {
			m.viewTasks(&s, contentHeight)
		}
	case stateTaskDetails:
		m.viewTaskDetails(&s, contentHeight)
	case stateGitConsole:
		m.viewGitConsole(&s, contentHeight)
	case stateTodayView:
		m.viewToday(&s, contentHeight)
	case stateAIPanel:
		m.viewAIPanel(&s, contentHeight)
	case stateAIPriorityModal:
		m.viewAIPriorityModal(&s, contentHeight)
	case stateAIScanModal:
		m.viewAIScanModal(&s, contentHeight)
	case stateAIGlobalConsole:
		m.viewAIGlobalConsole(&s, contentHeight)
	case stateAIConfirmAction:
		m.viewAIConfirmAction(&s, contentHeight)
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
		borderColor = m.getGroupColor(m.groupCursor, m.currentGroup)
	case stateGitConsole:
		borderColor = lipgloss.Color("#FFD700")
	case stateTodayView:
		borderColor = lipgloss.Color("#00CED1")
	case stateAIPanel, stateAIPriorityModal, stateAIScanModal, stateAIGlobalConsole, stateAIConfirmAction:
		borderColor = m.getGroupColor(m.groupCursor, m.currentGroup)
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
		s.WriteString(faintStyle.Render("  ↑↓: Nav • →/Enter: Open • n: New • r: Rename • Ctrl+x: Del • T: Today • g: Git"))
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

		// For virtual groups, show aggregate counts
		var done, total int
		if g == "ALL" {
			done, total = countWorkspaceTasks(m.currentWorkspace)
		} else if g == "FAVORITES" {
			for _, gm := range m.workspaceMeta.Groups {
				if gm.IsFavorite {
					d, t := countGroupTasks(m.currentWorkspace, gm.Name)
					done += d
					total += t
				}
			}
		} else {
			done, total = countGroupTasks(m.currentWorkspace, g)
		}

		// Check if group is favorited
		favStar := ""
		if !isVirtualGroup(g) {
			for _, gm := range m.workspaceMeta.Groups {
				if gm.Name == g && gm.IsFavorite {
					favStar = " ★"
					break
				}
			}
		}

		label := fmt.Sprintf("%s (%d/%d)%s", g, done, total, favStar)

		color := m.getGroupColor(i, g)
		if isVirtualGroup(g) {
			color = lipgloss.Color("250")
		}
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
		s.WriteString(faintStyle.Render("  ←: Back • ↑↓: Nav • Enter: Open • n: New • r: Rename • f: Fav • c: Color • Ctrl+x: Del"))
	}
}

// ── VIEW: TASKS ─────────────────────────────────────────────────────────────────

func (m model) viewTasks(s *strings.Builder, maxH int) {
	color := m.getGroupColor(m.groupCursor, m.currentGroup)
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
				tStyle = tStyle.Strikethrough(true).Foreground(lipgloss.Color("246"))
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

	// Description preview for selected task
	if m.taskCursor < len(m.tasks.Todos) {
		desc := m.tasks.Todos[m.taskCursor].Description
		if desc != "" {
			descStyle := lipgloss.NewStyle().Faint(true).Italic(true).
				Foreground(lipgloss.Color("245")).PaddingLeft(2)
			lines := strings.SplitN(desc, "\n", 4)
			if len(lines) > 3 {
				lines = lines[:3]
				lines = append(lines, "...")
			}
			s.WriteString(descStyle.Render("\U0001F4DD "+strings.Join(lines, "\n   ")) + "\n")
		}
		// Today indicator
		if m.tasks.Todos[m.taskCursor].Today {
			s.WriteString(lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("#00CED1")).PaddingLeft(2).Render("\U0001F4CC marked for Today") + "\n")
		}
	}

	if m.inputMode == inputAddTask || m.inputMode == inputRenameTask {
		s.WriteString("  " + m.input.View())
	} else {
		s.WriteString(faintStyle.Render("  ←: Back • Space: ✓ • →/Enter: Details • n: New • r: Rename • t: Today • A: AI Priority • Ctrl+x: Del"))
	}
}

// ── VIEW: ALL VIEW ──────────────────────────────────────────────────────────────

func (m model) viewAllTasks(s *strings.Builder, maxH int) {
	viewTitle := "  All Tasks"
	if m.currentGroup == "FAVORITES" {
		viewTitle = "  ★ Favorites"
	}
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render(viewTitle),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(getColor(m.workspaceCursor)).Bold(true).Render(m.currentWorkspace),
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
				color := m.getGroupColor(gIdx, entry.groupName)
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
						tStyle = tStyle.Strikethrough(true).Foreground(lipgloss.Color("246"))
					} else {
						tStyle = tStyle.Foreground(lipgloss.Color("255"))
					}
					gIdx := indexOf(m.groups, entry.groupName)
					color := m.getGroupColor(gIdx, entry.groupName)
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

// ── VIEW: TODAY ──────────────────────────────────────────────────────────────────

func (m model) viewToday(s *strings.Builder, maxH int) {
	header := titleStyle.Copy().
		Background(lipgloss.Color("#00CED1")).
		Foreground(lipgloss.Color("0")).
		Render("  \U0001F4CC Today")
	s.WriteString(header + "\n\n")

	if len(m.todayItems) == 0 {
		s.WriteString(faintStyle.Render("  No tasks marked for today.") + "\n")
		s.WriteString(faintStyle.Render("  Press 't' on any task to mark it.") + "\n")
	} else {
		visibleStart, visibleEnd := scrollWindow(m.taskCursor, len(m.todayItems), maxH-4)

		for i := visibleStart; i < visibleEnd; i++ {
			e := m.todayItems[i]
			gf := loadGroupFile(e.workspace, e.group)
			if e.taskIndex >= len(gf.Todos) {
				continue
			}
			t := gf.Todos[e.taskIndex]

			cursor := "  "
			if m.taskCursor == i {
				cursor = "> "
			}

			check := "[ ]"
			if t.Done {
				check = "[x]"
			}

			context := fmt.Sprintf("[%s > %s]", e.workspace, e.group)
			contextStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("245"))

			tStyle := lipgloss.NewStyle()
			if t.Done {
				tStyle = tStyle.Strikethrough(true).Foreground(lipgloss.Color("246"))
			} else {
				tStyle = tStyle.Foreground(lipgloss.Color("255"))
			}

			if m.taskCursor == i {
				tStyle = tStyle.Foreground(lipgloss.Color("#00CED1"))
				cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("#00CED1")).Render(cursor)
			}

			label := fmt.Sprintf("%s %s %s", check, t.Title, contextStyle.Render(context))
			s.WriteString(fmt.Sprintf("%s%s\n", cursor, tStyle.Render(label)))
		}

		if visibleEnd < len(m.todayItems) {
			s.WriteString(faintStyle.Render(fmt.Sprintf("  ... +%d more", len(m.todayItems)-visibleEnd)) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(faintStyle.Render("  ←/Esc: Back • ↑↓: Nav • Space: ✓ • t: Remove from Today"))
}

// ── VIEW: TASK DETAILS ──────────────────────────────────────────────────────────

func (m model) viewTaskDetails(s *strings.Builder, maxH int) {
	if m.taskCursor >= len(m.tasks.Todos) {
		s.WriteString("No task selected.\n")
		return
	}
	task := m.tasks.Todos[m.taskCursor]
	color := m.getGroupColor(m.groupCursor, m.currentGroup)

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
				stStyle = stStyle.Strikethrough(true).Foreground(lipgloss.Color("246"))
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
	if m.inputMode == inputAddSubtask || m.inputMode == inputRenameTaskTitle || m.inputMode == inputEditDescription || m.inputMode == inputRenameSubtask {
		s.WriteString("  " + m.input.View())
	} else {
		s.WriteString(faintStyle.Render("  ←/Esc: Back • r: Rename • R: Rename Task • d: Description • n: Subtask • a: AI Agent • Space: ✓ • Ctrl+x: Del"))
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

// ── VIEW: AI PANEL ──────────────────────────────────────────────────────────────

func (m model) viewAIPanel(s *strings.Builder, maxH int) {
	color := m.getGroupColor(m.groupCursor, m.currentGroup)

	// Header with breadcrumb
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  🤖 AI Agent"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(
			m.currentWorkspace+" > "+m.currentGroup,
		),
	)
	s.WriteString(header + "\n")

	// Task title context
	if m.taskCursor < len(m.tasks.Todos) {
		taskTitle := m.tasks.Todos[m.taskCursor].Title
		s.WriteString(lipgloss.NewStyle().Faint(true).PaddingLeft(2).Render("Tarefa: "+taskTitle) + "\n")
	}
	s.WriteString("\n")

	// Calculate available height for messages
	// Reserve: header(2) + task context(1) + blank(1) + input(1) + help(1) + blank(1) = 7
	msgHeight := maxH - 7
	if msgHeight < 3 {
		msgHeight = 3
	}

	// Build message lines
	var msgLines []string
	for _, msg := range m.aiMessages {
		// Skip the hidden initial analysis message
		if msg.Role == "user" && msg.Content == ai.BuildInitialAnalysisMessage() {
			continue
		}

		var prefix string
		var style lipgloss.Style

		switch msg.Role {
		case "user":
			prefix = "  Você > "
			style = lipgloss.NewStyle().Foreground(color)
		case "assistant":
			prefix = "  AI > "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		case "system":
			prefix = "  "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Italic(true)
		}

		// Word-wrap long messages
		content := msg.Content
		// Strip [SUBTASKS]...[/SUBTASKS] tags for display
		content = strings.ReplaceAll(content, "[SUBTASKS]", "")
		content = strings.ReplaceAll(content, "[/SUBTASKS]", "")

		contentLines := strings.Split(content, "\n")
		for j, line := range contentLines {
			if j == 0 {
				msgLines = append(msgLines, style.Render(prefix+line))
			} else {
				// Continuation lines with indent
				indent := strings.Repeat(" ", len(prefix))
				msgLines = append(msgLines, style.Render(indent+line))
			}
		}
		msgLines = append(msgLines, "") // blank line between messages
	}

	// Show spinner if waiting
	if m.aiWaiting {
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
		msgLines = append(msgLines, spinnerStyle.Render("  "+m.aiSpinner.View()+" Pensando..."))
	}

	// Apply scroll offset and visible window
	totalLines := len(msgLines)
	visibleEnd := totalLines - m.aiScrollOffset
	if visibleEnd < 0 {
		visibleEnd = 0
	}
	visibleStart := visibleEnd - msgHeight
	if visibleStart < 0 {
		visibleStart = 0
	}
	if visibleEnd > totalLines {
		visibleEnd = totalLines
	}

	// Clamp scroll offset
	maxScroll := totalLines - msgHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.aiScrollOffset > maxScroll {
		m.aiScrollOffset = maxScroll
	}

	// Render visible messages
	renderedLines := 0
	for i := visibleStart; i < visibleEnd; i++ {
		s.WriteString(msgLines[i] + "\n")
		renderedLines++
	}

	// Pad remaining space
	for renderedLines < msgHeight {
		s.WriteString("\n")
		renderedLines++
	}

	// Scroll indicator
	if m.aiScrollOffset > 0 {
		s.WriteString(faintStyle.Render(fmt.Sprintf("  ↑ %d linhas acima", m.aiScrollOffset)) + "\n")
	}

	// Input area with separator
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	s.WriteString(sepStyle.Render("  ────────────────────────────────────────") + "\n")
	s.WriteString("  " + m.aiInput.View() + "\n")

	// Help
	s.WriteString(faintStyle.Render("  Enter: Enviar • Esc: Fechar • ↑↓: Rolar"))
}

// ── VIEW: AI PRIORITY MODAL ─────────────────────────────────────────────────────

func (m model) viewAIPriorityModal(s *strings.Builder, maxH int) {
	color := m.getGroupColor(m.groupCursor, m.currentGroup)

	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  🎯 Recomendação de Prioridade"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(m.currentGroup),
	)
	s.WriteString(header + "\n\n")

	if m.aiWaiting {
		// Show spinner while waiting
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
		s.WriteString(spinnerStyle.Render("  "+m.aiSpinner.View()+" Analisando tarefas...") + "\n\n")
		s.WriteString(faintStyle.Render("  Esc: Cancelar"))
		return
	}

	if m.aiPriorityError != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6347"))
		s.WriteString(errStyle.Render("  ⚠ "+m.aiPriorityError) + "\n\n")
	} else if m.aiPriorityResult != "" {
		resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).PaddingLeft(2)
		lines := strings.Split(m.aiPriorityResult, "\n")
		for _, line := range lines {
			s.WriteString(resultStyle.Render(line) + "\n")
		}
		s.WriteString("\n")
	}

	s.WriteString(faintStyle.Render("  Pressione qualquer tecla para fechar"))
}

// ── VIEW: AI SCAN MODAL ─────────────────────────────────────────────────────────

func (m model) viewAIScanModal(s *strings.Builder, maxH int) {
	color := m.getGroupColor(m.groupCursor, m.currentGroup)

	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  🔍 Scanner Inteligente de Tarefas"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  "),
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(m.currentGroup),
	)
	s.WriteString(header + "\n\n")

	if m.aiWaiting {
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
		s.WriteString(spinnerStyle.Render("  "+m.aiSpinner.View()+" Escaneando tarefas e gerando sugestões...") + "\n\n")
		s.WriteString(faintStyle.Render("  Esc: Cancelar"))
		return
	}

	if m.aiScanError != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6347"))
		s.WriteString(errStyle.Render("  ⚠ "+m.aiScanError) + "\n\n")
	} else if len(m.aiScanSuggestions) > 0 {
		s.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render("Sugestões encontradas:") + "\n\n")
		for i, sug := range m.aiScanSuggestions {
			cursor := "  "
			itemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
			if i == m.aiScanCursor {
				cursor = "▶ "
				itemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
			}
			s.WriteString(itemStyle.Render(fmt.Sprintf("%s%s", cursor, sug.OriginalTitle)) + "\n")
			
			actionText := ""
			if sug.Type == "RENAME" {
				actionText = "Renomear para: " + sug.Suggestion
			} else {
				actionText = "Criar subtarefas: " + strings.ReplaceAll(sug.Suggestion, "|", ", ")
			}
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("62")).PaddingLeft(4).Render(actionText) + "\n\n")
		}
	} else {
		resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).PaddingLeft(2)
		s.WriteString(resultStyle.Render("Nenhuma sugestão ou todas as sugestões foram aplicadas.") + "\n\n")
	}

	s.WriteString(faintStyle.Render("  Enter: Aplicar selecionada • Esc: Fechar • ↑↓: Navegar"))
}

// ── VIEW: GLOBAL AI CONSOLE ─────────────────────────────────────────────────────

func (m model) viewAIGlobalConsole(s *strings.Builder, maxH int) {
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  🌍 Console Global da IA (Omni-chat)"),
	)
	s.WriteString(header + "\n\n")

	msgHeight := maxH - 5
	if msgHeight < 3 {
		msgHeight = 3
	}

	var msgLines []string
	for _, msg := range m.aiMessages {
		if msg.Role == "system" {
			continue
		}
		var prefix string
		var style lipgloss.Style
		switch msg.Role {
		case "user":
			prefix = "  Você > "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("62"))
		case "assistant":
			prefix = "  AI > "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		}

		contentLines := strings.Split(msg.Content, "\n")
		for j, line := range contentLines {
			if j == 0 {
				msgLines = append(msgLines, style.Render(prefix+line))
			} else {
				indent := strings.Repeat(" ", len(prefix))
				msgLines = append(msgLines, style.Render(indent+line))
			}
		}
		msgLines = append(msgLines, "")
	}

	if m.aiGlobalError != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6347"))
		msgLines = append(msgLines, errStyle.Render("  ⚠ "+m.aiGlobalError))
	}

	if m.aiWaiting {
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
		msgLines = append(msgLines, spinnerStyle.Render("  "+m.aiSpinner.View()+" Executando..."))
	}

	totalLines := len(msgLines)
	visibleEnd := totalLines - m.aiScrollOffset
	if visibleEnd < 0 {
		visibleEnd = 0
	}
	visibleStart := visibleEnd - msgHeight
	if visibleStart < 0 {
		visibleStart = 0
	}
	if visibleEnd > totalLines {
		visibleEnd = totalLines
	}

	maxScroll := totalLines - msgHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.aiScrollOffset > maxScroll {
		m.aiScrollOffset = maxScroll
	}

	renderedLines := 0
	for i := visibleStart; i < visibleEnd; i++ {
		s.WriteString(msgLines[i] + "\n")
		renderedLines++
	}
	for renderedLines < msgHeight {
		s.WriteString("\n")
		renderedLines++
	}

	if m.aiScrollOffset > 0 {
		s.WriteString(faintStyle.Render(fmt.Sprintf("  ↑ %d linhas acima", m.aiScrollOffset)) + "\n")
	}

	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	s.WriteString(sepStyle.Render("  ────────────────────────────────────────") + "\n")
	s.WriteString("  " + m.aiInput.View() + "\n")
	s.WriteString(faintStyle.Render("  Enter: Enviar • Esc: Fechar • ↑↓: Rolar"))
}

// ── VIEW: AI CONFIRM ACTION ─────────────────────────────────────────────────────

func (m model) viewAIConfirmAction(s *strings.Builder, maxH int) {
	color := lipgloss.Color("#FF6347") // red for caution

	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("  ⚠ Confirmação de Ação da IA"),
	)
	s.WriteString(header + "\n\n")

	if m.pendingAction != nil {
		actionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).PaddingLeft(2)
		s.WriteString(actionStyle.Render("A IA deseja executar a seguinte ação:") + "\n\n")
		
		s.WriteString(actionStyle.Render(fmt.Sprintf("Tipo:  %s", m.pendingAction.Type)) + "\n")
		s.WriteString(actionStyle.Render(fmt.Sprintf("Grupo: %s", m.pendingAction.Group)) + "\n")
		
		switch m.pendingAction.Type {
		case "ADD_TASK", "DELETE_TASK", "MARK_DONE":
			s.WriteString(actionStyle.Render(fmt.Sprintf("Tarefa: %s", m.pendingAction.Title)) + "\n")
		case "RENAME_TASK":
			s.WriteString(actionStyle.Render(fmt.Sprintf("De:   %s", m.pendingAction.OldTitle)) + "\n")
			s.WriteString(actionStyle.Render(fmt.Sprintf("Para: %s", m.pendingAction.NewTitle)) + "\n")
		}
	}

	s.WriteString("\n" + lipgloss.NewStyle().Foreground(color).Render("  Deseja permitir esta ação no seu banco de dados?") + "\n")
	s.WriteString(faintStyle.Render("  [y] Sim  [n] Não"))
}

type aiScanResponseMsg struct {
	Content string
	Err     error
}

type aiGlobalResponseMsg struct {
	Content string
	Err     error
}

func sendAIScanRequest(config ai.Config, msgs []ai.ChatMessage) tea.Cmd {
	return func() tea.Msg {
		resp, err := ai.CallOpenAI(config.OpenAIAPIKey, config.OpenAIModel, msgs)
		return aiScanResponseMsg{Content: resp, Err: err}
	}
}

func sendAIGlobalRequest(config ai.Config, msgs []ai.ChatMessage) tea.Cmd {
	return func() tea.Msg {
		resp, err := ai.CallOpenAI(config.OpenAIAPIKey, config.OpenAIModel, msgs)
		return aiGlobalResponseMsg{Content: resp, Err: err}
	}
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
