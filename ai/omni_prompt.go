package ai

import "strings"

func BuildOmniSystemPrompt(workspaces []string, currentWorkspace, currentGroup string, allTasks map[string][]TaskInfo) string {
	var sb strings.Builder

	sb.WriteString("Você é a IA Global do MAU-toDoTUI, um gerenciador de tarefas por terminal.\n")
	sb.WriteString("Sua principal função é auxiliar o usuário a gerenciar suas tarefas e executar ações automatizadas no sistema.\n\n")

	sb.WriteString("## Contexto Atual\n")
	sb.WriteString("- Workspace ativo: ")
	sb.WriteString(currentWorkspace)
	sb.WriteString("\n- Grupo ativo: ")
	sb.WriteString(currentGroup)
	sb.WriteString("\n- Workspaces existentes: ")
	sb.WriteString(strings.Join(workspaces, ", "))
	sb.WriteString("\n\n")

	sb.WriteString("## Tarefas do Workspace Atual\n")
	for group, tasks := range allTasks {
		sb.WriteString("### Grupo: ")
		sb.WriteString(group)
		sb.WriteString("\n")
		if len(tasks) == 0 {
			sb.WriteString("(Nenhuma tarefa)\n")
		} else {
			for _, t := range tasks {
				status := "pendente"
				if t.Done {
					status = "concluída"
				}
				sb.WriteString("- ")
				sb.WriteString(t.Title)
				sb.WriteString(" (")
				sb.WriteString(status)
				sb.WriteString(")\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Ações Permitidas (Obrigatório seguir este formato)\n")
	sb.WriteString("Se você decidir que precisa realizar uma alteração no sistema (como adicionar, renomear ou marcar tarefas), você DEVE retornar a ação formatada EXATAMENTE com o bloco `[ACTION: NOME_DA_ACAO]` seguido por um JSON na próxima linha.\n\n")
	sb.WriteString("Ações disponíveis:\n")
	sb.WriteString("1. `[ACTION: ADD_TASK]`\n```json\n{\"group\": \"nome_do_grupo\", \"title\": \"titulo_da_tarefa\"}\n```\n")
	sb.WriteString("2. `[ACTION: RENAME_TASK]`\n```json\n{\"group\": \"nome_do_grupo\", \"old_title\": \"titulo_antigo\", \"new_title\": \"novo_titulo\"}\n```\n")
	sb.WriteString("3. `[ACTION: DELETE_TASK]`\n```json\n{\"group\": \"nome_do_grupo\", \"title\": \"titulo_da_tarefa\"}\n```\n")
	sb.WriteString("4. `[ACTION: MARK_DONE]`\n```json\n{\"group\": \"nome_do_grupo\", \"title\": \"titulo_da_tarefa\"}\n```\n")
	sb.WriteString("\nAo sugerir uma ação, responda com uma breve explicação do porquê, e então insira o bloco de ação no final da mensagem.\n")
	sb.WriteString("Responda sempre de forma curta e direta em português.")

	return sb.String()
}
