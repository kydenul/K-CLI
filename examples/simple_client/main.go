package main

import (
	"fmt"
	"path/filepath"

	"github.com/kydenul/log"

	"github.com/kydenul/K-CLI/client"
)

// Global Logger
var (
	Logger *log.Log

	ClientPath = filepath.Join(".", "config", "client.yaml")
	ChatsPath  = filepath.Join(".", "config", "chats.jsonl")
	MCPSvrPath = filepath.Join(".", "config", "mcp_servers.jsonl")
	PromptPath = filepath.Join(".", "config", "prompts.jsonl")
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

	resp, err := mgr.HandleUserTextInput("今天上海天气怎么样？")
	if err != nil {
		Logger.Errorf("failed to run: %v", err)
		return
	}

	Logger.Infof("Response: %s", resp.Content)
}
