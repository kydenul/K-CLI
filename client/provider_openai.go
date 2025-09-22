package client

import (
	"bufio"
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

	client *http.Client
	config *Config
}

func NewOpenAIFormatProvider(config *Config, logger log.Logger) *OpenAIFormatProvider {
	return &OpenAIFormatProvider{
		BaseProvider: BaseProvider{Logger: logger},
		config:       config,
		client:       &http.Client{Timeout: DefaultTimeout},
	}
}

//nolint:cyclop
func (p *OpenAIFormatProvider) PrepareMessagesForCompletion(
	model string, messages []*Message, systemPrompt *string,
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

			// Add cache_control only to claude-3 series models
			if strings.Contains(model, ModelClaude3) {
				for i := range contentParts {
					if contentParts[i].Type == "text" {
						contentParts[i].CacheControl = map[string]any{
							"type": "ephemeral",
						}
					}
				}
				systemMessage.Content = contentParts
			} else {
				for _, part := range contentParts {
					if part.Type == "text" {
						apiParts = append(apiParts, part.Text)
					}
				}
				systemMessage.Content = apiParts
			}
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

//nolint:cyclop,funlen
func (p *OpenAIFormatProvider) doCallStreamableChatCompletions(
	messages []*Message, systemPrompt *string,
) <-chan StreamChunk {
	respChan := make(chan StreamChunk, DefaultStreamChunkSize)
	// ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	// defer cancel()
	ctx := context.Background()

	// NOTE: 异步调用
	go func() {
		defer close(respChan)

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
			Stream: true,
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
			return
		}

		// Build URL
		url := p.config.BaseURL
		if p.config.CustomAPIPath != "" {
			url += p.config.CustomAPIPath
		} else {
			url += DefaultCustomAPIPath
		}

		p.Infof("Making request to URL: %s. Request body: %s", url, string(jsonBody))

		// Create request
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
		if err != nil {
			respChan <- StreamChunk{Error: fmt.Errorf("error creating request: %w", err)}
			return
		}

		// Set headers
		req.Header.Set("HTTP-Referer", "https://kydenul.github.io")
		req.Header.Set("X-Title", "K-CLI")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)

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
			select {
			case <-ctx.Done():
				p.Info("Context cancelled")
				respChan <- StreamChunk{Error: ctx.Err()}
				return

			default:
			}

			line := scanner.Text()
			lineCount++
			p.Debugf("Received line %d: %s", lineCount, line)

			// NOTE: Ignore non-data lines
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" { // NOTE: stream done
				p.Infof("Stream marked as done")
				break
			}

			response := &OpenAIStreamResponse{}
			if err := sonic.UnmarshalString(data, response); err != nil {
				p.Errorf("Error unmarshaling response line: %v", err)
				continue
			}

			if len(response.Choices) > 0 {
				choice := response.Choices[0]

				// NOTE: stream chunk DONE
				if choice.FinishReason != "" {
					p.Info("Stream marked as done")

					if choice.Delta != nil && choice.Delta.Content != "" {
						content := choice.Delta.Content
						p.Debugf("Sending final chunk: %s", content)
						respChan <- StreamChunk{
							ID:    response.ID,
							Model: response.Model,

							Content: content,
							Done:    true,
						}
					} else {
						p.Debugln("Sending done signal")
						respChan <- StreamChunk{
							ID:    response.ID,
							Model: response.Model,

							Done: true,
						}
					}

					break
				}

				// NOTE: Send stream chunk to respsonse channel -> choice.Delta.Content
				respChan <- StreamChunk{
					ID:    response.ID,
					Model: response.Model,

					Content: choice.Delta.Content,
					Done:    false,
				}
			}
		}

		if err := scanner.Err(); err != nil {
			respChan <- StreamChunk{Error: fmt.Errorf("error reading response stream: %w", err)}
		}
	}()

	p.Info("OpenAI CallChatCompletionsStream launched goroutine")
	return respChan
}

func (p *OpenAIFormatProvider) CallStreamableChatCompletions(
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

	var id, model string
	if ret.StreamCh != nil {
		for chunk := range ret.StreamCh {
			if chunk.Error != nil {
				p.Errorln("Stream error:", chunk.Error)
				break
			}

			if chunk.Content != "" {
				fullContent.WriteString(chunk.Content)
				p.Debugf("Assistant chunk: %s", chunk.Content)
			}

			if chunk.Done {
				p.Info("Stream completed")
				id, model = chunk.ID, chunk.Model
				break
			}
		}
	}

	contentFull := fullContent.String()
	p.Infof("Assistant: %s", contentFull)

	assistantMessage := NewMessageWithOption(
		RoleAssistant,
		contentFull,
		&MessageOption{
			ID:    id,
			Model: model,

			ReasoningContent: contentFull,
			Provider:         p.config.Provider,
			ReasoningEffort:  p.config.ReasoningEffort,
			Links:            nil,
		})
	p.Infof("Assistant: %s", assistantMessage.Content)

	return assistantMessage
}
