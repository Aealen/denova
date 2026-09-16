package conversationapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"denova/config"
	agents "denova/internal/agents"
	agentchat "denova/internal/agents/chat"
	agentconversation "denova/internal/agents/conversation"
	"denova/internal/agents/conversationconfig"
	agentexecution "denova/internal/agents/execution"
	"denova/internal/agents/external"
	agentrun "denova/internal/agents/run"
	"denova/internal/agents/session"
	agenttool "denova/internal/agents/tool"
	appagentruntime "denova/internal/app/agentruntime"
	"denova/internal/book"
	projectdomain "denova/internal/project"
	workspacechange "denova/internal/workspace/change"
	agent "github.com/alfredxw/denova/agent"
)

// Exercise the same preparation/acceptance/settlement used by Writing and
// AgentChat with the real executable. Only model responses are local fixtures;
// tools, Ask, project stores, receipts and callbacks are production paths.
func TestInstalledExternalProductExecution(t *testing.T) {
	executable := os.Getenv("DENOVA_TEST_CODEX_EXE")
	if executable == "" {
		t.Skip("set DENOVA_TEST_CODEX_EXE to test product execution with App Server")
	}
	for _, scenario := range []struct{ name, kind, contract string }{
		{"writing", config.AgentKindIDE, ""}, {"general", config.AgentKindGeneral, ""},
		{"custom-writing", config.AgentKindIDE, "writing.primary.v1"}, {"custom-general", config.AgentKindGeneral, "project.general.v1"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" {
					http.NotFound(w, r)
					return
				}
				var raw json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				mu.Lock()
				requests = append(requests, string(raw))
				ordinal := len(requests)
				mu.Unlock()
				var item map[string]any
				switch ordinal {
				case 1:
					item = map[string]any{"type": "function_call", "id": "fc_question", "call_id": "call_question", "name": "ask", "arguments": `{"questions":[{"id":"tone","prompt":"Which tone?"}]}`, "status": "completed"}
				case 2:
					item = map[string]any{"type": "function_call", "id": "fc_write", "call_id": "call_write", "name": "write", "arguments": `{"path":"draft.md","content":"The harbor was still.\n"}`, "status": "completed"}
				default:
					item = map[string]any{"type": "message", "id": "msg_final", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "The restrained draft is saved.", "annotations": []any{}}}}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(event map[string]any) {
					body, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], body)
				}
				id := fmt.Sprintf("response_%d", ordinal)
				emit(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "output": []any{}}})
				emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
				emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
				emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 50, "total_tokens": 150, "input_tokens_details": map[string]int{"cached_tokens": 20}, "output_tokens_details": map[string]int{"reasoning_tokens": 10}}}})
			}))
			defer server.Close()
			hostRoot := t.TempDir()
			for _, key := range []string{"LOCALAPPDATA", "APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
				t.Setenv(key, hostRoot)
			}
			t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
			home := filepath.Join(hostRoot, "Denova", "runtimes", "codex")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			configuration := fmt.Sprintf("model = \"gpt-5.5\"\nmodel_provider = \"fixture\"\n[model_providers.fixture]\nname = \"Local fixture\"\nbase_url = %q\nwire_api = \"responses\"\nexperimental_bearer_token = \"fixture-only\"\n[features]\nenable_request_compression = false\n", server.URL+"/v1")
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configuration), 0o600); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			workspace := filepath.Join(root, "project")
			if err := os.MkdirAll(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			preference := &config.RuntimePreferences{Selected: config.RuntimeCodex, Codex: &config.CodexRuntimeSettings{Model: "gpt-5.5"}}
			cfg := config.Config{DenovaDir: root, Workspace: workspace, ProjectID: "product-fixture", ProjectStoreDir: filepath.Join(root, "store"), AgentRuntimes: config.AgentRuntimeSettings{IDE: preference, General: preference}}
			customID := ""
			if scenario.contract != "" {
				customID = "fixture-custom"
				cfg.CustomAgents = []config.CustomAgentConfig{{ID: customID, Name: "Fixture author", Contract: scenario.contract, Runtime: preference, Instructions: "Keep the opening restrained. CUSTOM_ROLE_SENTINEL"}}
			}
			selection, err := conversationconfig.DefaultWithCustomAgent(&cfg, scenario.kind, customID)
			if err != nil {
				t.Fatal(err)
			}
			store, err := session.NewStore(filepath.Join(cfg.ProjectStoreDir, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			sess, err := store.GetOrCreateWithRuntimeConfig("session-fixture", selection)
			if err != nil {
				t.Fatal(err)
			}
			runtime := Runtime{ProjectID: cfg.ProjectID, ProjectStore: cfg.ProjectStoreDir, ProjectType: projectdomain.TypeGeneral, AgentKind: scenario.kind, Session: sess, Config: cfg, Workspace: workspace, BookService: book.NewService(workspace)}
			if scenario.kind == config.AgentKindIDE {
				runtime.ProjectType, runtime.State = projectdomain.TypeBook, book.NewState(workspace)
				if err := runtime.State.InitWorkspace(); err != nil {
					t.Fatal(err)
				}
			}
			request := agentchat.ChatRequest{CommandID: "product-command", Message: "Ask for a tone, then save an opening in draft.md."}
			runtime, request, err = Prepare(ctx, runtime, request)
			if err != nil {
				t.Fatal(err)
			}
			engines := appagentruntime.NewEngines()
			defer engines.Close()
			built, err := BuildExecution(ctx, runtime, agents.AgentHostCapabilities{}, engines, "")
			if err != nil {
				t.Fatal(err)
			}
			answered, verified, inputCommitted := false, false, false
			var usage map[string]any
			var eventError error
			emit := func(event agentrun.Event) {
				if event.Type == "token_usage" {
					usage, _ = event.Data.(map[string]any)
					return
				}
				if event.Type != "ask_pending" {
					return
				}
				pending, err := sess.PendingExternalAsks(ctx)
				if err != nil || len(pending) != 1 {
					eventError = fmt.Errorf("pending question projection: %v", err)
					cancel()
					return
				}
				_, err = engines.Operations.Interactions.Resolve(ctx, runtime.ProjectID, sess, pending[0].ID, []agentconversation.HostAskAnswer{{QuestionID: "tone", CustomInput: "Restrained"}}, nil)
				answered, eventError = err == nil, err
				if err != nil {
					cancel()
				}
			}
			options := agentrun.Options{InputCommitEffect: agentrun.InputCommitEffectFuncs{ApplyFunc: func(context.Context, agentrun.InputCommitEffectRequest) error { inputCommitted = true; return nil }}, OnMutationsVerified: func(_ context.Context, mutations []agenttool.Mutation, _ agenttool.Verification) {
				verified = len(mutations) > 0
			}}
			op, err := built.Start(ctx, request, ProjectConversation(runtime, request), options, emit)
			if err != nil {
				t.Fatal(err)
			}
			outcome := op.Wait(ctx)
			if outcome.Status != agentrun.OutcomeCompleted || eventError != nil {
				t.Fatalf("product execution: %s, %v, %v", outcome.Status, outcome.Error, eventError)
			}
			if !answered || !verified || !inputCommitted || !op.OutputCommitted() {
				t.Fatalf("missing shared effects: answer=%v verification=%v input=%v output=%v", answered, verified, inputCommitted, op.OutputCommitted())
			}
			if usage["total_tokens"] != float64(450) || usage["model_calls"] != nil {
				t.Fatalf("incorrect product usage: %+v", usage)
			}
			body, err := os.ReadFile(filepath.Join(workspace, "draft.md"))
			if err != nil || string(body) != "The harbor was still.\n" {
				t.Fatalf("domain write: %q, %v", body, err)
			}
			changes, err := workspacechange.ForWorkspaceAt(workspace, runtime.ProjectStore)
			if err != nil {
				t.Fatal(err)
			}
			group, err := changes.GetGroup(ctx, string(op.Receipt().OperationID))
			if err != nil || len(group.ChangeSets) != 1 {
				t.Fatalf("missing original domain receipt: %+v, %v", group, err)
			}
			history, err := external.ReadHistory(ctx, sess)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(history.Messages)
			if !strings.Contains(string(encoded), "Restrained") || !strings.Contains(string(encoded), "The restrained draft is saved.") {
				t.Fatal("canonical history lost question answer or output")
			}
			canonical, err := sess.ReadCanonicalMessages(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(canonical) != 6 || len(canonical[1].ToolCalls) != 1 || canonical[2].ToolCallID != canonical[1].ToolCalls[0].ID || canonical[2].ToolName != "ask" || canonical[4].ToolName != "write" {
				body, _ := json.Marshal(canonical)
				t.Fatalf("Native projection lost completed external observations: %s", body)
			}
			// Switch this exact logical Session and execute the real Native loop.
			// Only its model is a fixture; preparation and canonical synchronization
			// remain the production implementation.
			nativeRuntime := agentexecution.NewEphemeralRuntime()
			defer nativeRuntime.Close(context.Background())
			runtime.ExecutionRuntime = nativeRuntime
			current, _ := sess.RuntimeConfig()
			next := current.Config
			next.Runtime = &config.RuntimeSelection{Kind: config.RuntimeNative}
			next.ProfileID, next.ThinkingLevel = "fixture", "off"
			opts := agentrun.Options{ProjectID: cfg.ProjectID, StateRoot: cfg.ProjectStoreDir, Workspace: workspace, SessionID: sess.ID, AgentKind: scenario.kind, Mode: "agent_chat"}
			if _, err := engines.ApplyEngineSelection(ctx, nativeRuntime, sess, opts, next, current.Revision); err != nil {
				t.Fatal(err)
			}
			nativeSettings := `[[model_endpoints]]
id = "fixture"
provider = "openai-compatible"
protocol = "openai-chat-completions"
api_key = "fixture-only"
base_url = "http://127.0.0.1:1/v1"
[[model_profiles]]
id = "fixture"
endpoint_id = "fixture"
model = "fixture"
context_window_tokens = 100000
`
			if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(nativeSettings), 0600); err != nil {
				t.Fatal(err)
			}
			nativeRequest := agentchat.ChatRequest{CommandID: "native-continuation", Message: "Continue from the confirmed saved draft."}
			runtime, nativeRequest, err = Prepare(ctx, runtime, nativeRequest)
			if err != nil {
				t.Fatal(err)
			}
			nativeBuilt, err := BuildExecution(ctx, runtime, agents.AgentHostCapabilities{}, engines, "")
			if err != nil {
				t.Fatal(err)
			}
			model := &continuationFixtureModel{}
			nativeBuilt.native.Definition.Model = model
			nativeOp, err := nativeBuilt.Start(ctx, nativeRequest, ProjectConversation(runtime, nativeRequest), opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			if outcome := nativeOp.Wait(ctx); outcome.Status != agentrun.OutcomeCompleted {
				t.Fatalf("Native continuation: %+v", outcome)
			}
			modelInput, _ := json.Marshal(model.input)
			if !strings.Contains(string(modelInput), "Restrained") || !strings.Contains(string(modelInput), "draft.md") || !strings.Contains(string(modelInput), "The restrained draft is saved.") {
				t.Fatalf("Native model lost confirmed external history: %s", modelInput)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 3 || !strings.Contains(requests[1], "Restrained") {
				t.Fatalf("engine did not continue with answer, requests=%d", len(requests))
			}
			if customID != "" && !strings.Contains(requests[0], "CUSTOM_ROLE_SENTINEL") {
				t.Fatal("custom Agent instructions were lost")
			}
		})
	}
}

type continuationFixtureModel struct{ input []*agent.Message }

func (m *continuationFixtureModel) Generate(_ context.Context, input []*agent.Message, _ ...agent.ModelOption) (*agent.Message, error) {
	m.input = input
	return &agent.Message{Role: agent.Assistant, Content: "Native continuation preserved the draft.", ResponseMeta: &agent.ResponseMeta{FinishReason: "stop"}}, nil
}
func (m *continuationFixtureModel) Stream(ctx context.Context, input []*agent.Message, options ...agent.ModelOption) (*agent.StreamReader[*agent.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	return agent.StreamReaderFromArray([]*agent.Message{message}), err
}
