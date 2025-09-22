package client

import (
	"strings"
	"testing"
	"time"
)

func TestChat_UpdateMessages(t *testing.T) {
	tests := []struct {
		name     string
		chat     *Chat
		messages []*Message
		expected int // expected number of non-system messages
	}{
		{
			name: "filter out system messages",
			chat: &Chat{ID: "test1"},
			messages: []*Message{
				{Role: "user", Content: "Hello"},
				{Role: "system", Content: "System message"},
				{Role: "assistant", Content: "Hi there"},
				{Role: "system", Content: "Another system message"},
			},
			expected: 2,
		},
		{
			name: "no system messages",
			chat: &Chat{ID: "test2"},
			messages: []*Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
			},
			expected: 2,
		},
		{
			name: "all system messages",
			chat: &Chat{ID: "test3"},
			messages: []*Message{
				{Role: "system", Content: "System message 1"},
				{Role: "system", Content: "System message 2"},
			},
			expected: 0,
		},
		{
			name:     "empty messages",
			chat:     &Chat{ID: "test4"},
			messages: []*Message{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalTime := tt.chat.UpdateTime
			tt.chat.UpdateMessages(tt.messages)

			if len(tt.chat.Messages) != tt.expected {
				t.Errorf(
					"UpdateMessages() got %d messages, want %d",
					len(tt.chat.Messages),
					tt.expected,
				)
			}

			// Check that system messages are filtered out
			for _, msg := range tt.chat.Messages {
				if msg.Role == "system" {
					t.Errorf("UpdateMessages() should filter out system messages, but found one")
				}
			}

			// Check that UpdateTime was updated
			if !tt.chat.UpdateTime.After(originalTime) {
				t.Errorf("UpdateMessages() should update UpdateTime")
			}
		})
	}
}

func TestGetUnixTimestamp(t *testing.T) {
	before := time.Now().UnixMilli()
	timestamp := GetUnixTimestamp()
	after := time.Now().UnixMilli()

	if timestamp < before || timestamp > after {
		t.Errorf("GetUnixTimestamp() = %d, should be between %d and %d", timestamp, before, after)
	}

	// Test that it returns milliseconds (13 digits for current time)
	if timestamp < 1000000000000 { // Less than 13 digits
		t.Errorf("GetUnixTimestamp() = %d, should return milliseconds (13+ digits)", timestamp)
	}
}

func TestGetISO8601Timestamp(t *testing.T) {
	before := time.Now()
	timestamp := GetISO8601Timestamp()
	after := time.Now()

	if timestamp.Before(before) || timestamp.After(after) {
		t.Errorf("GetISO8601Timestamp() should return current time")
	}

	// Test that it's a valid time
	if timestamp.IsZero() {
		t.Errorf("GetISO8601Timestamp() should not return zero time")
	}
}

func TestGenerateID(t *testing.T) {
	// Test that it generates a 6-character ID
	id := GenerateChatID()
	if len(id) != 6 {
		t.Errorf("GenerateID() = %s, length should be 6, got %d", id, len(id))
	}

	// Test that it doesn't contain dashes
	if strings.Contains(id, "-") {
		t.Errorf("GenerateID() = %s, should not contain dashes", id)
	}

	// Test that multiple calls generate different IDs
	id2 := GenerateChatID()
	if id == id2 {
		t.Errorf("GenerateID() should generate unique IDs, got same ID twice: %s", id)
	}

	// Test that it only contains valid hex characters
	validChars := "0123456789abcdef"
	for _, char := range id {
		if !strings.ContainsRune(validChars, char) {
			t.Errorf("GenerateID() = %s, contains invalid character: %c", id, char)
		}
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	// Generate multiple IDs and check for uniqueness
	ids := make(map[string]bool)
	iterations := 1000

	for range iterations {
		id := GenerateChatID()
		if ids[id] {
			t.Errorf("GenerateID() generated duplicate ID: %s", id)
		}
		ids[id] = true
	}

	if len(ids) != iterations {
		t.Errorf("Expected %d unique IDs, got %d", iterations, len(ids))
	}
}

func TestChat_Fields(t *testing.T) {
	// Test Chat struct initialization
	now := time.Now()
	messages := []*Message{
		{Role: "user", Content: "test message"},
	}

	chat := &Chat{
		ID:         "test-id",
		CreateTime: now,
		UpdateTime: now,
		Messages:   messages,
	}

	// Verify all fields are set correctly
	if chat.ID != "test-id" {
		t.Errorf("Expected ID 'test-id', got '%s'", chat.ID)
	}
	if !chat.CreateTime.Equal(now) {
		t.Errorf("Expected CreateTime %v, got %v", now, chat.CreateTime)
	}
	if !chat.UpdateTime.Equal(now) {
		t.Errorf("Expected UpdateTime %v, got %v", now, chat.UpdateTime)
	}
	if len(chat.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(chat.Messages))
	}
}

// Benchmark tests
func BenchmarkGenerateIDChat(b *testing.B) {
	for b.Loop() {
		GenerateChatID()
	}
}

func BenchmarkGetUnixTimestamp(b *testing.B) {
	for b.Loop() {
		GetUnixTimestamp()
	}
}

func BenchmarkGetISO8601Timestamp(b *testing.B) {
	for b.Loop() {
		GetISO8601Timestamp()
	}
}
