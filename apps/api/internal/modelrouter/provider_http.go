package modelrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultModelMaxTokens = 4096

func callOpenAICompatible(ctx context.Context, client *http.Client, baseURL, apiKey, providerName string, req CallRequest, models []ModelInfo) (*CallResult, error) {
	model := selectedModel(req, models)
	body := map[string]any{
		"model":    model,
		"messages": req.Messages,
		"stream":   false,
	}
	if req.StructuredReq {
		body["response_format"] = map[string]any{"type": "json_object"}
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := postJSON(ctx, client, strings.TrimRight(baseURL, "/")+"/chat/completions", apiKey, "", body, &out); err != nil {
		return nil, fmt.Errorf("%s chat completion: %w", providerName, err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s chat completion: response contained no choices", providerName)
	}
	prompt, completion, total := normalizeUsage(out.Usage.PromptTokens, out.Usage.CompletionTokens, out.Usage.TotalTokens)
	return &CallResult{
		Content:          out.Choices[0].Message.Content,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Cost:             estimateCost(models, model, prompt, completion),
		Model:            model,
		Provider:         providerName,
		FinishReason:     out.Choices[0].FinishReason,
	}, nil
}

func callAnthropic(ctx context.Context, client *http.Client, baseURL, apiKey string, req CallRequest, models []ModelInfo) (*CallResult, error) {
	model := selectedModel(req, models)
	var system []string
	var messages []Message
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system", "developer":
			if strings.TrimSpace(msg.Content) != "" {
				system = append(system, msg.Content)
			}
		case "assistant":
			messages = append(messages, Message{Role: "assistant", Content: msg.Content})
		default:
			messages = append(messages, Message{Role: "user", Content: msg.Content})
		}
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": defaultModelMaxTokens,
		"messages":   messages,
	}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	headers := map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
	}
	if err := postJSONWithHeaders(ctx, client, strings.TrimRight(baseURL, "/")+"/messages", headers, body, &out); err != nil {
		return nil, fmt.Errorf("anthropic message: %w", err)
	}
	var parts []string
	for _, part := range out.Content {
		if part.Type == "" || part.Type == "text" {
			parts = append(parts, part.Text)
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("anthropic message: response contained no text content")
	}
	prompt, completion, total := normalizeUsage(out.Usage.InputTokens, out.Usage.OutputTokens, 0)
	return &CallResult{
		Content:          strings.Join(parts, ""),
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Cost:             estimateCost(models, model, prompt, completion),
		Model:            model,
		Provider:         "anthropic",
		FinishReason:     out.StopReason,
	}, nil
}

func callGemini(ctx context.Context, client *http.Client, baseURL, apiKey string, req CallRequest, models []ModelInfo) (*CallResult, error) {
	model := selectedModel(req, models)
	var systemParts []map[string]string
	var contents []map[string]any
	for _, msg := range req.Messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		if msg.Role == "system" || msg.Role == "developer" {
			systemParts = append(systemParts, map[string]string{"text": msg.Content})
			continue
		}
		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]string{{"text": msg.Content}},
		})
	}
	body := map[string]any{
		"contents": contents,
	}
	if len(systemParts) > 0 {
		body["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	if req.StructuredReq {
		body["generationConfig"] = map[string]any{"responseMimeType": "application/json"}
	}

	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	modelPath := model
	if !strings.HasPrefix(modelPath, "models/") {
		modelPath = "models/" + modelPath
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + modelPath + ":generateContent?key=" + url.QueryEscape(apiKey)
	if err := postJSON(ctx, client, endpoint, "", "", body, &out); err != nil {
		return nil, fmt.Errorf("gemini generate content: %w", err)
	}
	if len(out.Candidates) == 0 {
		return nil, fmt.Errorf("gemini generate content: response contained no candidates")
	}
	var parts []string
	for _, part := range out.Candidates[0].Content.Parts {
		parts = append(parts, part.Text)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("gemini generate content: response contained no text parts")
	}
	prompt, completion, total := normalizeUsage(out.UsageMetadata.PromptTokenCount, out.UsageMetadata.CandidatesTokenCount, out.UsageMetadata.TotalTokenCount)
	return &CallResult{
		Content:          strings.Join(parts, ""),
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Cost:             estimateCost(models, model, prompt, completion),
		Model:            model,
		Provider:         "gemini",
		FinishReason:     out.Candidates[0].FinishReason,
	}, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint, bearerToken, apiKeyHeader string, body any, out any) error {
	headers := map[string]string{}
	if bearerToken != "" {
		headers["Authorization"] = "Bearer " + bearerToken
	}
	if apiKeyHeader != "" {
		headers["x-api-key"] = apiKeyHeader
	}
	return postJSONWithHeaders(ctx, client, endpoint, headers, body, out)
}

func postJSONWithHeaders(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body any, out any) error {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if value != "" {
			httpReq.Header.Set(key, value)
		}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func selectedModel(req CallRequest, models []ModelInfo) string {
	if strings.TrimSpace(req.PreferredModel) != "" {
		return strings.TrimSpace(req.PreferredModel)
	}
	if len(models) > 0 {
		return models[0].Name
	}
	return ""
}

func normalizeUsage(prompt, completion, total int) (int, int, int) {
	if total == 0 {
		total = prompt + completion
	}
	return prompt, completion, total
}

func estimateCost(models []ModelInfo, model string, promptTokens, completionTokens int) float64 {
	for _, info := range models {
		if info.Name == model {
			return (float64(promptTokens)/1000.0)*info.CostPer1KInput +
				(float64(completionTokens)/1000.0)*info.CostPer1KOutput
		}
	}
	return 0
}
