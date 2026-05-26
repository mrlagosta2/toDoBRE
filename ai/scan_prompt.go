package ai

import (
	"encoding/json"
	"strings"
)

func BuildScanSystemPrompt(workspace, group string, tasks []TaskInfo) string {
	var sb strings.Builder

	sb.WriteString("Você é um otimizador de produtividade. Analise as tarefas abaixo do grupo '")
	sb.WriteString(group)
	sb.WriteString("' e sugira melhorias diretas.\n\n")

	sb.WriteString("## Tarefas Atuais\n")
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

	sb.WriteString("\n## Instruções\n")
	sb.WriteString("Você deve sugerir nomes mais acionáveis (tipo RENAME) ou divisão em subtarefas essenciais (tipo SUBTASKS).\n")
	sb.WriteString("Você DEVE retornar sua resposta EXATAMENTE no seguinte formato JSON, sem nenhum texto adicional fora do JSON:\n\n")
	sb.WriteString("```json\n")
	sb.WriteString("[\n")
	sb.WriteString("  {\"original_title\": \"título antigo 1\", \"suggestion\": \"Novo título acionável\", \"type\": \"RENAME\"},\n")
	sb.WriteString("  {\"original_title\": \"título antigo 2\", \"suggestion\": \"Primeira subtarefa|Segunda subtarefa|Terceira subtarefa\", \"type\": \"SUBTASKS\"}\n")
	sb.WriteString("]\n")
	sb.WriteString("```\n")
	sb.WriteString("Retorne no máximo 3 sugestões de alto impacto.\n")
	sb.WriteString("Se a lista já estiver perfeita, retorne um array JSON vazio: []")

	return sb.String()
}

// ScanSuggestion represents a suggested modification from the AI Scanner.
type ScanSuggestion struct {
	OriginalTitle string `json:"original_title"`
	Suggestion    string `json:"suggestion"` // New title or pipe-separated subtasks
	Type          string `json:"type"`       // RENAME or SUBTASKS
}

// ParseScanResponse extracts the JSON array of suggestions.
func ParseScanResponse(content string) []ScanSuggestion {
	startIdx := strings.Index(content, "[")
	endIdx := strings.LastIndex(content, "]")
	if startIdx == -1 || endIdx == -1 || startIdx > endIdx {
		return nil
	}

	jsonStr := content[startIdx : endIdx+1]

	var suggestions []ScanSuggestion
	err := json.Unmarshal([]byte(jsonStr), &suggestions)
	if err != nil {
		return nil
	}
	return suggestions
}
