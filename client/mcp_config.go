package client

import (
	"fmt"

	"github.com/kydenul/log"
)

const (
	DefaultMCPServerConfigName    = "todo"
	DefaultMCPServerConfigType    = "stdio"
	DefaultMCPServerConfigCommand = "uvx"
)

var DefaultMCPServerConfigArgs = []string{"mcp-todo"}

// MCPSvrItem 对应 mcpServers 对象中的每一个服务器配置
type MCPSvrItem struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "stdio", "sse", "streamableHttp"
	IsActive bool   `json:"isActive"`

	Description string `json:"description,omitempty"` // Description of the MCP Server

	BaseURL string `json:"baseUrl,omitempty"` // The URL endpoint for SSE / StreamableHttp server connection

	//nolint:lll
	Command string   `json:"command,omitempty"` // The command to execute the server (e.g., 'node', 'python') - used for stdio
	Args    []string `json:"args,omitempty"`    // Command line arguments for the server - used for stdio

	//nolint:lll
	AutoConfirm []string `json:"autoConfirm,omitempty"` // List of tool names that should be auto-confirmed without user prompt
}

type MCPConfigSvr struct {
	log.Logger

	repo MCPSvrConfigRepo
}

func NewMCPSvr(repo MCPSvrConfigRepo, logger log.Logger) *MCPConfigSvr {
	svr := &MCPConfigSvr{
		Logger: logger,
		repo:   repo,
	}

	svr.ensureDefaultConfig()

	return svr
}

func (svr *MCPConfigSvr) ensureDefaultConfig() {
	if item := svr.MCPServerConfigByName("todo"); item != nil { // Not Find
		defaultConfig := svr.DefaultConfig()
		if err := svr.CreateMCPServerConfig(
			defaultConfig.Name,
			defaultConfig.Type,
			defaultConfig.IsActive,

			nil,
			nil,
			nil,
			defaultConfig.Args,
		); err != nil {
			svr.Panic("failed to create default mcp server config: %v", err)
		}
	}
}

func (svr *MCPConfigSvr) CreateMCPServerConfig(
	name, typ string,
	isActive bool,
	description, baseURL, command *string,
	args []string,
) error {
	config := &MCPSvrItem{
		Name:     name,
		Type:     typ,
		IsActive: isActive,
	}

	if description != nil {
		config.Description = *description
	}

	if baseURL != nil {
		config.BaseURL = *baseURL
	}

	if command != nil {
		config.Command = *command
	}

	if len(args) > 0 {
		config.Args = args
	}

	if err := svr.repo.UpdateMCPServerConfigByName(config); err != nil {
		svr.Errorf("failed to create mcp server config: %v", err)
		return fmt.Errorf("failed to create mcp server config: %w", err)
	}

	log.Infof("Created mcp server config: %s, MCP Server: %+v", name, config)

	return nil
}

// MCPServerConfigByName returns the specified mcp server config with name, otherwise return nil
func (svr *MCPConfigSvr) MCPServerConfigByName(name string) *MCPSvrItem {
	item, _ := svr.repo.MCPServerConfigByName(name)
	return item
}

// AllMCPServerConfig return the list of all MCP Server config
func (svr *MCPConfigSvr) AllMCPServerConfig() []*MCPSvrItem {
	return svr.repo.AllMCPServerConfigs()
}

// UpdateMCPServerConfigByName adds or updates the mcp server config with the specified config
func (svr *MCPConfigSvr) UpdateMCPServerConfigByName(item *MCPSvrItem) error {
	return svr.repo.UpdateMCPServerConfigByName(item)
}

// DeleteMCPServerConfigByName deletes the mcp server config with the specified name
func (svr MCPConfigSvr) DeleteMCPServerConfigByName(name string) error {
	return svr.repo.DeleteMCPServerConfigByName(name)
}

// DefaultConfig returns the default MCP server configuration
func (svr *MCPConfigSvr) DefaultConfig() *MCPSvrItem {
	item := &MCPSvrItem{
		Name:     DefaultMCPServerConfigName,
		IsActive: true,

		Command: DefaultMCPServerConfigCommand,
		Args:    DefaultMCPServerConfigArgs,
	}

	svr.Infof("Create default mcp server config: %+v", item)

	return item
}
