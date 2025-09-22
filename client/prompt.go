package client

import (
	"github.com/kydenul/log"
)

const (
	DefaultPromptName             = "default"
	DefaultMCPPromptName          = "mcp"
	DefaultDeepResearchPromptName = "deep-research"
)

type PromptItem struct {
	Name        string `mapstructure:"name"`                  // Unique identifier for the prompt
	Content     string `mapstructure:"content"`               // The content of the prompt
	Description string `mapstructure:"description,omitempty"` // Optional description of the prompt's purpose
}

// PromptSvr 对应整个 MCP PromptSvr 文件结构
type PromptSvr struct {
	log.Logger

	repo PromptRepo
}

func NewPromptSvr(repo PromptRepo, logger log.Logger) *PromptSvr {
	svr := &PromptSvr{
		Logger: logger,
		repo:   repo,
	}
	svr.ensureDefaultPrompt()

	return svr
}

// GetPrompt returns the PromptItem by name
func (svr *PromptSvr) PromptByName(name string) *PromptItem {
	item, _ := svr.repo.PromptByName(name)

	return item
}

// AddPrompt adds a new prompt configuration or update existing one.
func (svr *PromptSvr) AddPrompt(prompt *PromptItem) error {
	return svr.repo.UpdatePromptByName(prompt)
}

// DeletePrompt deletes a prompt configuration by name.
func (svr *PromptSvr) DeletePrompt(name string) error {
	return svr.repo.DeletePromptByName(name)
}

// ListPrompts returns all prompt configurations
func (svr *PromptSvr) AllPrompts() []*PromptItem {
	return svr.repo.AllPrompts()
}

// ensureDefaultPrompt ensures the default prompt exists
func (svr *PromptSvr) ensureDefaultPrompt() {
	if svr.PromptByName(DefaultMCPPromptName) == nil {
		if err := svr.AddPrompt(svr.defaultMCPPrompt()); err != nil {
			svr.Panic("failed to create default mcp prompt: %v", err)
		}
	}

	if svr.PromptByName(DefaultDeepResearchPromptName) == nil {
		if err := svr.AddPrompt(svr.defaultDeepResearchPrompt()); err != nil {
			svr.Panic("failed to create default deep research prompt: %v", err)
		}
	}
}

func (svr *PromptSvr) defaultMCPPrompt() *PromptItem {
	prompt := &PromptItem{
		Name:        DefaultMCPPromptName,
		Content:     MCPPrompt,
		Description: "mcp prompt",
	}

	svr.Infof("Create default mcp prompt: %+v", prompt)

	return prompt
}

func (svr *PromptSvr) defaultDeepResearchPrompt() *PromptItem {
	prompt := &PromptItem{
		Name:        DefaultDeepResearchPromptName,
		Content:     DeepResearchPrompt,
		Description: "deep research prompt",
	}

	svr.Infof("Create default deep research prompt: %+v", prompt)

	return prompt
}
