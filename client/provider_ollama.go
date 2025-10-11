package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
	"github.com/samber/lo"
)

var (
	_ Provider = (*OllamaFormatProvider)(nil)

	ToolTags = []string{"use_mcp_tool", "access_mcp_resource"}
)

// OllamaStreamResponse 是用于解码 Ollama /api/chat 流式响应中每一个 JSON 对象的结构体
type OllamaStreamResponse struct {
	Model     string    `json:"model"`      // 本次请求所使用的模型
	CreatedAt time.Time `json:"created_at"` // 响应创建的 UTC 时间戳 2025-08-28T03:42:30.559748Z
	Message   Message   `json:"message"`    // 包含模型生成内容的对象
	Done      bool      `json:"done"`       // 用于指示生成过程是否已完成

	// --->>> 以下字段: 仅在最后一个响应中出现 <<<---

	DoneReason         string `json:"done_reason"`          // 诊断字段描述了 Ollama 模型生成任务的最终状态: stop(完整回答) / length(回答被截断)
	TotalDuration      int64  `json:"total_duration"`       // 整个请求所花费的总时间(ns)，从收到请求到生成完最后一个 token
	LoadDuration       int64  `json:"load_duration"`        // 加载模型到内存所花费的时间(ns)
	PromptEvalCount    int    `json:"prompt_eval_count"`    // 在处理用户输入（Prompt）时，模型评估（处理）的 token 数量
	PromptEvalDuration int64  `json:"prompt_eval_duration"` // 处理用户输入（Prompt）所花费的时间(ns), 可理解为 `模型理解问题` 花费的时间
	EvalCount          int    `json:"eval_count"`           // 模型生成回答时，总共评估（生成）的 token 数量 => 代表了模型输出的长度
	EvalDuration       int64  `json:"eval_duration"`        // 生成所有回答 token 所花费的总时间(ns), 模型“思考并写出答案”所用的时间
}

// OllamaChatRequest 是 Ollama API 的请求结构体
type OllamaChatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Stream   bool             `json:"stream"`
}

type OllamaFormatProvider struct {
	BaseProvider

	config *Config
}

func NewOllmaFormatProvider(config *Config, logger log.Logger) *OllamaFormatProvider {
	return &OllamaFormatProvider{
		BaseProvider: BaseProvider{
			Logger: logger,
			Client: &http.Client{Timeout: DefaultTimeout},
		},
		config: config,
	}
}

func (p *OllamaFormatProvider) BuildRequest(
	ctx context.Context,
	respChan chan StreamChunk,
	messages []*Message,
	systemPrompt *string,
) (*http.Request, error) {
	p.Infof("Starting Ollama stream request")
	// Prepare messages for completion
	preparedMessages := p.PrepareMessagesForCompletion(p.config.Model, messages, systemPrompt)

	// Build request body for Ollama
	body := OllamaChatRequest{
		Model: p.config.Model,
		Messages: lo.Map(preparedMessages, func(message *Message, _ int) map[string]any {
			return map[string]any{
				"role":    message.Role,
				"content": message.Content,
			}
		}),
		Stream: p.config.Stream,
	}

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
		url += DefaultCustomAPIPath // Ollama 使用 /chat 端点，base-url 已经包含 /api
	}

	p.Infof("Making request to URL: %s. Request body: %s", url, string(jsonBody))

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		respChan <- StreamChunk{Error: fmt.Errorf("error creating request: %w", err)}
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

func (p *OllamaFormatProvider) CallStreamableChatCompletions(
	messages []*Message,
	prompt *string,
) *Message {
	return p.CallStreamableChatCompletionsWithBuilder(
		p.config.Provider, p.config.ReasoningEffort, messages, prompt, p.BuildRequest)
}
