package client

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kydenul/log"
)

type Chat struct {
	ID         string    `json:"id"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
	Messages   []*Message
}

// UpdateMessages filters out system messages and sorts the remaining ones by timestamp
func (c *Chat) UpdateMessages(messages []*Message) {
	// Filter out system messages and sort the remaining ones by timestamp
	c.Messages = make([]*Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "system" {
			c.Messages = append(c.Messages, msg)
		}
	}

	c.UpdateTime = GetISO8601Timestamp()
}

// ----------------------------------------------------------------------------

type ChatSvr struct {
	log.Logger

	repo ChatRepo
}

func NewChatSvr(repo ChatRepo, logger log.Logger) *ChatSvr {
	return &ChatSvr{
		Logger: logger,
		repo:   repo,
	}
}

// createTimeStamp returns current timestamp in ISO8601 format with timezone offset
func (svr *ChatSvr) createTimeStamp() time.Time {
	tm := GetISO8601Timestamp()
	svr.Infof("createTimeStamp: %s", tm)
	return tm
}

// ListChats returns a List of chats filtered by the given criteria, sorted by creation time descending
func (svr *ChatSvr) ListChats(
	ctx context.Context,
	keyword, model, provider *string,
	limit int,
) ([]*Chat, error) {
	return svr.repo.ListChats(ctx, keyword, model, provider, limit)
}

// Chat returns a specific chat by ID
func (svr *ChatSvr) Chat(ctx context.Context, chatID string) (*Chat, error) {
	return svr.repo.Chat(ctx, chatID)
}

// CreateChat creates a new chat with messages and optional external ID
func (svr *ChatSvr) CreateChat(
	ctx context.Context,
	messages []*Message,
	chatID string,
) (*Chat, error) {
	return svr.repo.AddChat(ctx, &Chat{
		ID:         chatID,
		CreateTime: svr.createTimeStamp(),
		UpdateTime: svr.createTimeStamp(),
		Messages:   messages,
	})
}

// UpdateChat updates an existing chat's messages
func (svr *ChatSvr) UpdateChat(
	ctx context.Context,
	chatID string,
	messages []*Message,
) (*Chat, error) {
	chat, err := svr.Chat(ctx, chatID)
	if err != nil {
		return nil, err
	}

	chat.UpdateMessages(messages)

	return svr.repo.UpdateChat(ctx, chat)
}

// DeleteChat deletes a chat by ID
func (svr *ChatSvr) DeleteChat(ctx context.Context, chatID string) (bool, error) {
	return svr.repo.DeleteChat(ctx, chatID)
}

// TODO: Implement
func (svr *ChatSvr) GenerateShareHTML(ctx context.Context, chatID string) (string, error) {
	svr.Warn("GenerateShareHTML not implemented", chatID, ctx)
	return "TODO-implement", nil
}

// GetUnixTimestamp returns current time as 13-digit unix timestamp (milliseconds)
func GetUnixTimestamp() int64 { return time.Now().UnixMilli() }

// GetISO8601Timestamp returns current timestamp in ISO8601 format with timezone offset
func GetISO8601Timestamp() time.Time { return time.Now() }

// GenerateChatID generates a unique ID (6 characters)
// Generate UUID and take first 6 characters of hex representation
func GenerateChatID() string {
	temp := strings.ReplaceAll(uuid.New().String(), "-", "")[:6]
	return strings.ReplaceAll(temp, "-", "")
}
