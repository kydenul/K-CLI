package main

import (
	"fmt"
	"path/filepath"

	"github.com/kydenul/log"

	"github.com/kydenul/K-CLI/client"
)

const ConfigPath = "/Users/kyden/git-space/K-CLI/examples/simple_client/config"

// Global Logger
var (
	Logger *log.Log

	ClientPath = filepath.Join(ConfigPath, "client.yaml")
	ChatsPath  = filepath.Join(ConfigPath, "chats.jsonl")
	MCPSvrPath = filepath.Join(ConfigPath, "mcp_servers.jsonl")
	PromptPath = filepath.Join(ConfigPath, "prompts.jsonl")
)

func main() {
	// NOTE: Initialize Logger
	cfg := ClientPath
	fmt.Println("Loading log config from", cfg)

	opt, err := log.LoadFromFile(cfg)
	if err != nil {
		panic(err)
	}
	Logger = log.NewLog(opt)
	defer Logger.Sync()
	Logger.Info("Logger initialized")

	// NOTE: Initialize Config
	config, err := client.NewConfigFromFile(ClientPath, Logger)
	if err != nil {
		Logger.Panic("Config initialized fail")
	}
	Logger.Info("Config initialized")

	// NOTE: Initialize Chat Repository
	chatRepo, err := client.NewChatFileRepository(ChatsPath, 4, Logger)
	if err != nil {
		Logger.Panic("FileRepository initialized fail")
	}
	Logger.Info("FileRepository initialized")

	// NOTE: Initialize MCP Server Config Repository
	mcpRepo, err := client.NewMCPSvrConfigFileRepo(MCPSvrPath, Logger)
	if err != nil {
		Logger.Panic("MCPServerConfigRepo initialized fail")
	}
	Logger.Info("MCPServerConfigRepo initialized")

	// NOTE: Initialize Prompt Repository
	promptRepo, err := client.NewPromptFileRepo(PromptPath, Logger)
	if err != nil {
		Logger.Panic("PromptRepo initialized fail")
	}
	Logger.Info("PromptRepo initialized")

	mgr := client.NewManager(Logger, chatRepo, mcpRepo, promptRepo, nil, config)
	// NOTE Clean up
	defer func() {
		if mgr.MCPMgr != nil {
			mgr.MCPMgr.ClossAllSession()
		}
	}()

	resp, err := mgr.HandleUserTextInput("今天上海天气怎么样？")
	if err != nil {
		Logger.Errorf("failed to run: %v", err)
		return
	}
	Logger.Infof("First => Response: %s", resp.Content)

	resp, err = mgr.HandleUserTextInput("今天上海天气怎么样？")
	if err != nil {
		Logger.Errorf("failed to run: %v", err)
		return
	}
	Logger.Infof("Second => Response: %s", resp.Content)
}
