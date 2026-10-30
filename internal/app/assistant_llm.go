package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ─── Assistant model providers ──────────────────────────────────────────────
//
// Each provider runs its own tool-calling loop, because Gemini's native API and
// the OpenAI-compatible APIs (Groq, OpenAI) shape tool calls differently. The
// loop hands every tool call to exec, which is where the app does the real work.

const assistantMaxToolRounds = 6

// toolDef describes a tool to the model; Params is a JSON Schema object.
type toolDef struct {
	Name        string
	Description string
	Params      map[string]any
}

type toolCall struct {
	Name string
	Args map[string]any
}

type toolExecutor func(toolCall) any

type llmProvider interface {
	Name() string
	Run(system string, history []assistantMessage, tools []toolDef, exec toolExecutor) (string, error)
}

var errTooManyToolRounds = errors.New("model kept calling tools without answering")

func postJSON(endpoint string, headers map[string]string, payload any, timeout time.Duration) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(data)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, snippet)
	}
	return data, nil
}

// ─── Gemini (native generateContent API) ────────────────────────────────────

// geminiBaseURL is a var so tests can point it at a fake server.
var geminiBaseURL = "https://generativelanguage.googleapis.com"

type geminiProvider struct {
	apiKey string
	model  string
	label  string
}

func (p geminiProvider) Name() string { return p.label }

func (p geminiProvider) Run(system string, history []assistantMessage, tools []toolDef, exec toolExecutor) (string, error) {
	// contents holds raw JSON so the model's own turns are echoed back exactly as
	// received; Gemini 3 needs their thought signatures to continue a tool loop.
	var contents []json.RawMessage
	for _, m := range history {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		turn, _ := json.Marshal(map[string]any{"role": role, "parts": []map[string]string{{"text": m.Content}}})
		contents = append(contents, turn)
	}

	decls := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Params})
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", geminiBaseURL, p.model)
	headers := map[string]string{"x-goog-api-key": p.apiKey}

	for round := 0; round < assistantMaxToolRounds; round++ {
		payload := map[string]any{
			"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
			"contents":          contents,
			"tools":             []map[string]any{{"functionDeclarations": decls}},
			"generationConfig":  map[string]any{"maxOutputTokens": 2048},
		}
		data, err := postJSON(endpoint, headers, payload, 30*time.Second)
		if err != nil {
			return "", err
		}

		var resp struct {
			Candidates []struct {
				Content json.RawMessage `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", err
		}
		if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content) == 0 {
			return "", errors.New("empty gemini response")
		}
		modelTurn := resp.Candidates[0].Content

		var parsed struct {
			Parts []struct {
				Text         string `json:"text"`
				Thought      bool   `json:"thought"`
				FunctionCall *struct {
					ID   string         `json:"id"`
					Name string         `json:"name"`
					Args map[string]any `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		}
		if err := json.Unmarshal(modelTurn, &parsed); err != nil {
			return "", err
		}

		var text strings.Builder
		var responses []map[string]any
		for _, part := range parsed.Parts {
			if part.FunctionCall != nil {
				result := exec(toolCall{Name: part.FunctionCall.Name, Args: part.FunctionCall.Args})
				fr := map[string]any{"name": part.FunctionCall.Name, "response": map[string]any{"result": result}}
				if part.FunctionCall.ID != "" {
					fr["id"] = part.FunctionCall.ID
				}
				responses = append(responses, map[string]any{"functionResponse": fr})
			} else if !part.Thought {
				text.WriteString(part.Text)
			}
		}
		if len(responses) == 0 {
			if strings.TrimSpace(text.String()) == "" {
				return "", errors.New("gemini returned no text")
			}
			return strings.TrimSpace(text.String()), nil
		}
		turn, _ := json.Marshal(map[string]any{"role": "user", "parts": responses})
		contents = append(contents, modelTurn, turn)
	}
	return "", errTooManyToolRounds
}

// ─── OpenAI-compatible chat completions (Groq, OpenAI) ──────────────────────

type openAICompatProvider struct {
	endpoint string
	apiKey   string
	model    string
	label    string
}

func (p openAICompatProvider) Name() string { return p.label }

func (p openAICompatProvider) Run(system string, history []assistantMessage, tools []toolDef, exec toolExecutor) (string, error) {
	messages := []any{map[string]string{"role": "system", "content": system}}
	for _, m := range history {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}
	toolSpecs := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		toolSpecs = append(toolSpecs, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Params},
		})
	}
	headers := map[string]string{"Authorization": "Bearer " + p.apiKey}

	for round := 0; round < assistantMaxToolRounds; round++ {
		payload := map[string]any{
			"model":       p.model,
			"messages":    messages,
			"tools":       toolSpecs,
			"tool_choice": "auto",
			"max_tokens":  1200,
		}
		data, err := postJSON(p.endpoint, headers, payload, 20*time.Second)
		if err != nil {
			return "", err
		}
		var resp struct {
			Choices []struct {
				Message json.RawMessage `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("empty choices")
		}
		var msg struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal(resp.Choices[0].Message, &msg); err != nil {
			return "", err
		}
		if len(msg.ToolCalls) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				return "", errors.New("model returned no text")
			}
			return strings.TrimSpace(msg.Content), nil
		}

		messages = append(messages, resp.Choices[0].Message)
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{}
			}
			result, _ := json.Marshal(exec(toolCall{Name: tc.Function.Name, Args: args}))
			messages = append(messages, map[string]string{"role": "tool", "tool_call_id": tc.ID, "content": string(result)})
		}
	}
	return "", errTooManyToolRounds
}
