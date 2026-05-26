package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ChatMessage represents a single message in the OpenAI Chat Completions API.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the request payload for the OpenAI Chat Completions API.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

// chatChoice represents a single choice in the API response.
type chatChoice struct {
	Message ChatMessage `json:"message"`
}

// chatResponse is the response payload from the OpenAI Chat Completions API.
type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Error   *apiError    `json:"error,omitempty"`
}

// apiError represents an error returned by the OpenAI API.
type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

const openAIEndpoint = "https://api.openai.com/v1/chat/completions"

// CallOpenAI sends a chat completion request to the OpenAI API and returns
// the assistant's response content. It uses Go's standard net/http client
// with a 60-second timeout. Errors are returned as human-readable messages.
func CallOpenAI(apiKey, model string, messages []ChatMessage) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("AI não configurada. Defina OPENAI_API_KEY ou adicione a chave no config.json")
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	reqBody := chatRequest{
		Model:    model,
		Messages: messages,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("Erro interno ao preparar requisição: %v", err)
	}

	req, err := http.NewRequest("POST", openAIEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("Erro interno ao criar requisição: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Erro de rede: não foi possível conectar à API da OpenAI. Verifique sua conexão")
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Erro ao ler resposta da API: %v", err)
	}

	// Handle HTTP error status codes with friendly messages
	switch {
	case resp.StatusCode == 401:
		return "", fmt.Errorf("Chave de API inválida. Verifique seu OPENAI_API_KEY ou config.json")
	case resp.StatusCode == 429:
		return "", fmt.Errorf("Limite de requisições excedido. Aguarde um momento e tente novamente")
	case resp.StatusCode >= 500:
		return "", fmt.Errorf("Erro no serviço da OpenAI (HTTP %d). Tente novamente mais tarde", resp.StatusCode)
	case resp.StatusCode != 200:
		var errResp chatResponse
		if json.Unmarshal(respBytes, &errResp) == nil && errResp.Error != nil {
			return "", fmt.Errorf("Erro da API (HTTP %d): %s", resp.StatusCode, errResp.Error.Message)
		}
		return "", fmt.Errorf("Erro da API (HTTP %d)", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", fmt.Errorf("Erro ao processar resposta da API: %v", err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("Resposta vazia da AI")
	}

	return chatResp.Choices[0].Message.Content, nil
}
