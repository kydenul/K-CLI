package client

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/kydenul/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cast"
)

const (
	MCPClientName = "kMCPClient"
	MCPClientVer  = "v1.0.0"

	ServerTypeStdio          = "stdio"
	ServerTypeSSE            = "sse"
	ServerTypeStreamableHTTP = "streamableHttp"
)

type MCPSvrManager struct {
	log.Logger

	repo MCPSvrConfigRepo // MCP Server Config

	client *mcp.Client // MCP client => 一个 client 创建多个 session

	mu       sync.RWMutex
	sessions map[string]*mcp.ClientSession // Servername => Session 每个 session 连接到不同的 MCP Server
	tools    map[string]string             // 工具名到服务器名的映射
}

// NewMCPSvrManager returns a new instance of MCPSvrManager
func NewMCPSvrManager(repo MCPSvrConfigRepo, logger log.Logger) *MCPSvrManager {
	return &MCPSvrManager{
		Logger: logger,

		repo: repo,

		client: mcp.NewClient(&mcp.Implementation{
			Name:    MCPClientName,
			Version: MCPClientVer,
		}, nil),
		sessions: make(map[string]*mcp.ClientSession),
		tools:    make(map[string]string),
	}
}

// initMCPServer initializes the MCP server, which creates a new session for each server and stores to Session and Tools
func (ss *MCPSvrManager) initMCPServer(ctx context.Context) {
	svrs := ss.repo.AllMCPServerConfigs()
	if len(svrs) <= 0 {
		ss.Info("No MCP servers found in config")
		return
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// NOTE: 1. Clear exsist session and tools
	for k := range ss.sessions {
		_ = ss.sessions[k].Close()

		delete(ss.sessions, k)
	}
	for k := range ss.tools {
		delete(ss.tools, k)
	}

	// NOTE: 2. Create new session
	var transport mcp.Transport
	for _, item := range svrs {
		if !item.IsActive {
			ss.Infof("MCP server %s is not active, skipping", item.Name)
			continue
		}

		switch item.Type {
		case ServerTypeStdio: // Stdio transport
			ss.Info("Using stdio transport")
			cmd := exec.Command(item.Command, item.Args...) //nolint:gosec
			transport = &mcp.CommandTransport{Command: cmd}

		case ServerTypeSSE: // HTTP transport
			ss.Info("Using SSE transport")
			httpClient := &http.Client{} // 简化版，可扩展以添加头部

			transport = &mcp.SSEClientTransport{
				Endpoint:   item.BaseURL,
				HTTPClient: httpClient,
			}

		case ServerTypeStreamableHTTP: // HTTP transport
			ss.Info("Using Streamable HTTP transport")
			httpClient := &http.Client{} // 简化版，可扩展以添加头部

			transport = &mcp.StreamableClientTransport{
				Endpoint:   item.BaseURL,
				HTTPClient: httpClient,
				MaxRetries: 1,
			}

		default:
			ss.Warnf("Skipping server '%s': no command or httpUrl configured", item.Name)
			continue
		}

		// NOTE: 3.1 Create MCP Server Session
		ss.Infof("Connecting to server '%s'...", item.Name)
		session, err := ss.client.Connect(ctx, transport, nil)
		if err != nil {
			ss.Infof("Failed to connect to server '%s': %v", item.Name, err)
			continue
		}
		ss.sessions[item.Name] = session
		ss.Infof("Successfully connected to server '%s'", item.Name)

		// NOTE: 3.2 Register tools
		tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			ss.Errorf("Failed to list tools for server '%s': %v", item.Name, err)
			continue
		}

		for _, tool := range tools.Tools {
			ss.tools[tool.Name] = item.Name
			ss.Infof("Registered tool '%s' for server '%s'", tool.Name, item.Name)
		}
	}
}

// ClossAllSession closes all sessions and clears the session
func (ss *MCPSvrManager) ClossAllSession() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	ss.Infof("Closing all sessions...")
	for name, session := range ss.sessions {
		err := session.Close()
		if err != nil {
			ss.Errorf("Failed to close session for server '%s': %v", name, err)
			continue
		}

		ss.Infof("  --> Close session for server '%s'", name)
		delete(ss.sessions, name)
	}
	ss.Infof("All sessions are closed.")
}

// CallTool calls a tool on a specific server according to its tool name
func (ss *MCPSvrManager) CallTool(
	ctx context.Context, toolName string, args map[string]any,
) (*mcp.CallToolResult, error) {
	// NOTE: Routing tool
	ss.mu.RLock()
	serverName, ok := ss.tools[toolName]
	if !ok {
		ss.Infof("Tool '%s' not found in any connected server", toolName)
		ss.mu.RUnlock()
		return nil, fmt.Errorf("tool '%s' not found on any connected server", toolName)
	}

	// NOTE: Routing MCP Server
	session := ss.sessions[serverName]
	ss.Infof("Routing tool '%s' to server '%s'", toolName, serverName)
	ss.mu.RUnlock()

	return session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
}

func (ss *MCPSvrManager) ExtractMCPToolUse(content string) *MCPToolUse {
	match := regexp.MustCompile("(?s)<use_mcp_tool>(.*?)</use_mcp_tool>").
		FindStringSubmatch(content)
	if len(match) < 2 {
		ss.Errorf("No <use_mcp_tool> tag found in content")
		return nil
	}
	toolContent := match[1]

	serverMatch := regexp.MustCompile("(?s)<server_name>(.*?)</server_name>").
		FindStringSubmatch(toolContent)
	if len(serverMatch) < 2 {
		ss.Errorf("No <server_name> tag found in content")
		return nil
	}
	serverName := strings.TrimSpace(serverMatch[1])

	toolMatch := regexp.MustCompile("(?s)<tool_name>(.*?)</tool_name>").
		FindStringSubmatch(toolContent)
	if len(toolMatch) < 2 {
		ss.Errorf("No <tool_name> tag found in content")
		return nil
	}
	toolName := strings.TrimSpace(toolMatch[1])

	argsMatch := regexp.MustCompile(`(?s)<arguments>\s*(\{.*?\})\s*</arguments>`).
		FindStringSubmatch(toolContent)
	if len(argsMatch) < 2 {
		ss.Errorf("No <arguments> tag found in content")
		return nil
	}
	argsStr := argsMatch[1]

	// Parse arguments JSON
	var arguments map[string]any
	err := sonic.UnmarshalString(argsStr, &arguments)
	if err != nil {
		ss.Errorf("Failed to parse arguments JSON: %v", err)
		return nil
	}

	temp := &MCPToolUse{
		ServerName: serverName,
		ToolsName:  toolName,
		Arguments:  arguments,
	}

	ss.Infof("Extracted MCP Tool Use: %+v", temp)

	return temp
}

// MCPServerList returns the list of connected MCP servers
func (ss *MCPSvrManager) MCPServerList() []string {
	vSvr := make([]string, 0, len(ss.sessions))

	ss.mu.RLock()
	for name := range ss.sessions {
		vSvr = append(vSvr, name)
	}
	ss.mu.RUnlock()

	ss.Infof("MCP Server List: %v", vSvr)

	return vSvr
}

// ToolsByServerName returns the list of tools for a specific server
func (ss *MCPSvrManager) ToolsByServerName(
	ctx context.Context,
	serverName string,
) ([]*mcp.Tool, error) {
	vTool := make([]*mcp.Tool, 0, len(ss.sessions))

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	session, ok := ss.sessions[serverName]
	if !ok {
		ss.Errorf("Server '%s' not found among connected sessions", serverName)
		return nil, fmt.Errorf("server '%s' not found among connected sessions", serverName)
	}

	ss.Infof("MCP Server: '%s' SessionID: '%s'", serverName, session.ID())
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		ss.Errorf("Failed to list tools for server '%s': %v", serverName, err)
		return nil, err
	}

	vTool = append(vTool, tools.Tools...)

	ss.Infof("Found %d tools for server '%s'", len(tools.Tools), serverName)

	return vTool, nil
}

// ResourceTemplatesByServerName returns the list of resource templates for a specific server
func (ss *MCPSvrManager) ResourceTemplatesByServerName(
	ctx context.Context, serverName string,
) ([]*mcp.ResourceTemplate, error) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	session, ok := ss.sessions[serverName]
	if !ok {
		ss.Infof("Server '%s' not found among connected sessions", serverName)
		return nil, fmt.Errorf("server '%s' not found among connected sessions", serverName)
	}

	ss.Infof("MCP Server: '%s' SessionID: '%s'", serverName, session.ID())
	templates, err := session.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{})
	if err != nil {
		ss.Errorf("Failed to list resource templates for server '%s': %v", serverName, err)
		return nil, err
	}

	vResourceTemplate := make([]*mcp.ResourceTemplate, 0, len(ss.sessions))
	vResourceTemplate = append(vResourceTemplate, templates.ResourceTemplates...)

	ss.Infof("Found %d resource templates for server '%s'",
		len(templates.ResourceTemplates), serverName)

	return vResourceTemplate, nil
}

// ResourcesByServerName returns the list of resources for a specific server
func (ss *MCPSvrManager) ResourcesByServerName(
	ctx context.Context, serverName string,
) ([]*mcp.Resource, error) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	session, ok := ss.sessions[serverName]
	if !ok {
		ss.Infof("Server '%s' not found among connected sessions", serverName)
		return nil, fmt.Errorf("server '%s' not found among connected sessions", serverName)
	}

	ss.Infof("MCP Server: '%s' SessionID: '%s'", serverName, session.ID())
	resources, err := session.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		ss.Errorf("Failed to list resources for server '%s': %v", serverName, err)
		return nil, err
	}

	vResource := make([]*mcp.Resource, 0, len(ss.sessions))
	vResource = append(vResource, resources.Resources...)

	ss.Infof("Found %d resources for server '%s'", len(resources.Resources), serverName)

	return vResource, nil
}

// FormatToolsSection formats the tools section
func (ss *MCPSvrManager) FormatToolsSection(ctx context.Context, serverName string) string {
	if serverName == "" {
		ss.Warn("Server name is empty")
		return ""
	}

	tools, err := ss.ToolsByServerName(ctx, serverName)
	if err != nil {
		ss.Warnf("Failed to get tools for server '%s': %v", serverName, err)
		return ""
	}

	formattedTools := make([]string, 0, len(tools))
	for _, tool := range tools {
		// Convert input schema to pretty JSON string
		schemaJSON, err := sonic.MarshalIndent(tool.InputSchema, "", "  ")
		if err != nil {
			ss.Errorf("Failed to marshal InputSchema for tool '%s': %v", tool.Name, err)
			schemaJSON = []byte("{}")
		}

		formattedTools = append(formattedTools, fmt.Sprintf(
			"- %s: %s%s", tool.Name, tool.Description,
			"\n    Input Schema:\n    "+
				strings.Join(strings.Split(cast.ToString(schemaJSON), "\n"), "\n    ")))
	}

	if len(formattedTools) == 0 {
		ss.Warnf("No tools found for server '%s'", serverName)
		return ""
	}

	tempStr := "\n\n### Available Tools\n" + strings.Join(formattedTools, "\n\n")
	ss.Infoln(tempStr)
	return tempStr
}

// FormatResourceTemplatesSection formats the resource templates section
func (ss *MCPSvrManager) FormatResourceTemplatesSection(
	ctx context.Context,
	serverName string,
) string {
	if serverName == "" {
		ss.Warn("Server name is empty")
		return ""
	}

	templates, err := ss.ResourceTemplatesByServerName(ctx, serverName)
	if err != nil {
		ss.Warnf("Failed to get resource templates for server '%s': %v", serverName, err)
		return ""
	}

	if len(templates) == 0 {
		ss.Warnf("No resource templates found for server '%s'", serverName)
		return ""
	}

	formattedTemplates := make([]string, 0, len(templates))
	for _, template := range templates {
		formattedTemplates = append(formattedTemplates, fmt.Sprintf(
			"- %s (%s): %s",
			template.URITemplate, template.Name, template.Description))
	}

	if len(formattedTemplates) == 0 {
		ss.Warnf("No resource templates found for server '%s'", serverName)
		return ""
	}

	tempStr := "\n\n### Resource Templates\n" + strings.Join(formattedTemplates, "\n")
	ss.Infof(tempStr)
	return tempStr
}

// FormatResourcesSection formats the resources section
func (ss *MCPSvrManager) FormatResourcesSection(ctx context.Context, serverName string) string {
	if serverName == "" {
		ss.Warn("Server name is empty")
		return ""
	}

	resources, err := ss.ResourcesByServerName(ctx, serverName)
	if err != nil {
		ss.Warnf("Failed to get resources for server '%s': %v", serverName, err)
		return ""
	}

	if len(resources) == 0 {
		ss.Warnf("No resources found for server '%s'", serverName)
		return ""
	}

	formattedResources := make([]string, 0, len(resources))
	for _, resource := range resources {
		formattedResources = append(formattedResources, fmt.Sprintf(
			"- %s (%s): %s",
			resource.URI, resource.Name, resource.Description))
	}

	if len(formattedResources) == 0 {
		ss.Warnf("No resources found for server '%s'", serverName)
		return ""
	}

	tempStr := "\n\n### Resources\n" + strings.Join(formattedResources, "\n")
	ss.Infof(tempStr)
	return tempStr
}

// FormatServerInfo formats the server info
func (ss *MCPSvrManager) FormatServerInfo(ctx context.Context) string {
	svrs := ss.MCPServerList()
	if len(svrs) == 0 {
		ss.Warn("No connected MCP servers")
		return ""
	}

	serverSections := make([]string, 0, len(svrs))
	for _, svrName := range svrs {
		ss.Infof("Formatting info for server: %s", svrName)

		serverSections = append(serverSections, fmt.Sprintf("## %s%s%s%s",
			svrName,
			ss.FormatToolsSection(ctx, svrName),
			ss.FormatResourceTemplatesSection(ctx, svrName),
			ss.FormatResourcesSection(ctx, svrName),
		))
	}

	return strings.Join(serverSections, "\n\n")
}

// Prompt generate the complete system prompt including MCP server information
func (ss *MCPSvrManager) Prompt(ctx context.Context, promptSvr *PromptSvr) string {
	svrInfo := ss.FormatServerInfo(ctx)
	if svrInfo != "" {
		mcpPrompt := promptSvr.PromptByName(DefaultMCPPromptName)
		if mcpPrompt != nil {
			return fmt.Sprintf("%s\n\n%s", mcpPrompt, svrInfo)
		}
		return svrInfo
	}

	return ""
}
