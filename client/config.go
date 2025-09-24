package client

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kydenul/log"
	"github.com/spf13/viper"
)

var (
	// DefaultCfgPath is the default configuration file path
	DefaultCfgPath = filepath.Join(".", "config", "client.yaml")

	// DefaultMCPServerConfig 默认的 MCP 服务器配置
	DefaultMCPServerConfig = []string{"todo"}
)

const (
	YamlKeyBot             = "K-CLI"
	DefaultProvider        = "OpenAI"
	DefaultBaseURL         = "https://openrouter.ai/api"
	DefaultCustomAPIPath   = "/v1/chat/completions"
	DefaultAPIKey          = ""
	DefaultModel           = "deepseek/deepseek-chat-v3.1:free"
	DefaultStorageType     = "file"
	DefaultMCPSvrPath      = "~/.config/k-cli/mcp_servers.jsonl"
	DefaultPromptPath      = "~/.config/k-cli/prompts.jsonl"
	DefaultMaxTurns        = 10
	DefaultMaxTokens       = 32768
	DefaultReasoningEffort = "medium"
)

type Config struct {
	logger log.Logger
	viper  *viper.Viper

	cfgPath string // 配置文件路径

	// Model Provider
	Provider      string `mapstructure:"provider"`
	BaseURL       string `mapstructure:"base_url"`
	CustomAPIPath string `mapstructure:"custom_api_path"`
	Model         string `mapstructure:"model"`
	APIKey        string `mapstructure:"api_key"`

	StorageType string `mapstructure:"storage_type,omitempty"`
	// MCP
	MCPSvrPath string `mapstructure:"mcp_server_path"` // MCP Server 配置文件路径
	// Prompt
	PromptPath string `mapstructure:"prompt_path"` // Prompt 配置文件路径

	MaxTurns        uint   `mapstructure:"max_turns"`        // 最多调用 MCP Server 的次数
	MaxTokens       uint64 `mapstructure:"max_tokens"`       // 最大 token 数
	ReasoningEffort string `mapstructure:"reasoning_effort"` // 推理努力度 => high | medium | low | minimal
	Stream          bool   `mapstructure:"stream"`           // 是否使用流式输出
}

// NewDefaultConfig returns a new Config with default values
func NewDefaultConfig(logger log.Logger) (*Config, error) {
	mcpSvrPath, err := ExpandUser(DefaultMCPSvrPath)
	if err != nil {
		log.Panic("expand user error: " + err.Error())
	}

	if err := EnsureFileExistsSync(mcpSvrPath); err != nil {
		return nil, fmt.Errorf("failed to ensure MCP server config file exists: %w", err)
	}

	promptPath, err := ExpandUser(DefaultPromptPath)
	if err != nil {
		log.Panic("expand user error: " + err.Error())
	}
	if err := EnsureFileExistsSync(promptPath); err != nil {
		return nil, fmt.Errorf("failed to ensure prompt config file exists: %w", err)
	}

	return &Config{
		logger: logger,
		viper:  viper.New(),

		cfgPath: DefaultCfgPath,

		Provider:      DefaultProvider,
		BaseURL:       DefaultBaseURL,
		CustomAPIPath: DefaultCustomAPIPath,
		Model:         DefaultModel,
		APIKey:        DefaultAPIKey,

		StorageType: DefaultStorageType,
		MCPSvrPath:  DefaultMCPSvrPath,
		PromptPath:  DefaultPromptPath,

		MaxTurns:        DefaultMaxTurns,
		MaxTokens:       DefaultMaxTokens,
		ReasoningEffort: DefaultReasoningEffort,
	}, nil
}

func NewConfigFromFile(configPath string, logger log.Logger) (*Config, error) {
	// Start with default options
	opts, err := NewDefaultConfig(logger)
	if err != nil || opts == nil {
		return nil, errors.New("failed to create default options")
	}

	if EnsureFileExistsSync(configPath) != nil {
		return nil, fmt.Errorf(
			"configuration file %s does not exist or is not accessible. ",
			configPath,
		)
	}

	// Set the config file path
	opts.cfgPath = configPath
	opts.viper.SetConfigFile(opts.cfgPath)

	// Read the configuration file
	if err := opts.viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf(
			"failed to read configuration file %s: %w. Please ensure the file exists and is accessible",
			configPath,
			err,
		)
	}

	// Unmarshal the configuration into Options struct
	if err := opts.viper.UnmarshalKey(YamlKeyBot, opts); err != nil {
		return nil, fmt.Errorf(
			"failed to parse configuration from %s: %w. "+
				"Please check your configuration syntax and ensure all field names match the expected configuration options",
			configPath,
			err,
		)
	}

	// Validate the loaded configuration
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf(
			"invalid configuration values in %s: %w. "+
				"Please review your configuration values and ensure they meet the required constraints",
			configPath,
			err,
		)
	}

	return opts, nil
}

// Validate validates the loaded configuration
// TODO: To implement
func (svr *Config) Validate() error {
	if svr.StorageType == "" {
		svr.StorageType = DefaultStorageType
	}

	return nil
}
