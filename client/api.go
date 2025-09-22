package client

import (
	"context"
)

// ChatRepo (Chat Repository) defines the interface for chat repository operations
type ChatRepo interface {
	ListChatsAsync(
		ctx context.Context,
		keyword, model, provider *string,
		limit int,
	) <-chan OpResp
	GetChatAsync(ctx context.Context, chatID string) <-chan OpResp
	AddChatAsync(ctx context.Context, chat *Chat) <-chan OpResp
	UpdateChatAsync(ctx context.Context, chat *Chat) <-chan OpResp
	DeleteChatAsync(ctx context.Context, chatID string) <-chan OpResp

	// Sync versions for convenience
	ListChats(ctx context.Context, keyword, model, provider *string, limit int) ([]*Chat, error)
	Chat(ctx context.Context, chatID string) (*Chat, error)
	AddChat(ctx context.Context, chat *Chat) (*Chat, error)
	UpdateChat(ctx context.Context, chat *Chat) (*Chat, error)
	DeleteChat(ctx context.Context, chatID string) (bool, error)

	Close() error
}

// MCPServerConfigByName (MCP Server Config Repository) defines the interface
// for mcp server config repository operations
type MCPSvrConfigRepo interface {
	MCPServerConfigByName(name string) (*MCPSvrItem, error)
	AllMCPServerConfigs() []*MCPSvrItem
	UpdateMCPServerConfigByName(item *MCPSvrItem) error
	DeleteMCPServerConfigByName(name string) error
}

// PromptRepo (Prompt Repository) defines the interface for prompt repository operations
type PromptRepo interface {
	PromptByName(name string) (*PromptItem, error)
	AllPrompts() []*PromptItem
	UpdatePromptByName(item *PromptItem) error
	DeletePromptByName(name string) error
}

// StreamChunk defines a chunk of a stream
type StreamChunk struct {
	ID string // 每一条工具调用请求都有一个唯一的 ID => 在返回结果时，必须将这个 ID 附上，以便模型能够准确地将返回的结果与它当初的请求对应起来
	// Provider string
	Model string

	Content string // The content of the chunk
	Done    bool   // Whether the stream is done
	Error   error  // Any error that occurred
}

type Provider interface {
	CallStreamableChatCompletions(
		messages []*Message,
		prompt *string,
	) *Message
}
