package ai

import (
	"fmt"
	"strings"
)

// SubtaskInfo contains the minimal info needed to describe a subtask in prompts.
type SubtaskInfo struct {
	Title string
	Done  bool
}

// TaskInfo contains the minimal info needed to describe a task for priority analysis.
type TaskInfo struct {
	Title string
	Done  bool
	Today bool
}

// BuildTaskSystemPrompt constructs the system prompt for the AI agent panel,
// providing full context about the current task, its subtasks, and hierarchy.
// The prompt is in Portuguese as per user preference.
func BuildTaskSystemPrompt(workspace, group, taskTitle, taskDesc string, subtasks []SubtaskInfo) string {
	var sb strings.Builder

	sb.WriteString("Você é um assistente de produtividade inteligente integrado diretamente em um gerenciador de tarefas TUI.\n")
	sb.WriteString("Seu papel é ajudar o usuário a planejar, detalhar e executar suas tarefas de forma eficiente.\n\n")

	sb.WriteString("## Contexto Atual\n")
	sb.WriteString(fmt.Sprintf("- **Workspace:** %s\n", workspace))
	sb.WriteString(fmt.Sprintf("- **Grupo:** %s\n", group))
	sb.WriteString(fmt.Sprintf("- **Tarefa:** %s\n", taskTitle))

	if taskDesc != "" {
		sb.WriteString(fmt.Sprintf("- **Descrição:** %s\n", taskDesc))
	} else {
		sb.WriteString("- **Descrição:** (sem descrição)\n")
	}

	sb.WriteString("\n### Subtarefas Existentes\n")
	if len(subtasks) == 0 {
		sb.WriteString("Nenhuma subtarefa cadastrada.\n")
	} else {
		for i, st := range subtasks {
			status := "⬜ pendente"
			if st.Done {
				status = "✅ concluída"
			}
			sb.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, st.Title, status))
		}
	}

	sb.WriteString("\n## Comportamentos Esperados\n\n")

	sb.WriteString("### 1. Análise e Clarificação\n")
	sb.WriteString("Se a tarefa parecer vaga, genérica ou pouco especificada, faça 2-3 perguntas ")
	sb.WriteString("direcionadas para entender melhor o escopo antes de dar sugestões.\n\n")

	sb.WriteString("### 2. Quebra em Subtarefas\n")
	sb.WriteString("Quando solicitado (ou quando apropriado), proponha uma divisão da tarefa em subtarefas ")
	sb.WriteString("concretas e acionáveis. IMPORTANTE: ao propor subtarefas, use EXATAMENTE este formato:\n\n")
	sb.WriteString("```\n[SUBTASKS]\n1. Primeira subtarefa\n2. Segunda subtarefa\n3. Terceira subtarefa\n[/SUBTASKS]\n```\n\n")
	sb.WriteString("Após listar as subtarefas nesse formato, pergunte ao usuário se deseja adicioná-las.\n\n")

	sb.WriteString("### 3. Orientação Passo a Passo\n")
	sb.WriteString("Se o usuário disser que vai começar a tarefa, guie-o passo a passo. ")
	sb.WriteString("Apresente um passo de cada vez e aguarde confirmação antes de avançar.\n\n")

	sb.WriteString("### 4. Tom e Estilo\n")
	sb.WriteString("- Seja direto e objetivo, mas amigável\n")
	sb.WriteString("- Use formatação simples (sem markdown complexo — estamos em um terminal)\n")
	sb.WriteString("- Respostas curtas e focadas\n")
	sb.WriteString("- Sempre em português\n")

	return sb.String()
}

// BuildInitialAnalysisMessage returns the hidden initial user message
// that triggers the AI to proactively analyze the task on first open.
func BuildInitialAnalysisMessage() string {
	return "Analise a tarefa acima. Se ela for vaga ou pouco detalhada, " +
		"faça 2-3 perguntas de clarificação. Se já for clara o suficiente, " +
		"dê uma breve análise e pergunte como posso ajudar (ex: criar subtarefas, " +
		"guiar passo a passo, etc)."
}

// BuildPrioritySystemPrompt constructs the system prompt for the priority
// recommendation feature, listing all tasks in the current group.
func BuildPrioritySystemPrompt(workspace, group string, tasks []TaskInfo) string {
	var sb strings.Builder

	sb.WriteString("Você é um consultor de produtividade. Analise a lista de tarefas abaixo ")
	sb.WriteString("e recomende qual tarefa o usuário deveria fazer PRIMEIRO e por quê.\n\n")

	sb.WriteString(fmt.Sprintf("**Workspace:** %s\n", workspace))
	sb.WriteString(fmt.Sprintf("**Grupo:** %s\n\n", group))

	sb.WriteString("## Tarefas\n")
	pendingCount := 0
	for i, t := range tasks {
		status := "⬜ pendente"
		if t.Done {
			status = "✅ concluída"
		}
		todayMark := ""
		if t.Today {
			todayMark = " 📌 (marcada para hoje)"
		}
		sb.WriteString(fmt.Sprintf("%d. %s — %s%s\n", i+1, t.Title, status, todayMark))
		if !t.Done {
			pendingCount++
		}
	}

	if pendingCount == 0 {
		sb.WriteString("\nTodas as tarefas estão concluídas! Parabéns! 🎉\n")
	}

	sb.WriteString("\n## Instruções\n")
	sb.WriteString("- Considere urgência, dependências entre tarefas e complexidade\n")
	sb.WriteString("- Dê uma recomendação clara de qual fazer primeiro\n")
	sb.WriteString("- Explique brevemente o raciocínio\n")
	sb.WriteString("- Se houver tarefas marcadas para hoje, dê peso extra a elas\n")
	sb.WriteString("- Seja conciso (máximo 5-6 linhas)\n")
	sb.WriteString("- Sempre em português\n")

	return sb.String()
}

// ParseSubtaskProposal extracts subtask titles from an AI response that
// contains the [SUBTASKS]...[/SUBTASKS] block format.
// Returns nil if no such block is found.
func ParseSubtaskProposal(content string) []string {
	startTag := "[SUBTASKS]"
	endTag := "[/SUBTASKS]"

	startIdx := strings.Index(content, startTag)
	if startIdx == -1 {
		return nil
	}
	endIdx := strings.Index(content, endTag)
	if endIdx == -1 || endIdx <= startIdx {
		return nil
	}

	block := content[startIdx+len(startTag) : endIdx]
	lines := strings.Split(strings.TrimSpace(block), "\n")

	var subtasks []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip leading "1. ", "2. ", "- ", etc.
		cleaned := stripListPrefix(line)
		if cleaned != "" {
			subtasks = append(subtasks, cleaned)
		}
	}
	return subtasks
}

// stripListPrefix removes common list prefixes like "1. ", "- ", "* " from a line.
func stripListPrefix(line string) string {
	// Try numbered format: "1. ", "12. ", etc.
	for i, ch := range line {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '.' && i > 0 {
			rest := strings.TrimSpace(line[i+1:])
			if rest != "" {
				return rest
			}
		}
		break
	}

	// Try dash or asterisk prefix
	if strings.HasPrefix(line, "- ") {
		return strings.TrimSpace(line[2:])
	}
	if strings.HasPrefix(line, "* ") {
		return strings.TrimSpace(line[2:])
	}

	return line
}

// IsConfirmation checks if the user's input is a confirmation message
// (supporting both Portuguese and English common patterns).
func IsConfirmation(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	confirmWords := []string{
		"sim", "yes", "ok", "okay",
		"s", "y",
		"add", "add them", "adicionar", "adiciona",
		"confirma", "confirmar", "confirmo",
		"pode", "pode sim", "bora", "vai",
		"isso", "exato", "manda",
	}
	for _, word := range confirmWords {
		if lower == word {
			return true
		}
	}
	return false
}
