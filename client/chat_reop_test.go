package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Mock logger for testing
type discardLogger struct{}

func (m *discardLogger) Sync() {}

func (m *discardLogger) Debug(args ...any) {}

func (m *discardLogger) Debugf(template string, args ...any) {}

func (m *discardLogger) Debugw(msg string, keysAndValues ...any) {}

func (m *discardLogger) Debugln(args ...any) {}

func (m *discardLogger) Info(args ...any) {}

func (m *discardLogger) Infof(template string, args ...any) {}

func (m *discardLogger) Infow(msg string, keysAndValues ...any) {}

func (m *discardLogger) Infoln(args ...any) {}

func (m *discardLogger) Warn(args ...any) {}

func (m *discardLogger) Warnf(template string, args ...any) {}

func (m *discardLogger) Warnw(msg string, keysAndValues ...any) {}

func (m *discardLogger) Warnln(args ...any) {}

func (m *discardLogger) Error(args ...any) {}

func (m *discardLogger) Errorf(template string, args ...any) {}

func (m *discardLogger) Errorw(msg string, keysAndValues ...any) {}

func (m *discardLogger) Errorln(args ...any) {}

func (m *discardLogger) Panic(args ...any) {}

func (m *discardLogger) Panicf(template string, args ...any) {}

func (m *discardLogger) Panicw(msg string, keysAndValues ...any) {}

func (m *discardLogger) Panicln(args ...any) {}

func (m *discardLogger) Fatal(args ...any) {}

func (m *discardLogger) Fatalf(template string, args ...any) {}

func (m *discardLogger) Fatalw(msg string, keysAndValues ...any) {}

func (m *discardLogger) Fatalln(args ...any) {}

func createTempFile(t *testing.T) string {
	tmpDir := t.TempDir()
	return filepath.Join(tmpDir, "test_chats.jsonl")
}

func createTestChat(id string) *Chat {
	return &Chat{
		ID:         id,
		CreateTime: time.Now(),
		UpdateTime: time.Now(),
		Messages: []*Message{
			{
				Role:     "user",
				Content:  fmt.Sprintf("Test message for chat %s", id),
				Model:    "gpt-4",
				Provider: "openai",
			},
		},
	}
}

func TestNewFileRepository(t *testing.T) {
	tests := []struct {
		name        string
		dataFile    string
		workerCount int
		expectError bool
	}{
		{
			name:        "valid parameters",
			dataFile:    createTempFile(t),
			workerCount: 2,
			expectError: false,
		},
		{
			name:        "zero worker count uses default",
			dataFile:    createTempFile(t),
			workerCount: 0,
			expectError: false,
		},
		{
			name:        "negative worker count uses default",
			dataFile:    createTempFile(t),
			workerCount: -1,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := NewChatFileRepository(tt.dataFile, tt.workerCount, &discardLogger{})

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if repo != nil {
				defer repo.Close()

				// Verify file was created
				if _, err := os.Stat(tt.dataFile); os.IsNotExist(err) {
					t.Errorf("Data file was not created: %s", tt.dataFile)
				}
			}
		})
	}
}

func TestFileRepo_AddChat(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	chat := createTestChat("test-add-1")

	// Test adding a chat
	addedChat, err := repo.AddChat(ctx, chat)
	if err != nil {
		t.Errorf("AddChat() error = %v", err)
	}
	if addedChat == nil {
		t.Errorf("AddChat() returned nil chat")
	}
	if addedChat.ID != chat.ID {
		t.Errorf("AddChat() returned chat with wrong ID: got %s, want %s", addedChat.ID, chat.ID)
	}

	// Verify chat was added to cache
	retrievedChat, err := repo.Chat(ctx, chat.ID)
	if err != nil {
		t.Errorf("GetChat() error = %v", err)
	}
	if retrievedChat == nil {
		t.Errorf("GetChat() returned nil after adding chat")
	}
	if retrievedChat.ID != chat.ID {
		t.Errorf("GetChat() returned wrong chat ID: got %s, want %s", retrievedChat.ID, chat.ID)
	}
}

func TestFileRepo_GetChat(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	chat := createTestChat("test-get-1")

	// Add a chat first
	_, err = repo.AddChat(ctx, chat)
	if err != nil {
		t.Fatalf("Failed to add chat: %v", err)
	}

	// Test getting existing chat
	retrievedChat, err := repo.Chat(ctx, chat.ID)
	if err != nil {
		t.Errorf("GetChat() error = %v", err)
	}
	if retrievedChat == nil {
		t.Errorf("GetChat() returned nil for existing chat")
	}
	if retrievedChat.ID != chat.ID {
		t.Errorf("GetChat() returned wrong chat: got %s, want %s", retrievedChat.ID, chat.ID)
	}

	// Test getting non-existent chat
	nonExistentChat, err := repo.Chat(ctx, "non-existent")
	if err != nil {
		t.Errorf("GetChat() error for non-existent chat = %v", err)
	}
	if nonExistentChat != nil {
		t.Errorf("GetChat() should return nil for non-existent chat")
	}
}

func TestFileRepo_UpdateChat(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	chat := createTestChat("test-update-1")

	// Add a chat first
	_, err = repo.AddChat(ctx, chat)
	if err != nil {
		t.Fatalf("Failed to add chat: %v", err)
	}

	// Update the chat
	chat.Messages = append(chat.Messages, &Message{
		Role:    "assistant",
		Content: "Updated message",
	})
	chat.UpdateTime = time.Now()

	updatedChat, err := repo.UpdateChat(ctx, chat)
	if err != nil {
		t.Errorf("UpdateChat() error = %v", err)
	}
	if updatedChat == nil {
		t.Errorf("UpdateChat() returned nil")
	}
	if len(updatedChat.Messages) != 2 {
		t.Errorf("UpdateChat() expected 2 messages, got %d", len(updatedChat.Messages))
	}

	// Test updating non-existent chat
	nonExistentChat := createTestChat("non-existent")
	_, err = repo.UpdateChat(ctx, nonExistentChat)
	if err == nil {
		t.Errorf("UpdateChat() should return error for non-existent chat")
	}
}

func TestFileRepo_DeleteChat(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	chat := createTestChat("test-delete-1")

	// Add a chat first
	_, err = repo.AddChat(ctx, chat)
	if err != nil {
		t.Fatalf("Failed to add chat: %v", err)
	}

	// Delete the chat
	deleted, err := repo.DeleteChat(ctx, chat.ID)
	if err != nil {
		t.Errorf("DeleteChat() error = %v", err)
	}
	if !deleted {
		t.Errorf("DeleteChat() should return true for successful deletion")
	}

	// Verify chat was deleted
	retrievedChat, err := repo.Chat(ctx, chat.ID)
	if err != nil {
		t.Errorf("GetChat() error after deletion = %v", err)
	}
	if retrievedChat != nil {
		t.Errorf("GetChat() should return nil after deletion")
	}

	// Test deleting non-existent chat
	deleted, err = repo.DeleteChat(ctx, "non-existent")
	if err != nil {
		t.Errorf("DeleteChat() error for non-existent chat = %v", err)
	}
	if deleted {
		t.Errorf("DeleteChat() should return false for non-existent chat")
	}
}

func TestFileRepo_ListChats(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	// Add multiple chats
	chats := []*Chat{
		createTestChat("chat-1"),
		createTestChat("chat-2"),
		createTestChat("chat-3"),
	}

	for _, chat := range chats {
		_, err := repo.AddChat(ctx, chat)
		if err != nil {
			t.Fatalf("Failed to add chat %s: %v", chat.ID, err)
		}
		time.Sleep(10 * time.Millisecond) // Ensure different create times
	}

	// Test listing all chats
	allChats, err := repo.ListChats(ctx, nil, nil, nil, 10)
	if err != nil {
		t.Errorf("ListChats() error = %v", err)
	}
	if len(allChats) != 3 {
		t.Errorf("ListChats() expected 3 chats, got %d", len(allChats))
	}

	// Test with limit
	limitedChats, err := repo.ListChats(ctx, nil, nil, nil, 2)
	if err != nil {
		t.Errorf("ListChats() with limit error = %v", err)
	}
	if len(limitedChats) != 2 {
		t.Errorf("ListChats() with limit expected 2 chats, got %d", len(limitedChats))
	}

	// Test with keyword filter
	keyword := "chat-1"
	filteredChats, err := repo.ListChats(ctx, &keyword, nil, nil, 10)
	if err != nil {
		t.Errorf("ListChats() with keyword error = %v", err)
	}
	if len(filteredChats) != 1 {
		t.Errorf("ListChats() with keyword expected 1 chat, got %d", len(filteredChats))
	}

	// Test with model filter
	model := "gpt-4"
	modelFilteredChats, err := repo.ListChats(ctx, nil, &model, nil, 10)
	if err != nil {
		t.Errorf("ListChats() with model filter error = %v", err)
	}
	if len(modelFilteredChats) != 3 {
		t.Errorf("ListChats() with model filter expected 3 chats, got %d", len(modelFilteredChats))
	}

	// Test with provider filter
	provider := "openai"
	providerFilteredChats, err := repo.ListChats(ctx, nil, nil, &provider, 10)
	if err != nil {
		t.Errorf("ListChats() with provider filter error = %v", err)
	}
	if len(providerFilteredChats) != 3 {
		t.Errorf(
			"ListChats() with provider filter expected 3 chats, got %d",
			len(providerFilteredChats),
		)
	}
}

func TestFileRepo_AsyncOperations(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	chat := createTestChat("test-async-1")

	// Test async add
	resultCh := repo.AddChatAsync(ctx, chat)
	select {
	case result := <-resultCh:
		if result.Error != nil {
			t.Errorf("AddChatAsync() error = %v", result.Error)
		}
		if result.Data == nil {
			t.Errorf("AddChatAsync() returned nil data")
		}
	case <-time.After(5 * time.Second):
		t.Errorf("AddChatAsync() timed out")
	}

	// Test async get
	getCh := repo.GetChatAsync(ctx, chat.ID)
	select {
	case result := <-getCh:
		if result.Error != nil {
			t.Errorf("GetChatAsync() error = %v", result.Error)
		}
		if result.Data == nil {
			t.Errorf("GetChatAsync() returned nil data")
		}
	case <-time.After(5 * time.Second):
		t.Errorf("GetChatAsync() timed out")
	}

	// Test async list
	listCh := repo.ListChatsAsync(ctx, nil, nil, nil, 10)
	select {
	case result := <-listCh:
		if result.Error != nil {
			t.Errorf("ListChatsAsync() error = %v", result.Error)
		}
		chats := result.Data.([]*Chat)
		if len(chats) == 0 {
			t.Errorf("ListChatsAsync() returned empty list")
		}
	case <-time.After(5 * time.Second):
		t.Errorf("ListChatsAsync() timed out")
	}
}

func TestFileRepo_ContextCancellation(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	chat := createTestChat("test-cancel-1")

	// Test that cancelled context returns error
	_, err = repo.AddChat(ctx, chat)
	if err == nil {
		t.Errorf("AddChat() with cancelled context should return error")
	}
	if err != context.Canceled {
		t.Errorf("AddChat() with cancelled context should return context.Canceled, got %v", err)
	}
}

func TestFileRepo_Close(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Close the repository
	err = repo.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Test that operations after close return error
	ctx := context.Background()
	chat := createTestChat("test-after-close")

	resultCh := repo.AddChatAsync(ctx, chat)
	select {
	case result := <-resultCh:
		if result.Error == nil {
			t.Errorf("AddChatAsync() after close should return error")
		}
		if !strings.Contains(result.Error.Error(), "shutdown") {
			t.Errorf(
				"AddChatAsync() after close should return shutdown error, got %v",
				result.Error,
			)
		}
	case <-time.After(1 * time.Second):
		t.Errorf("AddChatAsync() after close timed out")
	}

	// Test that closing again doesn't error
	err = repo.Close()
	if err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

func TestExpandUser(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
		hasError bool
	}{
		{
			name:     "no tilde",
			path:     "/absolute/path",
			expected: "/absolute/path",
			hasError: false,
		},
		{
			name:     "tilde only",
			path:     "~",
			expected: "", // Will be set to home dir
			hasError: false,
		},
		{
			name:     "tilde with path",
			path:     "~/documents/file.txt",
			expected: "", // Will be set to home dir + path
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandUser(tt.path)

			if tt.hasError && err == nil {
				t.Errorf("ExpandUser() expected error but got none")
			}
			if !tt.hasError && err != nil {
				t.Errorf("ExpandUser() unexpected error: %v", err)
			}

			if !tt.hasError {
				if tt.name == "no tilde" && result != tt.path {
					t.Errorf("ExpandUser() = %s, want %s", result, tt.path)
				}
				if strings.HasPrefix(tt.path, "~") &&
					!strings.Contains(result, string(os.PathSeparator)) {
					t.Errorf("ExpandUser() should expand tilde to home directory")
				}
			}
		})
	}
}

func TestEnsureFileExistsSync(t *testing.T) {
	// Test with non-existent file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "subdir", "test.txt")

	err := EnsureFileExistsSync(testFile)
	if err != nil {
		t.Errorf("EnsureFileExistsSync() error = %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Errorf("EnsureFileExistsSync() did not create file")
	}

	// Test with existing file
	err = EnsureFileExistsSync(testFile)
	if err != nil {
		t.Errorf("EnsureFileExistsSync() error on existing file = %v", err)
	}
}

func TestLoadChatFromFile(t *testing.T) {
	// Create a temporary file with test data
	tmpFile := createTempFile(t)

	// Write test data
	testChats := []*Chat{
		createTestChat("load-test-1"),
		createTestChat("load-test-2"),
	}

	err := persistChatToFile(tmpFile, testChats)
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// Test loading
	loadedChats, err := loadChatFromFile(tmpFile)
	if err != nil {
		t.Errorf("loadChatFromFile() error = %v", err)
	}

	if len(loadedChats) != 2 {
		t.Errorf("loadChatFromFile() expected 2 chats, got %d", len(loadedChats))
	}

	// Test loading non-existent file
	_, err = loadChatFromFile("/non/existent/file")
	if err == nil {
		t.Errorf("loadChatFromFile() should return error for non-existent file")
	}
}

func TestPersistChatToFile(t *testing.T) {
	tmpFile := createTempFile(t)

	testChats := []*Chat{
		createTestChat("persist-test-1"),
		createTestChat("persist-test-2"),
	}

	err := persistChatToFile(tmpFile, testChats)
	if err != nil {
		t.Errorf("persistChatToFile() error = %v", err)
	}

	// Verify file exists and has content
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Errorf("Failed to read persisted file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Errorf("persistChatToFile() expected 2 lines, got %d", len(lines))
	}
}

func TestFileRepo_ConcurrentOperations(t *testing.T) {
	dataFile := createTempFile(t)
	repo, err := NewChatFileRepository(dataFile, 5, &discardLogger{})
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	numOperations := 50

	// Perform concurrent add operations
	errCh := make(chan error, numOperations)
	for i := range numOperations {
		go func(id int) {
			chat := createTestChat(fmt.Sprintf("concurrent-%d", id))
			_, err := repo.AddChat(ctx, chat)
			errCh <- err
		}(i)
	}

	// Wait for all operations to complete
	for i := range numOperations {
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Concurrent operation %d failed: %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("Concurrent operation %d timed out", i)
		}
	}

	// Verify all chats were added
	allChats, err := repo.ListChats(ctx, nil, nil, nil, numOperations+10)
	if err != nil {
		t.Errorf("ListChats() after concurrent operations error = %v", err)
	}
	if len(allChats) != numOperations {
		t.Errorf(
			"Expected %d chats after concurrent operations, got %d",
			numOperations,
			len(allChats),
		)
	}
}

// Benchmark tests
func BenchmarkFileRepo_AddChat(b *testing.B) {
	dataFile := createTempFile(&testing.T{})
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		b.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	for i := 0; b.Loop(); i++ {
		chat := createTestChat(fmt.Sprintf("bench-%d", i))
		_, err := repo.AddChat(ctx, chat)
		if err != nil {
			b.Errorf("AddChat() error = %v", err)
		}
	}
}

func BenchmarkFileRepo_GetChat(b *testing.B) {
	dataFile := createTempFile(&testing.T{})
	repo, err := NewChatFileRepository(dataFile, 2, &discardLogger{})
	if err != nil {
		b.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()

	// Add some chats first
	for i := range 100 {
		chat := createTestChat(fmt.Sprintf("bench-get-%d", i))
		_, err := repo.AddChat(ctx, chat)
		if err != nil {
			b.Fatalf("Failed to add chat: %v", err)
		}
	}

	for i := 0; b.Loop(); i++ {
		chatID := fmt.Sprintf("bench-get-%d", i%100)
		_, err := repo.Chat(ctx, chatID)
		if err != nil {
			b.Errorf("GetChat() error = %v", err)
		}
	}
}

func BenchmarkGenerateID(b *testing.B) {
	for b.Loop() {
		GenerateChatID()
	}
}
