package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kydenul/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cast"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"

	ProviderOpenAI = "OpenAI"
	ProviderOllama = "Ollama"

	DefaultChatMessageSize = DefaultMaxTurns
)

type Manager struct {
	log.Logger

	chatSvr  *ChatSvr
	chat     *Chat      // current chat
	messages []*Message // current message in chat

	mcpMgr   *MCPSvrManager
	provider Provider

	promptSvr    *PromptSvr
	systemPrompt string

	chatID        string
	continueExist bool

	config *Config
}

type LLMStreamRet struct {
	Err  error // First
	Done bool  // Second

	ID    string
	Model string

	Content  string
	StreamCh <-chan StreamChunk
}

type MCPToolUse struct {
	ServerName string
	ToolsName  string
	Arguments  map[string]any
}

func NewManager(
	logger log.Logger,
	chatReop ChatRepo,
	mcpReop MCPSvrConfigRepo,
	promptRepo PromptRepo,
	chatID *string,
	config *Config,
) *Manager {
	// NOTE Provider
	var provider Provider
	switch config.Provider {
	case ProviderOpenAI:
		provider = NewOpenAIFormatProvider(config, logger)

	case ProviderOllama:
		provider = NewOllmaFormatProvider(config, logger)

	default: // OpenAI
		provider = NewOpenAIFormatProvider(config, logger)
	}

	// NOTE Manager
	mgr := &Manager{
		Logger: logger,

		chatSvr:  NewChatSvr(chatReop, logger),
		messages: make([]*Message, 0, DefaultChatMessageSize),

		systemPrompt: "",
		promptSvr:    NewPromptSvr(promptRepo, logger),

		mcpMgr:   NewMCPSvrManager(mcpReop, logger),
		provider: provider,
		config:   config,
	}

	// NOTE Generate new chat ID immediately
	if chatID != nil && *chatID != "" {
		mgr.chatID = *chatID
		mgr.continueExist = true

		mgr.Info("continue existing chat", mgr.chatID)
	} else {
		mgr.chatID = GenerateChatID()

		mgr.Info("new chat created, chat id: ", mgr.chatID)
	}

	mgr.mcpMgr.initMCPServer(context.Background())

	return mgr
}

// HandleUserTextInput handle user TEXT input without any link, image
func (mgr *Manager) HandleUserTextInput(userInput string) (*Message, error) {
	// NOTE Clean up
	defer func() {
		if mgr.mcpMgr != nil {
			mgr.mcpMgr.ClossAllSession()
		}
	}()

	mgr.Info("Starting chat session...")

	// Load chat if chat_id was provided and not already loaded
	if mgr.continueExist {
		mgr.loadChat(context.Background())
		mgr.Info("Chat loaded successfully")
	}

	// NOTE 1. Init basic system prompt
	promptBuilder := strings.Builder{}
	promptBuilder.Reset()
	promptBuilder.WriteString(TimePrompt + "\n")

	// NOTE 2. Initialize MCP and system prompt if MCP server settings exist
	if mgr.mcpMgr != nil {
		promptBuilder.WriteString(
			mgr.mcpMgr.Prompt(context.Background(), mgr.promptSvr) + "\n")
	}

	// NOTE 3. Initialize prompt
	if mgr.promptSvr != nil {
		// TODO: 后续使用配置，支持多个 prompt, e.g. "MCP", "Knowledge"
		prompt := mgr.promptSvr.PromptByName(DefaultMCPPromptName)
		if prompt != nil {
			promptBuilder.WriteString(prompt.Content + "\n")
		}
	}
	mgr.systemPrompt = promptBuilder.String()
	mgr.Infof("System prompt: %s", mgr.systemPrompt)

	// NOTE 4. Show history messages
	messageNum := len(mgr.messages)
	if messageNum > 0 {
		mgr.Infof("Loaded %d messages from chat %s", len(mgr.messages), mgr.chatID)

		for _, msg := range mgr.messages {
			switch msg.Role {
			case RoleUser:
				mgr.Infof("\t👤 User: %s\n\n", msg.Content)
			case RoleAssistant:
				mgr.Infof("\t🤖 Assistant: %s\n\n", msg.Content)
			}
		}

		mgr.Infoln("---")
	}

	// NOTE 5. Build user input
	message := NewMessageWithOption(
		RoleUser,
		userInput,
		&MessageOption{},
	)
	mgr.Infoln("🤖 Assistant is thinking...")

	// NOTE 6. Process user input and get assistant response
	var turn uint = 1
	mgr.processUserMessage(&turn, message)
	mgr.Infof("==>> Use  %d turns MCP Server to complete the task.", turn-1)

	// NOTE 7. Show lastest message
	if len(mgr.messages) > messageNum {
		lastMsg := mgr.messages[len(mgr.messages)-1]
		if lastMsg.Role == RoleAssistant {
			fmt.Printf("🤖 Assistant: %s\n\n", lastMsg.Content)
		}

		return mgr.messages[len(mgr.messages)-1], nil
	}

	return nil, errors.New("no new message")
}

func (mgr *Manager) processUserMessage(turn *uint, message *Message) {
	if *turn > mgr.config.MaxTurns {
		mgr.Errorf("MaxTurns %d exceeded", mgr.config.MaxTurns)
		return
	}

	mgr.Infof("Role: %s, Content: %s", message.Role, message.Content)
	mgr.messages = append(mgr.messages, message)

	// NOTE Call Streamable Chat Completions Interface
	assistantMessage := mgr.provider.CallStreamableChatCompletions(mgr.messages, &mgr.systemPrompt)
	if assistantMessage == nil {
		mgr.Errorf("failed to get response from provider")
		return
	}
	content := cast.ToString(assistantMessage.Content) // FIXME: 暂时强制转换到 string

	// NOTE Handle response with tool use
	plainContent, toolContent := mgr.splitContent(content)
	mgr.Infof("plainContent: %s\r\ntoolContent: %s", plainContent, toolContent)

	// NOTE Check if the response contains tool use
	if !mgr.containsToolUse(content) || toolContent == nil {
		mgr.messages = append(mgr.messages, &Message{
			Role:    RoleAssistant,
			Content: content,
		})

		mgr.persistChat()
		return
	}

	mgr.Infof("Assistant: %s\r\n, Tool: %s", plainContent, *toolContent)

	MCPToolUse := mgr.mcpMgr.ExtractMCPToolUse(*toolContent)
	if MCPToolUse == nil {
		return
	}

	// Add server, tool, and arguments info to assistant message
	toolName, svrName, args := MCPToolUse.ToolsName, MCPToolUse.ServerName, MCPToolUse.Arguments
	assistantMessage.Tool = toolName
	assistantMessage.Server = svrName
	assistantMessage.Arguments = args

	// Update last assistant message with plain content
	assistantMessage.Content = plainContent

	mgr.messages = append(mgr.messages, assistantMessage)

	// Execute tool and get results
	toolResults, err := mgr.mcpMgr.CallTool(context.Background(), toolName, args)
	if err != nil {
		mgr.Errorf("failed to call tool: %v", err)
		return
	}

	if len(toolResults.Content) == 0 {
		mgr.Errorf("no content in tool results")
		return
	}

	// TODO: Handle tool results
	switch tc := toolResults.Content[0].(type) {
	case *mcp.TextContent:
		// Create user message with tool results and include tool info
		userMessage := NewMessageWithOption(
			RoleTool,
			tc.Text,
			&MessageOption{
				ID:       assistantMessage.ID,
				Model:    assistantMessage.Model,
				Provider: assistantMessage.Provider,

				Server:    svrName,
				Tool:      toolName,
				Arguments: args,
			})

		// Process user message and assistant response recursively
		(*turn)++
		mgr.processUserMessage(turn, userMessage)

	default:
		mgr.Errorf("unknown content type: %T", tc)

		return
	}
}

// containsToolUse checks if the content contains the XML tags for tool usage.
func (mgr *Manager) containsToolUse(content string) bool {
	for idx := range ToolTags {
		if strings.Contains(content, fmt.Sprintf("<%s>", ToolTags[idx])) &&
			strings.Contains(content, fmt.Sprintf("</%s>", ToolTags[idx])) {
			mgr.Debugf("contains tool use: %s", ToolTags[idx])

			return true
		}
	}

	mgr.Debugf("no tool use found")

	return false
}

// SplitContent splits the content into plain text and tool definition parts.
// Returns a tuple of (plain content, tool content).
func (mgr *Manager) splitContent(content string) (string, *string) {
	// Find the first tool tag
	firstTagIndex := len(content)
	var firstTag *string
	for _, tag := range ToolTags {
		tagStart := strings.Index(content, fmt.Sprintf("<%s>", tag))
		if tagStart != -1 && tagStart < firstTagIndex {
			firstTagIndex = tagStart
			firstTag = &tag
		}
	}

	if firstTagIndex < len(content) && firstTag != nil {
		// Find the end of the tool block
		endTag := fmt.Sprintf("</%s>", *firstTag)
		endIndex := strings.Index(content, endTag)
		if endIndex != -1 {
			endIndex += len(endTag)

			// Extract tool content
			toolContent := strings.TrimSpace(content[firstTagIndex:endIndex])

			// Combine content before and after tool block
			plainContent := strings.TrimSpace(content[:firstTagIndex] + content[endIndex:])

			mgr.Debugf(
				"splitContent, firstTagIndex: %d, SecondTagIndex: %d",
				firstTagIndex, endIndex)

			return plainContent, &toolContent
		}
	}

	mgr.Debugf("splitContent, no tool content found")

	return strings.TrimSpace(content), nil
}

func (mgr *Manager) loadChat(ctx context.Context) {
	chat, err := mgr.chatSvr.Chat(ctx, mgr.chatID)
	if err != nil {
		mgr.Errorf("failed to load chat: %v", err)
		// TODO: 输出到前端，例如控制台

		return
	}

	mgr.messages = chat.Messages
	mgr.chat = chat

	mgr.Infof("Loaded %d messages from chat %s", len(mgr.messages), mgr.chatID)
}

func (mgr *Manager) persistChat() {
	if mgr.chat == nil {
		chat, err := mgr.chatSvr.CreateChat(context.Background(), mgr.messages, mgr.chatID)
		if err != nil {
			mgr.Errorf("failed to create chat: %v", err)
			// TODO: 输出到前端，例如控制台

			return
		}

		mgr.chat = chat
		return
	}

	// Update existing chat
	_, err := mgr.chatSvr.UpdateChat(context.Background(), mgr.chatID, mgr.messages)
	if err != nil {
		mgr.Errorf("failed to update chat: %v", err)
		// TODO: 输出到前端，例如控制台
	}
}
