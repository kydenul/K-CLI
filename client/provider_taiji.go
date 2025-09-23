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

// TaijiChatRequest 是用于发送 OpenAI /v1/chat/completions 请求的结构体
type TaijiChatRequest struct {
	QueryID     string     `json:"query_id"`
	Model       string     `json:"model"`
	Messages    []*Message `json:"messages"`
	Temperature float64    `json:"temperature"` // 调节概率值，取值区间为 (0.0, 2.0]，默认为 1.0
	TopP        float64    `json:"top_p"`       // 采样累积概率的阈值, 取值区间为 [0.0, 1.0]，默认值 1.0
	MaxTokens   uint64     `json:"max_tokens"`
	Stream      bool       `json:"stream"`

	Thinking bool `json:"thinking,omitempty"` // DeepSeek-V3_1
}

type TaijiProvider struct {
	BaseProvider

	config *Config
}

func NewTaijiProvider(config *Config, logger log.Logger) *TaijiProvider {
	return &TaijiProvider{
		BaseProvider: BaseProvider{
			Logger: logger,

			Client: &http.Client{Timeout: DefaultTimeout},
		},
		config: config,
	}
}

func (p *TaijiProvider) BuildRequest(
	ctx context.Context,
	respChan chan StreamChunk,
	messages []*Message,
	systemPrompt *string,
) (*http.Request, error) {
	p.Infof("Starting OpenAI stream request")
	// Prepare messages for completion
	preparedMessages := p.PrepareMessagesForCompletion(p.config.Model, messages, systemPrompt)

	// Build request body for Ollama
	body := TaijiChatRequest{
		QueryID: GenerateChatID(),
		Model:   p.config.Model,
		Messages: lo.Map(preparedMessages, func(message *Message, _ int) *Message {
			return &Message{
				Role:    message.Role,
				Content: message.Content,
			}
		}),

		Temperature: p.config.ReasoningEffort,
		TopP:        1.0,
		MaxTokens:   p.config.MaxTokens,

		Stream: false,
	}

	// NOTE DeepSeek-V3_1 => thinking
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

func (p *TaijiProvider) CallStreamableChatCompletions(
	messages []*Message,
	prompt *string,
) *Message {
	return p.BaseProvider.CallStreamableChatCompletions(
		p.config.Provider, p.config.ReasoningEffort, messages, prompt, p.BuildRequest)
}
