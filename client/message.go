package client

import (
	"fmt"
	"time"

	"github.com/bytedance/sonic"
)

const (
	DefaultContentType = "text"
)

type ContentPart struct {
	Text string `json:"text"`
	Type string `json:"type"`

	CacheControl map[string]any `json:"cache_control,omitempty"` // Claude-3
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []ContentPart

	Timestamp     *time.Time `json:"timestamp,omitempty"`
	UnixTimestamp int64      `json:"unix_timestamp,omitempty"`

	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ReasoningEffort  string         `json:"reasoning_effort,omitempty"`
	Links            []string       `json:"links,omitempty"`
	Images           []string       `json:"images,omitempty"`
	Model            string         `json:"model,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	ID               string         `json:"id,omitempty"`
	ParentID         string         `json:"parent_id,omitempty"`
	Server           string         `json:"server,omitempty"`
	Tool             string         `json:"tool,omitempty"`
	Arguments        map[string]any `json:"arguments,omitempty"`
}

// MessageOption contains optional fields for creating a message
type MessageOption struct {
	ReasoningContent string
	ReasoningEffort  string

	Links  []string
	Images []string

	Provider  string
	Model     string
	ID        string
	ParentID  string
	Server    string
	Tool      string
	Arguments map[string]any
}

func NewMessage(role, content string, timestamp time.Time, unixTimestamp int64) *Message {
	return &Message{
		Role:          role,
		Content:       content,
		Timestamp:     &timestamp,
		UnixTimestamp: unixTimestamp,
	}
}

// CreateMessage creates a Message object with optional fields using MessageOption
func NewMessageWithOption(role, content string, opt *MessageOption) *Message {
	message := NewMessage(role, content, GetISO8601Timestamp(), GetUnixTimestamp())

	// Apply optional fields if MessageOption is provided
	if opt != nil {
		if opt.ReasoningContent != "" {
			message.ReasoningContent = opt.ReasoningContent
		}
		if opt.ReasoningEffort != "" {
			message.ReasoningEffort = opt.ReasoningEffort
		}

		if opt.Links != nil {
			message.Links = opt.Links
		}
		if opt.Images != nil {
			message.Images = opt.Images
		}

		if opt.Provider != "" {
			message.Provider = opt.Provider
		}
		if opt.Model != "" {
			message.Model = opt.Model
		}

		if opt.ID != "" {
			message.ID = opt.ID
		}
		if opt.ParentID != "" {
			message.ParentID = opt.ParentID
		}
		if opt.Server != "" {
			message.Server = opt.Server
		}
		if opt.Tool != "" {
			message.Tool = opt.Tool
		}
		if opt.Arguments != nil {
			message.Arguments = opt.Arguments
		}
	}

	return message
}

// LoadMessageFromString loads a message from a JSON string
func LoadMessageFromJSON(str string) (*Message, error) {
	if str == "" {
		return nil, nil
	}

	message := &Message{}
	err := sonic.UnmarshalString(str, message)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	return message, nil
}

// SerializeMessageToString serializes a message to a JSON string
func ToJSON(message *Message) (string, error) {
	if message == nil {
		return "", nil
	}

	return sonic.MarshalString(message)
}
