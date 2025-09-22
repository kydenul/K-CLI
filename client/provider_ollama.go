package client

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
	"github.com/samber/lo"
	"github.com/spf13/cast"
)

const (
	ChatCompletionEndpoint = "/chat/completions"
	DefaultTimeout         = 60 * time.Second
	DefaultStreamChunkSize = 16 // default stream chunk size
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

	client *http.Client
	config *Config
}

func NewOllmaFormatProvider(config *Config, logger log.Logger) *OllamaFormatProvider {
	return &OllamaFormatProvider{
		BaseProvider: BaseProvider{Logger: logger},
		config:       config,
		client:       &http.Client{Timeout: DefaultTimeout},
	}
}

func (p *OllamaFormatProvider) PrepareMessagesForCompletion(
	messages []*Message, systemPrompt *string,
) []*Message {
	preparedMessages := make([]*Message, 0, 16)

	// Add system message if provided
	if systemPrompt != nil && *systemPrompt != "" {
		systemMessage := NewMessageWithOption("system", *systemPrompt, nil)

		// Convert string content to content parts format
		if content, ok := systemMessage.Content.(string); ok {
			systemMessage.Content = content
		} else if contentParts, ok := systemMessage.Content.([]*ContentPart); ok {
			var apiParts []string
			for _, part := range contentParts {
				if part.Type == "text" {
					apiParts = append(apiParts, part.Text)
				}
			}
			systemMessage.Content = apiParts
		}

		// Remove timestamp fields, otherwise likely unsupported_country_region_territory
		systemMessage.Timestamp = nil
		systemMessage.UnixTimestamp = 0

		preparedMessages = append(preparedMessages, systemMessage)
	}

	// Add original messages
	for _, msg := range messages {
		// Handle content conversion for structured content
		if parts, ok := msg.Content.([]*ContentPart); ok {
			var apiParts []map[string]any
			for _, part := range parts {
				apiParts = append(apiParts, map[string]any{
					"type": part.Type,
					"text": part.Text,
				})
			}
			msg.Content = apiParts
		}

		// Remove timestamp fields, otherwise likely unsupported_country_region_territory
		msg.Timestamp = nil
		msg.UnixTimestamp = 0

		preparedMessages = append(preparedMessages, msg)
	}

	p.Infof("Prepared messages for completion: %v", preparedMessages)

	return preparedMessages
}

func (p *OllamaFormatProvider) doCallStreamableChatCompletions(
	messages []*Message, systemPrompt *string,
) <-chan StreamChunk {
	respChan := make(chan StreamChunk, DefaultStreamChunkSize)
	// ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	// defer cancel()
	ctx := context.Background()

	// NOTE: 异步调用
	go func() {
		defer close(respChan)

		p.Infof("Starting Ollama stream request")
		// Prepare messages for completion
		preparedMessages := p.PrepareMessagesForCompletion(messages, systemPrompt)

		// Build request body for Ollama
		body := OllamaChatRequest{
			Model: p.config.Model,
			Messages: lo.Map(preparedMessages, func(message *Message, _ int) map[string]any {
				return map[string]any{
					"role":    message.Role,
					"content": message.Content,
				}
			}),
			Stream: true,
		}

		jsonBody, err := sonic.Marshal(body)
		if err != nil {
			p.Errorf("Error marshaling request body: %v", err)
			respChan <- StreamChunk{Error: fmt.Errorf("error marshaling request body: %w", err)}
			return
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
			return
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")

		// Make request
		resp, err := p.client.Do(req)
		if err != nil {
			p.Errorf("HTTP request error: %v", err)
			respChan <- StreamChunk{Error: fmt.Errorf("HTTP error getting chat response: %w", err)}
			return
		}
		defer resp.Body.Close()

		p.Infof("Response status: %d", resp.StatusCode)
		if resp.StatusCode != http.StatusOK {
			p.Errorf("HTTP error: status code %d", resp.StatusCode)
			respChan <- StreamChunk{Error: fmt.Errorf("HTTP error: status code %d", resp.StatusCode)}
			return
		}

		p.Info("Starting to process streaming response")

		// Process streaming response
		scanner := bufio.NewScanner(resp.Body)
		lineCount := 0
		for scanner.Scan() {
			// NOTE:
			select {
			case <-ctx.Done():
				p.Info("Context cancelled")
				respChan <- StreamChunk{Error: ctx.Err()}
				return

			default:
			}

			// NOTE:
			line := scanner.Text()
			lineCount++
			p.Debugf("Received line %d: %s", lineCount, line)

			if line == "" {
				continue
			}

			response := &OllamaStreamResponse{}
			if err := sonic.UnmarshalString(line, response); err != nil {
				p.Errorf("Error unmarshaling response line: %v", err)
				continue
			}

			// NOTE: stream chunk DONE
			if response.Done {
				p.Info("Stream marked as done")
				if response.Message.Content != nil &&
					cast.ToString(response.Message.Content) != "" {
					content := cast.ToString(response.Message.Content)
					p.Infof("Sending final chunk: %s", content)
					respChan <- StreamChunk{Content: content, Done: true}
				} else {
					p.Info("Sending done signal")
					respChan <- StreamChunk{Done: true}
				}
				break
			}

			// NOTE: Send stream chunk to respsonse channel
			if response.Message.Content != nil {
				content := cast.ToString(response.Message.Content)
				if content != "" {
					p.Debugf("Goroutine sends chunk: %s", content)
					respChan <- StreamChunk{Content: content, Done: false}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			respChan <- StreamChunk{Error: fmt.Errorf("error reading response stream: %w", err)}
		}
	}()

	p.Info("Ollama CallChatCompletionsStream launched goroutine")
	return respChan
}

func (p *OllamaFormatProvider) CallStreamableChatCompletions(
	messages []*Message,
	prompt *string,
) *Message {
	ret := p.HandleStreamableChat(p.doCallStreamableChatCompletions(messages, prompt))
	if ret.Err != nil {
		p.Error(ret.Err)
		return nil
	}

	if ret.Done {
		p.Info("Chat completed")
		return nil
	}

	var fullContent strings.Builder
	fullContent.WriteString(ret.Content)

	// handle stream
	if ret.StreamCh != nil {
		for chunk := range ret.StreamCh {
			if chunk.Error != nil {
				p.Errorln("Stream error:", chunk.Error)
				break
			}

			if chunk.Done {
				p.Info("Stream completed")
				break
			}

			if chunk.Content != "" {
				fullContent.WriteString(chunk.Content)
				p.Debugf("Assistant chunk: %s", chunk.Content)
			}
		}
	}

	contentFull := fullContent.String()
	p.Infof("Assistant: %s", contentFull)

	assistantMessage := NewMessageWithOption(
		RoleAssistant,
		contentFull,
		&MessageOption{
			ReasoningContent: contentFull,
			Provider:         p.config.Provider,
			Model:            p.config.Model,
			ReasoningEffort:  DefaultReasoningEffort,
			Links:            nil,
		})
	p.Infof("Assistant: %s", assistantMessage.Content)

	return assistantMessage
}
