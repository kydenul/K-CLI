package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
	"github.com/samber/lo"
)

// OpenAIChatRequest 是用于发送 OpenAI /v1/chat/completions 请求的结构体
type OpenAIChatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Stream   bool             `json:"stream"`

	IncludeReasoning bool `json:"include_reasoning,omitempty"` // deepseek-r1
	Thinking         bool `json:"thinking,omitempty"`          // deepseekv3.1

	ReasoningEffort float64 `json:"reasoning_effort,omitempty"`
	MaxTokens       uint64  `json:"max_tokens,omitempty"`
}

// OpenAIStreamChoiceDelta 代表 OpenAI 流中的增量变化
type OpenAIStreamChoiceDelta struct {
	Content string `json:"content"`
	Role    string `json:"role"` // 通常只在第一个数据块出现
}

// OpenAIStreamChoice 代表 OpenAI 流中的一个选项
type OpenAIStreamChoice struct {
	Index        int                      `json:"index"`
	Delta        *OpenAIStreamChoiceDelta `json:"delta"`
	FinishReason string                   `json:"finish_reason"` // 在最后一个数据块出现
}

// OpenAIStreamResponse 是用于解码 OpenAI /v1/chat/completions 流式响应中每一个 JSON 对象的结构体
type OpenAIStreamResponse struct {
	ID                string                `json:"id"`
	Object            string                `json:"object"`
	Created           int64                 `json:"created"`
	Model             string                `json:"model"`
	SystemFingerprint string                `json:"system_fingerprint"`
	Choices           []*OpenAIStreamChoice `json:"choices"`
}

type OpenAIFormatProvider struct {
	BaseProvider

	config *Config
}

func NewOpenAIFormatProvider(config *Config, logger log.Logger) *OpenAIFormatProvider {
	return &OpenAIFormatProvider{
		BaseProvider: BaseProvider{
			Logger: logger,

			Client: &http.Client{Timeout: DefaultTimeout},
		},
		config: config,
	}
}

func (p *OpenAIFormatProvider) BuildRequest(
	ctx context.Context,
	respChan chan StreamChunk,
	messages []*Message,
	systemPrompt *string,
) (*http.Request, error) {
	p.Infof("Starting OpenAI stream request")
	// Prepare messages for completion
	preparedMessages := p.PrepareMessagesForCompletion(p.config.Model, messages, systemPrompt)

	// Build request body for Ollama
	body := OpenAIChatRequest{
		Model: p.config.Model,
		Messages: lo.Map(preparedMessages, func(message *Message, _ int) map[string]any {
			return map[string]any{
				"role":    message.Role,
				"content": message.Content,
			}
		}),
		Stream: p.config.Stream,
	}
	body.IncludeReasoning = strings.Contains(p.config.Model, ModelDeepSeekR1)
	if p.config.MaxTokens > 0 {
		body.MaxTokens = p.config.MaxTokens
	}

	if p.config.ReasoningEffort > 0 {
		body.ReasoningEffort = p.config.ReasoningEffort
	}

	if strings.Contains(p.config.Model, ModelDeepSeekV31) {
		body.Thinking = true
	}

	// NOTE Convert to JSON
	jsonBody, err := sonic.Marshal(body)
	if err != nil {
		p.Errorf("Error marshaling request body: %v", err)
		respChan <- StreamChunk{Error: fmt.Errorf("error marshaling request body: %w", err)}
		return nil, fmt.Errorf("error marshaling request body: %w", err)
	}

	// Build URL
	url := p.config.BaseURL
	if p.config.CustomAPIPath != "" {
		url += p.config.CustomAPIPath
	} else {
		url += DefaultCustomAPIPath
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		respChan <- StreamChunk{Error: fmt.Errorf("error creating request: %w", err)}
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("HTTP-Referer", "https://kydenul.github.io")
	req.Header.Set("X-Title", "K-CLI")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	// XXX: 调用辅助函数生成 curl 命令字符串
	curlCmd, _ := p.GenerateCurlCommand(req, jsonBody)
	p.Infof("--- Replayable curl command ---\n%s\n-----------------------------", curlCmd)

	return req, nil
}

func (p *OpenAIFormatProvider) CallStreamableChatCompletions(
	messages []*Message,
	prompt *string,
) *Message {
	return p.BaseProvider.CallStreamableChatCompletions(
		p.config.Provider, p.config.ReasoningEffort, messages, prompt, p.BuildRequest)
}
