package client

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
)

var _ MCPSvrConfigRepo = (*MCPSvrConfigFileRepo)(nil)

// FileRepo implements ChatRepository using file storage with async operations
type MCPSvrConfigFileRepo struct {
	log.Logger

	dataFile string
	mu       sync.RWMutex // Read-write mutex for thread safety

	cache   map[string]*MCPSvrItem // In-memory cache
	cacheMu sync.RWMutex           // Separate mutex for cache operations
}

func NewMCPSvrConfigFileRepo(path string, logger log.Logger) (*MCPSvrConfigFileRepo, error) {
	file, err := ExpandUser(path)
	if err != nil {
		return nil, fmt.Errorf("failed to expand user: %w", err)
	}

	if err := EnsureFileExistsSync(file); err != nil {
		return nil, fmt.Errorf("failed to ensure file exists: %w", err)
	}

	repo := &MCPSvrConfigFileRepo{
		Logger:   logger,
		dataFile: file,

		cache: make(map[string]*MCPSvrItem),
	}

	if err := repo.loadCacheSync(); err != nil {
		repo.Errorf("failed to load initial data: %v", err)
		return nil, fmt.Errorf("failed to load initial data: %w", err)
	}

	return repo, nil
}

func (r *MCPSvrConfigFileRepo) loadCacheSync() error {
	// NOTE: Load mcp server config data from file
	r.mu.RLock()
	defer r.mu.RUnlock()

	configs, err := loadMCPServerConfigsFromJSONL(r.dataFile)
	if err != nil {
		r.Errorf("failed to load initial data: %v", err)
		return fmt.Errorf("failed to load initial data: %w", err)
	}

	// NOTE: add mcp server config to cache
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	for _, config := range configs {
		r.cache[config.Name] = config
	}

	return nil
}

func (r *MCPSvrConfigFileRepo) persistCache() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()

	// NOTE: Convert cache to slice and sort by name
	configs := make([]*MCPSvrItem, 0, len(r.cache))
	for _, config := range r.cache {
		configs = append(configs, config)
	}
	if len(configs) > 0 {
		sort.Slice(configs, func(i, j int) bool {
			return configs[i].Name < configs[j].Name
		})
	}

	err := persistMCPServerConfigToJSONL(r.dataFile, configs)
	if err != nil {
		r.Errorf("failed to persist cache: %v", err)
		return fmt.Errorf("failed to persist cache: %w", err)
	}

	return nil
}

// GetMCPServerConfigByName returns the mcp server config by name, or error if not found
func (r *MCPSvrConfigFileRepo) MCPServerConfigByName(name string) (*MCPSvrItem, error) {
	if name == "" {
		r.Errorf("name is empty")
		return nil, errors.New("name is empty")
	}

	// NOTE: get mcp server config from cache
	r.cacheMu.RLock()
	if item, ok := r.cache[name]; ok {
		r.cacheMu.RUnlock()

		r.Infof("mcp server [%s] found in cache", name)
		return item, nil
	}
	r.cacheMu.RUnlock()

	return nil, fmt.Errorf("mcp server [%s] not found", name)
}

func (r *MCPSvrConfigFileRepo) AllMCPServerConfigs() []*MCPSvrItem {
	items := make([]*MCPSvrItem, 0, len(r.cache))

	r.cacheMu.RLock()
	for _, val := range r.cache {
		items = append(items, val)
	}
	r.cacheMu.RUnlock()

	r.Infof("Load %d MCP Servers", len(items))

	return items
}

func (r *MCPSvrConfigFileRepo) UpdateMCPServerConfigByName(item *MCPSvrItem) error {
	if item == nil || item.Name == "" {
		r.Errorf("name or item is empty")
		return errors.New("name or item is empty")
	}

	// NOTE: update mcp server config in cache
	r.cacheMu.Lock()
	oldCahce, ok := r.cache[item.Name]
	if !ok {
		r.Warnf("mcp server [%s] not found, add it to cache ...", item.Name)
	}
	r.cache[item.Name] = item
	r.cacheMu.Unlock()

	// NOTE: persist cache
	if err := r.persistCache(); err != nil {
		if ok {
			r.Errorf("failed to persist cache: %v => rollback", err)

			// Rollback cache change
			r.cacheMu.Lock()
			r.cache[item.Name] = oldCahce
			r.cacheMu.Unlock()
			return fmt.Errorf("failed to persist cache: %w => rollback", err)
		}

		return fmt.Errorf("failed to persist cache: %w", err)
	}

	r.Infof("updated mcp server config in cache and persisted: %s", item.Name)

	return nil
}

func (r *MCPSvrConfigFileRepo) DeleteMCPServerConfigByName(name string) error {
	if name == "" {
		r.Errorf("name is empty")
		return errors.New("name is empty")
	}

	// NOTE: delete mcp server config from cache
	r.cacheMu.Lock()
	oldCache, ok := r.cache[name]
	if !ok {
		r.cacheMu.Unlock()
		r.Warnf("mcp server [%s] not found", name)
		return nil
	}

	delete(r.cache, name)
	r.cacheMu.Unlock()

	// NOTE: persist cache
	if err := r.persistCache(); err != nil {
		r.Errorf("failed to persist cache: %v => rollback", err)

		// Rollback cache change
		r.cacheMu.Lock()
		r.cache[name] = oldCache
		r.cacheMu.Unlock()

		return fmt.Errorf("failed to persist cache: %w => rollback", err)
	}

	r.Infof("deleted mcp server config in cache and persisted: %s", name)

	return nil
}

// loadMCPServerConfigsFromJSONL loads MCP configs from the JSONL file
func loadMCPServerConfigsFromJSONL(jsonl string) ([]*MCPSvrItem, error) {
	file, err := os.Open(jsonl) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	items := make([]*MCPSvrItem, 0, 128)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		item := &MCPSvrItem{}
		if err := sonic.UnmarshalString(line, item); err != nil {
			continue // skip invalid lines
		}

		items = append(items, item)
	}

	return items, scanner.Err()
}

// persistMCPServerConfigToJSONL writes MCP configs to the JSONL file
func persistMCPServerConfigToJSONL(jsonl string, configs []*MCPSvrItem) error {
	file, err := os.Create(jsonl) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	for _, config := range configs {
		data, err := sonic.Marshal(config)
		if err != nil {
			return err
		}

		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}

	return nil
}
