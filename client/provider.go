package client

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
)

const (
	ModelClaude3     = "claude-3"
	ModelDeepSeekR1  = "deepseek-r1"
	ModelDeepSeekV31 = "DeepSeek-V3_1"

	DefaultTimeout         = 60 * time.Second
	DefaultStreamChunkSize = 16 // default stream chunk size
)

type BaseProvider struct {
	log.Logger

	Client *http.Client
}

func (p *BaseProvider) DoCallStreamableChatCompletions(
	messages []*Message, systemPrompt *string,
	BuildRequest func(
		context.Context,
		chan StreamChunk,
		[]*Message,
		*string,
	) (*http.Request, error),
) <-chan StreamChunk {
	respChan := make(chan StreamChunk, DefaultStreamChunkSize)
	// ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	// defer cancel()
	ctx := context.Background()

	// NOTE: 异步调用
	go func() {
		defer close(respChan)

		req, err := BuildRequest(ctx, respChan, messages, systemPrompt)
		if err != nil {
			p.Errorf("Error building request: %v", err)
			respChan <- StreamChunk{Error: fmt.Errorf("error building request: %w", err)}
			return
		}

		// Make request
		resp, err := p.Client.Do(req)
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
		p.ProcessStreamableResponse(ctx, resp, respChan)
	}()

	p.Info("Ollama CallChatCompletionsStream launched goroutine")
	return respChan
}

//nolint:cyclop
func (p *BaseProvider) PrepareMessagesForCompletion(
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

func (p *BaseProvider) ProcessStreamableResponse(
	ctx context.Context,
	resp *http.Response,
	respChan chan StreamChunk,
) {
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

				// 非优先使用 reasoning_content，如果为空则使用 content
				var finalContent string
				if choice.Delta != nil {
					if choice.Delta.Content != "" {
						finalContent = choice.Delta.Content
					} else {
						finalContent = choice.Delta.ReasoningContent
					}
				}

				if finalContent != "" {
					p.Debugf("Sending final chunk: %s", finalContent)
					respChan <- StreamChunk{
						ID:    response.ID,
						Model: response.Model,

						Content: finalContent,
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

			// NOTE: Send stream chunk to response channel
			// 优先使用 reasoning_content，如果为空则使用 content
			var chunkContent string
			if choice.Delta.Content != "" {
				chunkContent = choice.Delta.Content
			} else {
				chunkContent = choice.Delta.ReasoningContent
			}

			respChan <- StreamChunk{
				ID:    response.ID,
				Model: response.Model,

				Content: chunkContent,
				Done:    false,
			}
		}
	}

	p.Infof("Finished scanning response body, total lines: %d", lineCount)

	if err := scanner.Err(); err != nil {
		p.Errorf("Scanner error: %v", err)
		respChan <- StreamChunk{Error: fmt.Errorf("error reading response stream: %w", err)}
	} else if lineCount == 0 {
		p.Warn("No lines received from response body - this might indicate an empty response")
	}
}

func (p *BaseProvider) HandleStreamableChat(streamCh <-chan StreamChunk) LLMStreamRet {
	// NOTE Check channel is closed or not
	chunk, ok := <-streamCh
	if !ok {
		p.Info("Stream channel closed immediately")
		return LLMStreamRet{}
	}

	// NOTE: 1. Error is occurred => return
	if chunk.Error != nil {
		p.Errorln("Stream error:", chunk.Error)
		return LLMStreamRet{Err: chunk.Error}
	}

	// NOTE: 2. Process Done => return
	if chunk.Done {
		p.Info("Stream completed immediately")
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,
			Done:  true,
		}
	}

	// NOTE: 3. Processing
	if chunk.Content != "" {
		p.Infof("Received first chunk: %s", chunk.Content)
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,

			Content:  chunk.Content,
			StreamCh: streamCh,
		}
	}

	// NOTE: 4. Empty first chunk
	p.Info("Empty first chunk, waiting for next")
	return p.waitForNextChunk(streamCh)
}

func (p *BaseProvider) waitForNextChunk(streamCh <-chan StreamChunk) LLMStreamRet {
	// NOTE Check channel is closed or not
	chunk, ok := <-streamCh
	if !ok {
		p.Info("Stream channel closed => complete!!!")
		return LLMStreamRet{}
	}

	if chunk.Error != nil {
		p.Errorln("Stream error:", chunk.Error)
		return LLMStreamRet{Err: chunk.Error}
	}

	if chunk.Done {
		p.Info("Stream completed")
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,

			Done: true,
		}
	}

	if chunk.Content != "" {
		return LLMStreamRet{
			ID:    chunk.ID,
			Model: chunk.Model,

			Content:  chunk.Content,
			StreamCh: streamCh,
		}
	}

	p.Debug("Empty chunk, waiting for next")
	return p.waitForNextChunk(streamCh)
}

func (p *BaseProvider) CallStreamableChatCompletions(
	provider string,
	reasoningEffort string,
	messages []*Message,
	prompt *string,
	BuildRequest func(
		context.Context,
		chan StreamChunk,
		[]*Message,
		*string,
	) (*http.Request, error),
) *Message {
	ret := p.HandleStreamableChat(p.DoCallStreamableChatCompletions(messages, prompt, BuildRequest))
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
			Provider:         provider,
			ReasoningEffort:  reasoningEffort,
			Links:            nil,
		})
	// p.Infof("Assistant: %s", assistantMessage.Content)

	return assistantMessage
}

// GenerateCurlCommand returns a string that can be executed to make the request
// ⚠️ 注意：因为该函数没有读取 req.Body => 请求体仍然可以被 client.Do 正常读取。
func (*BaseProvider) GenerateCurlCommand(
	req *http.Request,
	bodyBytes []byte,
) (string, error) {
	var command strings.Builder

	// 1. Add 'curl -X METHOD' part
	// 对 URL 进行转义处理
	command.WriteString(fmt.Sprintf("curl -X %s %s", req.Method, shellEscape(req.URL.String())))

	// 2. Add req headers
	for key, values := range req.Header {
		for _, value := range values {
			// 使用 ' \\\n  -H' 来换行和缩进，使命令更易读
			// 对 header 的 key 和 value 进行转义
			headerStr := fmt.Sprintf("%s: %s", key, value)
			command.WriteString(" \\\n  -H " + shellEscape(headerStr))
		}
	}

	// 3. Add req body if exists
	if len(bodyBytes) > 0 {
		// Use ' \\\n  --data-raw' to make command more readable
		// 对请求体进行转义处理
		command.WriteString(" \\\n  --data-raw " + shellEscape(string(bodyBytes)))
	}

	return command.String(), nil
}

// shellEscape 对字符串进行 shell 转义处理
func shellEscape(s string) string {
	// 如果字符串不包含需要转义的字符，直接用单引号包围
	if !strings.ContainsAny(s, "'\"\\$`\n\r\t") {
		return "'" + s + "'"
	}

	// 对于包含单引号的字符串，使用双引号并转义内部的特殊字符
	s = strings.ReplaceAll(s, "\\", "\\\\") // 转义反斜杠
	s = strings.ReplaceAll(s, "\"", "\\\"") // 转义双引号
	s = strings.ReplaceAll(s, "$", "\\$")   // 转义美元符号
	s = strings.ReplaceAll(s, "`", "\\`")   // 转义反引号
	s = strings.ReplaceAll(s, "\n", "\\n")  // 转义换行符
	s = strings.ReplaceAll(s, "\r", "\\r")  // 转义回车符
	s = strings.ReplaceAll(s, "\t", "\\t")  // 转义制表符

	return "\"" + s + "\""
}
