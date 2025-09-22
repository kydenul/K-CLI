package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
	"github.com/spf13/cast"
)

// Operation types for async operations
type opType int

const (
	opListChats opType = iota
	opGetChat
	opAddChat
	opUpdateChat
	opDeleteChat
	opShutdown
)

const (
	DefaultOperationQueueSize = 100 // Default size of the operation queue
	DefaultWorkerCount        = 5   // Default number of worker goroutines
)

// ListChatsOption holds parameters for list chats operation
type ListChatsOption struct {
	keyword  *string
	model    *string
	provider *string
	limit    int
}

// opReq (operation request) represents an async operation request
type opReq struct {
	opType   opType
	data     any
	resultCh chan OpResp
}

// OpResp (operation response) represents the result of an async operation
type OpResp struct {
	Data  any
	Error error
}

var _ ChatRepo = (*FileRepo)(nil)

// FileRepo implements ChatRepository using file storage with async operations
type FileRepo struct {
	logger log.Logger

	dataFile string
	mu       sync.RWMutex // Read-write mutex for thread safety

	cache   map[string]*Chat // In-memory cache
	cacheMu sync.RWMutex     // Separate mutex for cache operations

	opCh     chan opReq     // Channel for async operations => operation queue
	workerWg sync.WaitGroup // WaitGroup for worker goroutines

	shutdownCh chan struct{} // Channel to signal shutdown
	isShutdown bool
	shutdownMu sync.RWMutex
}

// NewChatFileRepository creates a new FileRepository instance with async capabilities
func NewChatFileRepository(
	dataFile string,
	workerCount int,
	logger log.Logger,
) (*FileRepo, error) {
	dataFile, err := ExpandUser(dataFile)
	if err != nil {
		log.Panic("expand user error: " + err.Error())
	}

	if err := EnsureFileExistsSync(dataFile); err != nil {
		log.Panic("ensure file exists error: " + err.Error())
	}

	fr := &FileRepo{
		logger: logger,

		dataFile:   dataFile,
		cache:      make(map[string]*Chat),
		opCh:       make(chan opReq, DefaultOperationQueueSize),
		shutdownCh: make(chan struct{}),
	}

	// Initialize the file
	if err := EnsureFileExistsSync(fr.dataFile); err != nil {
		return nil, fmt.Errorf("failed to initialize file: %w", err)
	}

	// Load initial data into cache
	if err := fr.loadCacheSync(); err != nil {
		return nil, fmt.Errorf("failed to load initial data: %w", err)
	}

	// Start worker goroutines
	if workerCount <= 0 {
		workerCount = DefaultWorkerCount
	}
	for i := 0; i < workerCount; i++ {
		fr.workerWg.Add(1)
		go fr.worker(context.Background(), i)
	}

	return fr, nil
}

// worker processes async operations
func (fr *FileRepo) worker(ctx context.Context, workerID int) {
	defer fr.workerWg.Done()

	log.Infof("Worker %d started\n", workerID)

	for {
		select {
		case <-fr.shutdownCh:
			log.Infof("Worker %d shutting down\n", workerID)
			return

		case req := <-fr.opCh:
			fr.processOperation(ctx, req)
		}
	}
}

// processOperation processes a single operation
func (fr *FileRepo) processOperation(ctx context.Context, req opReq) {
	var result OpResp

	switch req.opType {
	case opListChats:
		params, ok := req.data.(ListChatsOption)
		if !ok {
			result = OpResp{Error: errors.New("invalid operation data")}
			break
		}

		chats, err := fr.listChatsInternal(
			params.keyword,
			params.model,
			params.provider,
			params.limit,
		)
		result = OpResp{Data: chats, Error: err}

	case opGetChat:
		chatID := cast.ToString(req.data)
		chat, err := fr.getChatInternal(chatID)
		result = OpResp{Data: chat, Error: err}

	case opAddChat:
		chat, ok := req.data.(*Chat)
		if !ok {
			result = OpResp{Error: errors.New("invalid operation data")}
			break
		}

		addedChat, err := fr.addChatInternal(chat)
		result = OpResp{Data: addedChat, Error: err}

	case opUpdateChat:
		chat, ok := req.data.(*Chat)
		if !ok {
			result = OpResp{Error: errors.New("invalid operation data")}
			break
		}

		updatedChat, err := fr.updateChatInternal(chat)
		result = OpResp{Data: updatedChat, Error: err}

	case opDeleteChat:
		chatID := cast.ToString(req.data)
		deleted, err := fr.deleteChatInternal(chatID)
		result = OpResp{
			Data:  deleted,
			Error: err,
		}

	default:
		result = OpResp{Error: fmt.Errorf("unknown operation type: %d", req.opType)}
	}

	// Send result back
	select {
	case req.resultCh <- result:
	case <-ctx.Done():
		// Context cancelled, don't block
	}
}

// loadCacheSync loads all chats into memory cache
func (fr *FileRepo) loadCacheSync() error {
	// NOTE: Load chat data from file
	fr.mu.RLock()
	defer fr.mu.RUnlock()

	chats, err := loadChatFromFile(fr.dataFile)
	if err != nil {
		fr.logger.Errorf("failed to load initial data: %v", err)
		return fmt.Errorf("failed to load initial data: %w", err)
	}

	// NOTE: Add chat to cache
	fr.cacheMu.Lock()
	defer fr.cacheMu.Unlock()

	for _, chat := range chats {
		fr.cache[chat.ID] = chat
	}

	return nil
}

// persistCache writes the cache with chats sorted by create time to file
func (fr *FileRepo) persistCache() error {
	fr.mu.Lock()
	defer fr.mu.Unlock()

	fr.cacheMu.RLock()
	defer fr.cacheMu.RUnlock()

	// NOTE: Convert cache to slice and sort by create time
	chats := make([]*Chat, 0, len(fr.cache))
	for _, chat := range fr.cache {
		chats = append(chats, chat)
	}
	if len(chats) > 0 {
		sort.Slice(chats, func(i, j int) bool {
			return chats[i].CreateTime.After(chats[j].CreateTime)
		})
	}

	err := persistChatToFile(fr.dataFile, chats)
	if err != nil {
		fr.logger.Errorf("failed to persist cache: %v", err)
		return fmt.Errorf("failed to persist cache: %w", err)
	}

	return err
}

// listChatsInternal lists all chats in cache. Supports filtering by keyword, model, provider
func (fr *FileRepo) listChatsInternal(
	keyword, model, provider *string,
	limit int,
) ([]*Chat, error) {
	fr.cacheMu.RLock()
	defer fr.cacheMu.RUnlock()

	// Convert cache to slice
	allChats := make([]*Chat, 0, len(fr.cache))
	for _, chat := range fr.cache {
		allChats = append(allChats, chat)
	}

	// Sort by create_time in descending order
	sort.Slice(allChats, func(i, j int) bool {
		return allChats[i].CreateTime.After(allChats[j].CreateTime)
	})

	// Apply filters
	allChats = fr.filterChatsByKeyword(
		allChats,
		keyword,
		model,
		provider,
		limit,
	)

	// Apply limit
	if len(allChats) > limit {
		allChats = allChats[:limit]
	}

	return allChats, nil
}

// filterChatsByKeyword filters chats by keyword, model, provider, and limit
//
//nolint:cyclop
func (fr *FileRepo) filterChatsByKeyword(
	allChats []*Chat,
	keyword, model, provider *string,
	limit int,
) []*Chat {
	if len(allChats) == 0 ||
		(keyword == nil && model == nil && provider == nil) {
		fr.logger.Infof("no chats found or no filters specified")

		return allChats
	}

	// Apply filters if any are specified
	if keyword != nil || model != nil || provider != nil {
		var filteredChats []*Chat

		for _, chat := range allChats {
			chatMatches := false

			// Check each message in the chat
			for _, msg := range chat.Messages {
				matches := true

				// Apply keyword filter if specified
				if keyword != nil {
					keywordLower := strings.ToLower(*keyword)
					contentMatches := false

					switch content := msg.Content.(type) {
					case string:
						if strings.Contains(strings.ToLower(content), keywordLower) {
							contentMatches = true
						}
					case []any:
						for _, part := range content {
							if partMap, ok := part.(map[string]any); ok {
								if text, exists := partMap["text"].(string); exists {
									if strings.Contains(strings.ToLower(text), keywordLower) {
										contentMatches = true
										break
									}
								}
							}
						}
					}

					if !contentMatches {
						// matches = false
						continue
					}
				}

				// Apply model filter if specified
				if model != nil {
					if msg.Model == "" ||
						!strings.Contains(strings.ToLower(msg.Model), strings.ToLower(*model)) {
						// matches = false
						continue
					}
				}

				// Apply provider filter if specified
				if provider != nil {
					if msg.Provider == "" ||
						!strings.Contains(
							strings.ToLower(msg.Provider),
							strings.ToLower(*provider),
						) {
						// matches = false
						continue
					}
				}

				// If all specified filters match, add the chat and break
				if matches {
					filteredChats = append(filteredChats, chat)
					chatMatches = true
					break
				}
			}

			if chatMatches && len(filteredChats) >= limit {
				break
			}
		}

		allChats = filteredChats
	}

	return allChats
}

// getChatInternal returns a chat from cache
func (fr *FileRepo) getChatInternal(chatID string) (*Chat, error) {
	fr.cacheMu.RLock()
	defer fr.cacheMu.RUnlock()

	if chat, ok := fr.cache[chatID]; ok {
		return chat, nil
	}

	return nil, nil // Not found
}

// addChatInternal adds a chat to cache and persists to file
func (fr *FileRepo) addChatInternal(chat *Chat) (*Chat, error) {
	fr.cacheMu.Lock()
	fr.cache[chat.ID] = chat
	fr.logger.Infof("added chat to cache: %s", chat.ID)
	fr.cacheMu.Unlock()

	// Persist to file
	if err := fr.persistCache(); err != nil {
		// Rollback cache change
		fr.cacheMu.Lock()
		fr.logger.Warnf("failed to persist cache: %v", err)
		delete(fr.cache, chat.ID)
		fr.cacheMu.Unlock()

		return nil, err
	}

	return chat, nil
}

// updateChatInternal updates a chat in cache and persists to file
func (fr *FileRepo) updateChatInternal(chat *Chat) (*Chat, error) {
	fr.cacheMu.Lock()
	if _, exists := fr.cache[chat.ID]; !exists {
		fr.cacheMu.Unlock()
		log.Errorf("chat with id %s not found", chat.ID)
		return nil, fmt.Errorf("chat with id %s not found", chat.ID)
	}

	oldChat := fr.cache[chat.ID]
	fr.cache[chat.ID] = chat
	fr.cacheMu.Unlock()

	// Persist to file
	if err := fr.persistCache(); err != nil {
		// Rollback cache change
		fr.cacheMu.Lock()
		fr.cache[chat.ID] = oldChat
		fr.cacheMu.Unlock()

		fr.logger.Warnf("failed to persist cache: %v => rollback", err)

		return nil, err
	}

	fr.logger.Infof("updated chat in cache and persisted: %s", chat.ID)

	return chat, nil
}

// deleteChatInternal deletes a chat from cache and persists to file
func (fr *FileRepo) deleteChatInternal(chatID string) (bool, error) {
	fr.cacheMu.Lock()
	if _, exists := fr.cache[chatID]; !exists {
		fr.cacheMu.Unlock()

		fr.logger.Warnf("chat with id %s not found", chatID)

		return false, nil
	}

	oldChat := fr.cache[chatID]
	delete(fr.cache, chatID)
	fr.cacheMu.Unlock()

	// Persist to file
	if err := fr.persistCache(); err != nil {
		// Rollback cache change
		fr.cacheMu.Lock()
		fr.cache[chatID] = oldChat
		fr.cacheMu.Unlock()

		fr.logger.Warnf("failed to persist cache: %v => rollback", err)

		return false, err
	}

	fr.logger.Infof("deleted chat from cache and persisted: %s", chatID)

	return true, nil
}

// ListChatsAsync lists all chats from cache
func (fr *FileRepo) ListChatsAsync(
	ctx context.Context,
	keyword, model, provider *string,
	limit int,
) <-chan OpResp {
	resultCh := make(chan OpResp, 1)

	// NOTE: Check if repository is shutdown
	fr.shutdownMu.RLock()
	if fr.isShutdown {
		fr.shutdownMu.RUnlock()

		go func() { resultCh <- OpResp{Error: errors.New("repository is shutdown")} }()

		return resultCh
	}
	fr.shutdownMu.RUnlock()

	// NOTE: Send operation request to operation queue
	select {
	case fr.opCh <- opReq{
		opType:   opListChats,
		data:     ListChatsOption{keyword: keyword, model: model, provider: provider, limit: limit},
		resultCh: resultCh,
	}:
		fr.logger.Info("list chats operation enqueued")

	case <-ctx.Done():
		go func() {
			resultCh <- OpResp{Error: ctx.Err()}
		}()
	}

	return resultCh
}

// GetChatAsync returns a chat from cache
func (fr *FileRepo) GetChatAsync(ctx context.Context, chatID string) <-chan OpResp {
	resultCh := make(chan OpResp, 1)

	// NOTE: Check if repository is shutdown
	fr.shutdownMu.RLock()
	if fr.isShutdown {
		fr.shutdownMu.RUnlock()
		go func() {
			resultCh <- OpResp{Error: errors.New("repository is shutdown")}
		}()
		return resultCh
	}
	fr.shutdownMu.RUnlock()

	// NOTE: Send operation request to operation queue
	select {
	case fr.opCh <- opReq{
		opType:   opGetChat,
		data:     chatID,
		resultCh: resultCh,
	}:
		fr.logger.Info("get chat operation enqueued")

	case <-ctx.Done():
		go func() {
			resultCh <- OpResp{Error: ctx.Err()}
		}()
	}

	return resultCh
}

// AddChatAsync adds a chat to cache
func (fr *FileRepo) AddChatAsync(ctx context.Context, chat *Chat) <-chan OpResp {
	resultCh := make(chan OpResp, 1)

	// NOTE: Check if repository is shutdown
	fr.shutdownMu.RLock()
	if fr.isShutdown {
		fr.shutdownMu.RUnlock()
		go func() {
			resultCh <- OpResp{Error: errors.New("repository is shutdown")}
		}()
		return resultCh
	}
	fr.shutdownMu.RUnlock()

	// NOTE: Send operation request to operation queue
	select {
	case fr.opCh <- opReq{
		opType:   opAddChat,
		data:     chat,
		resultCh: resultCh,
	}:
		fr.logger.Info("add chat operation enqueued")

	case <-ctx.Done():
		go func() {
			resultCh <- OpResp{Error: ctx.Err()}
		}()
	}

	return resultCh
}

// UpdateChatAsync updates a chat in cache
func (fr *FileRepo) UpdateChatAsync(ctx context.Context, chat *Chat) <-chan OpResp {
	resultCh := make(chan OpResp, 1)

	// NOTE: Check if repository is shutdown
	fr.shutdownMu.RLock()
	if fr.isShutdown {
		fr.shutdownMu.RUnlock()
		go func() {
			resultCh <- OpResp{Error: errors.New("repository is shutdown")}
		}()
		return resultCh
	}
	fr.shutdownMu.RUnlock()

	// NOTE: Send operation request to operation queue
	select {
	case fr.opCh <- opReq{
		opType:   opUpdateChat,
		data:     chat,
		resultCh: resultCh,
	}:
		fr.logger.Info("update chat operation enqueued")

	case <-ctx.Done():
		go func() {
			resultCh <- OpResp{Error: ctx.Err()}
		}()
	}

	return resultCh
}

// DeleteChatAsync deletes a chat from cache
func (fr *FileRepo) DeleteChatAsync(
	ctx context.Context,
	chatID string,
) <-chan OpResp {
	resultCh := make(chan OpResp, 1)

	// NOTE: Check if repository is shutdown
	fr.shutdownMu.RLock()
	if fr.isShutdown {
		fr.shutdownMu.RUnlock()
		go func() {
			resultCh <- OpResp{Error: errors.New("repository is shutdown")}
		}()
		return resultCh
	}
	fr.shutdownMu.RUnlock()

	// NOTE: Send operation request to operation queue
	select {
	case fr.opCh <- opReq{
		opType:   opDeleteChat,
		data:     chatID,
		resultCh: resultCh,
	}:
		fr.logger.Info("delete chat operation enqueued")

	case <-ctx.Done():
		go func() {
			resultCh <- OpResp{Error: ctx.Err()}
		}()
	}

	return resultCh
}

// ListChatsAsync lists chats from cache
func (fr *FileRepo) ListChats(
	ctx context.Context,
	keyword, model, provider *string,
	limit int,
) ([]*Chat, error) {
	resultCh := fr.ListChatsAsync(ctx, keyword, model, provider, limit)
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return nil, result.Error
		}

		vChat, ok := result.Data.([]*Chat)
		if !ok {
			return nil, errors.New("invalid operation data")
		}

		return vChat, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetChatAsync returns a chat from cache
func (fr *FileRepo) Chat(ctx context.Context, chatID string) (*Chat, error) {
	resultCh := fr.GetChatAsync(ctx, chatID)
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return nil, result.Error
		}

		chat, ok := result.Data.(*Chat)
		if !ok {
			return nil, errors.New("invalid operation data")
		}

		return chat, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AddChatAsync adds a chat to cache
func (fr *FileRepo) AddChat(ctx context.Context, chat *Chat) (*Chat, error) {
	resultCh := fr.AddChatAsync(ctx, chat)
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return nil, result.Error
		}

		chat, ok := result.Data.(*Chat)
		if !ok {
			return nil, errors.New("invalid operation data")
		}

		return chat, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// UpdateChatAsync updates a chat in cache
func (fr *FileRepo) UpdateChat(ctx context.Context, chat *Chat) (*Chat, error) {
	resultCh := fr.UpdateChatAsync(ctx, chat)
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return nil, result.Error
		}

		chat, ok := result.Data.(*Chat)
		if !ok {
			return nil, errors.New("invalid operation data")
		}

		return chat, nil

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DeleteChatAsync deletes a chat from cache
func (fr *FileRepo) DeleteChat(ctx context.Context, chatID string) (bool, error) {
	resultCh := fr.DeleteChatAsync(ctx, chatID)
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return false, result.Error
		}

		succ, err := cast.ToBoolE(result.Data)
		if err != nil {
			return false, err
		}

		return succ, nil

	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Close shuts down the repository gracefully
func (fr *FileRepo) Close() error {
	fr.shutdownMu.Lock()
	if fr.isShutdown {
		fr.shutdownMu.Unlock()

		fr.logger.Info("Repository already closed")

		return nil
	}
	fr.isShutdown = true
	fr.shutdownMu.Unlock()

	// Signal all workers to shutdown
	close(fr.shutdownCh)

	// Wait for all workers to finish
	fr.workerWg.Wait()

	// Close operation channel
	close(fr.opCh)

	fr.logger.Info("Repository closed gracefully")
	return nil
}

// loadChatFromFile loads chat data from a file
func loadChatFromFile(file string) ([]*Chat, error) {
	f, err := os.Open(file) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	chats := make([]*Chat, 0, 128)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		dat := &Chat{}
		if err := sonic.UnmarshalString(line, dat); err != nil {
			continue // skip invalid lines
		}

		chats = append(chats, dat)
	}

	return chats, scanner.Err()
}

// persistChatToFile writes chat data to a file
func persistChatToFile(file string, chats []*Chat) error {
	f, err := os.Create(file) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close()

	for _, chat := range chats {
		data, err := sonic.Marshal(chat)
		if err != nil {
			return err
		}

		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}

	return nil
}

// ExpandUser expands the ~ in the beginning of a file path to the user's home directory
func ExpandUser(path string) (string, error) {
	// NOTE:
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// NOTE: Handle ~user
	if strings.HasPrefix(path, "~") && len(path) > 1 && path[1] != '/' && path[1] != '\\' {
		// Not found
		usernameEnd := strings.IndexAny(path[1:], "/\\")
		if usernameEnd == -1 {
			usernameEnd = len(path)
		}
		username := path[1 : usernameEnd+1]

		// return expanded path
		return filepath.Join(homeDir, username), nil
	}

	return filepath.Join(homeDir, path[1:]), nil
}

// ensureFileExists ensures that the data file exists, creating it if necessary
func EnsureFileExistsSync(file string) error {
	// Check if the file exists
	if _, err := os.Stat(file); os.IsNotExist(err) {
		dir := filepath.Dir(file)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("failed to create directory: %v", err)
		}

		// Create the file
		file, err := os.Create(file) //nolint:gosec
		if err != nil {
			return fmt.Errorf("failed to create file: %v", err)
		}
		defer file.Close()
	}

	return nil
}
