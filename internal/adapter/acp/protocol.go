package acp

import "encoding/json"

const protocolVersion = 1

type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "acp json-rpc error"
	}
	return e.Message
}

type implementationInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type initializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         implementationInfo `json:"clientInfo"`
}

type clientCapabilities struct {
	FS       *fsClientCapabilities `json:"fs,omitempty"`
	Terminal bool                  `json:"terminal,omitempty"`
}

type fsClientCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type initializeResponse struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities agentCapabilities  `json:"agentCapabilities"`
	AgentInfo         implementationInfo `json:"agentInfo"`
	AuthMethods       []authMethod       `json:"authMethods"`
}

type authMethod struct {
	ID          string          `json:"id"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Type        string          `json:"type,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

type agentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	PromptCapabilities  promptCapabilities  `json:"promptCapabilities"`
	MCPCapabilities     mcpCapabilities     `json:"mcpCapabilities"`
	SessionCapabilities sessionCapabilities `json:"sessionCapabilities"`
}

type promptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type mcpCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

type sessionCapabilities struct {
	Resume          json.RawMessage `json:"resume,omitempty"`
	Close           json.RawMessage `json:"close,omitempty"`
	List            json.RawMessage `json:"list,omitempty"`
	SetConfigOption json.RawMessage `json:"setConfigOption,omitempty"`
	SetMode         json.RawMessage `json:"setMode,omitempty"`
}

func (c sessionCapabilities) supportsResume() bool {
	return len(c.Resume) > 0 && string(c.Resume) != "null"
}

func (c sessionCapabilities) supportsClose() bool {
	return len(c.Close) > 0 && string(c.Close) != "null"
}

func (c sessionCapabilities) supportsSetConfigOption() bool {
	return len(c.SetConfigOption) > 0 && string(c.SetConfigOption) != "null"
}

func (c sessionCapabilities) supportsSetMode() bool {
	return len(c.SetMode) > 0 && string(c.SetMode) != "null"
}

type mcpServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []envVar `json:"env,omitempty"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type sessionCreateParams struct {
	SessionID  string      `json:"sessionId,omitempty"`
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers,omitempty"`
}

type sessionIDParams struct {
	SessionID string `json:"sessionId"`
}

type sessionResponse struct {
	SessionID     string         `json:"sessionId,omitempty"`
	Modes         []sessionMode  `json:"modes,omitempty"`
	CurrentMode   string         `json:"currentMode,omitempty"`
	ConfigOptions []configOption `json:"configOptions,omitempty"`
}

type sessionMode struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type configOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type"`
	CurrentValue string              `json:"currentValue"`
	Options      []configOptionValue `json:"options"`
}

type configOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type setConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type setConfigOptionResponse struct {
	ConfigOptions []configOption `json:"configOptions"`
}

type setModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type promptResponse struct {
	StopReason string `json:"stopReason"`
}

type sessionUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type updateEnvelope struct {
	SessionUpdate string `json:"sessionUpdate"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type messageChunkUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Content       textContent `json:"content"`
}

type planUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []planEntry `json:"entries"`
}

type planEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type toolCallUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
}

type availableCommandsUpdate struct {
	SessionUpdate     string             `json:"sessionUpdate"`
	AvailableCommands []availableCommand `json:"availableCommands"`
}

type availableCommand struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
}

type requestPermissionParams struct {
	SessionID  string             `json:"sessionId"`
	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      string             `json:"title,omitempty"`
	Options    []permissionOption `json:"options"`
}

type permissionOption struct {
	ID          string          `json:"id"`
	Name        string          `json:"name,omitempty"`
	Kind        string          `json:"kind,omitempty"`
	Description string          `json:"description,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

type permissionResponse struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

type fsReadTextFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Line      int    `json:"line,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type fsReadTextFileResponse struct {
	Content string `json:"content"`
}

type fsWriteTextFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type terminalCreateParams struct {
	SessionID       string   `json:"sessionId"`
	Command         string   `json:"command"`
	Args            []string `json:"args,omitempty"`
	Env             []envVar `json:"env,omitempty"`
	CWD             string   `json:"cwd,omitempty"`
	OutputByteLimit int      `json:"outputByteLimit,omitempty"`
}

type terminalCreateResponse struct {
	TerminalID string `json:"terminalId"`
}

type terminalIDParams struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type terminalOutputResponse struct {
	Output     string              `json:"output"`
	Truncated  bool                `json:"truncated"`
	ExitStatus *terminalExitStatus `json:"exitStatus,omitempty"`
}

type terminalExitStatus struct {
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}
