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

var _ PromptRepo = (*PromptFileRepo)(nil)

type PromptFileRepo struct {
	log.Logger

	dataFile string
	mtx      sync.RWMutex // Read-write mutex for thread safety

	cache    map[string]*PromptItem // In-memory cache
	cacheMtx sync.RWMutex           // Separate mutex for the cache
}

func NewPromptFileRepo(jsonl string, logger log.Logger) (*PromptFileRepo, error) {
	jsonl, err := ExpandUser(jsonl)
	if err != nil {
		logger.Panic("expand user error: " + err.Error())
		return nil, err
	}

	if err := EnsureFileExistsSync(jsonl); err != nil {
		logger.Panic("ensure file exists error: " + err.Error())
		return nil, err
	}

	repo := &PromptFileRepo{
		Logger:   logger,
		dataFile: jsonl,

		cache: make(map[string]*PromptItem),
	}

	if err := repo.loadCacheSync(); err != nil {
		repo.Errorf("failed to load initial data: %v", err)
		return nil, fmt.Errorf("failed to load initial data: %w", err)
	}

	return repo, nil
}

func (r *PromptFileRepo) loadCacheSync() error {
	// NOTE: Load prompt data from file
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	prompts, err := loadPromptFromJSONL(r.dataFile)
	if err != nil {
		r.Errorf("failed to load initial data: %v", err)
		return fmt.Errorf("failed to load initial data: %w", err)
	}

	// NOTE: add prompt to cache
	r.cacheMtx.Lock()
	defer r.cacheMtx.Unlock()

	for _, prompt := range prompts {
		r.cache[prompt.Name] = prompt
	}

	return nil
}

func (r *PromptFileRepo) persistCacheSync() error {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	r.cacheMtx.RLock()
	defer r.cacheMtx.RUnlock()

	// NOTE: Convert cache to slice and sort by name
	prompts := make([]*PromptItem, 0, len(r.cache))
	for _, prompt := range r.cache {
		prompts = append(prompts, prompt)
	}
	if len(prompts) > 0 {
		sort.Slice(prompts, func(i, j int) bool {
			return prompts[i].Name < prompts[j].Name
		})
	}

	if err := persistPromptToJSONL(r.dataFile, prompts); err != nil {
		r.Errorf("failed to persist cache: %v", err)
		return fmt.Errorf("failed to persist cache: %w", err)
	}

	return nil
}

func (r *PromptFileRepo) PromptByName(name string) (*PromptItem, error) {
	if name == "" {
		r.Errorf("name is empty")
		return nil, errors.New("name is empty")
	}

	// NOTE: get prompt from cache
	r.cacheMtx.RLock()
	if item, ok := r.cache[name]; ok {
		r.cacheMtx.RUnlock()

		r.Infof("prompt [%s] found in cache", name)
		return item, nil
	}
	r.cacheMtx.RUnlock()

	return nil, fmt.Errorf("prompt [%s] not found", name)
}

func (r *PromptFileRepo) AllPrompts() []*PromptItem {
	items := make([]*PromptItem, 0, len(r.cache))

	r.cacheMtx.RLock()
	for _, item := range r.cache {
		items = append(items, item)
	}
	r.cacheMtx.RUnlock()

	r.Infof("Found %d prompts", len(items))
	return items
}

func (r *PromptFileRepo) UpdatePromptByName(item *PromptItem) error {
	if item == nil || item.Name == "" {
		r.Errorf("name or item is empty")
		return errors.New("name or item is empty")
	}

	// NOTE: update prompt in cache
	r.cacheMtx.Lock()
	oldCache, ok := r.cache[item.Name]
	if !ok {
		r.Warnf("prompt [%s] not found, add it to cache ...", item.Name)
	}
	r.cache[item.Name] = item
	r.cacheMtx.Unlock()

	// NOTE: persist cache
	if err := r.persistCacheSync(); err != nil {
		if ok {
			r.Errorf("failed to persist cache: %v => rollback", err)

			// Rollback cache change
			r.cacheMtx.Lock()
			r.cache[item.Name] = oldCache
			r.cacheMtx.Unlock()
			return fmt.Errorf("failed to persist cache: %w", err)
		}

		r.Errorf("failed to persist cache: %v", err)
	}

	r.Infof("Update prompt in cache and persisted: %s", item.Name)

	return nil
}

func (r *PromptFileRepo) DeletePromptByName(name string) error {
	if name == "" {
		r.Errorf("name is empty")
		return errors.New("name is empty")
	}

	// NOTE: delete prompt from cache
	r.cacheMtx.Lock()
	oldCache, ok := r.cache[name]
	if !ok {
		r.cacheMtx.Unlock()
		r.Warnf("prompt [%s] not found", name)
		return nil
	}

	delete(r.cache, name)
	r.cacheMtx.Unlock()

	// NOTE: persist cache
	if err := r.persistCacheSync(); err != nil {
		r.Errorf("failed to persist cache: %v => rollback", err)

		// Rollback cache change
		r.cacheMtx.Lock()
		r.cache[name] = oldCache
		r.cacheMtx.Unlock()
		return fmt.Errorf("failed to persist cache: %w", err)
	}

	r.Infof("Delete prompt in cache and persisted: %s", name)

	return nil
}

// loadPromptFromJSONL loads prompts from the JSONL file
func loadPromptFromJSONL(jsonl string) ([]*PromptItem, error) {
	file, err := os.Open(jsonl) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	items := make([]*PromptItem, 0, 128)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		item := &PromptItem{}
		if err := sonic.UnmarshalString(line, item); err != nil {
			continue // skip invalid lines
		}

		items = append(items, item)
	}

	return items, nil
}

// persistPromptToJSONL writes prompts to the JSONL file
func persistPromptToJSONL(jsonl string, prompts []*PromptItem) error {
	file, err := os.Create(jsonl) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	for _, prompt := range prompts {
		data, err := sonic.Marshal(prompt)
		if err != nil {
			return err
		}

		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}

	return nil
}
