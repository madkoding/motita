package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/mcp"
)

// fakeMCP is a connected server: it records the calls and, when asked, puts a question to the
// person in the middle of one, like a real server does.
type fakeMCP struct {
	tools    []mcp.Tool
	toolsErr error
	result   mcp.CallResult
	err      error
	calls    []string
	args     []string
	closed   int
	ask      *mcp.ElicitRequest
	asked    mcp.ElicitResult
	handler  mcp.Handler
}

func (f *fakeMCP) Tools(context.Context) ([]mcp.Tool, error) { return f.tools, f.toolsErr }
func (f *fakeMCP) Close() error {
	f.closed++
	return nil
}
func (f *fakeMCP) Call(ctx context.Context, name string, args json.RawMessage) (mcp.CallResult, error) {
	f.calls = append(f.calls, name)
	f.args = append(f.args, string(args))
	if f.ask != nil {
		f.asked = f.handler.Elicit(ctx, *f.ask)
	}
	return f.result, f.err
}

// mcpAgent is an agent with two servers configured and a fake in place of each.
func mcpAgent(t *testing.T, answer bool) (*fixture, map[string]*fakeMCP, *[]ApprovalRequest) {
	t.Helper()
	e := fileAgent(t)
	fakes := map[string]*fakeMCP{"shop": {}, "db": {}}
	old := startMCP
	t.Cleanup(func() { startMCP = old })
	startMCP = func(_ context.Context, cfg mcp.Config, h mcp.Handler) (mcpClient, error) {
		if cfg.Command == "broken" {
			return nil, errors.New("cannot start")
		}
		f := fakes[cfg.Name]
		f.handler = h
		return f, nil
	}
	e.agent.cfg.Agent.MCP = []config.MCPServer{
		{Name: "shop", Command: "shop-mcp"}, {Name: "db", Command: "db-mcp", Dir: "/srv"},
	}
	asked := &[]ApprovalRequest{}
	e.agent.SetApprover(func(_ context.Context, r ApprovalRequest) (bool, error) {
		*asked = append(*asked, r)
		return answer, nil
	})
	return e, fakes, asked
}

func TestMCPToolsListsEveryServer(t *testing.T) {
	e, fakes, asked := mcpAgent(t, true)
	schema := json.RawMessage(`{"type":"object"}`)
	fakes["shop"].tools = []mcp.Tool{{Name: "buy", Description: "buys\n  things", InputSchema: schema}}
	fakes["db"].toolsErr = errors.New("down")
	out, err := box(e, "mcp_tools", "")
	if err != nil || !strings.Contains(out, "data from a third party") || !strings.Contains(out, "## shop (1 tools)") {
		t.Fatalf("%v %q", err, out)
	}
	if !strings.Contains(out, "- buy: buys things") || !strings.Contains(out, "## db\n[failed: down]") {
		t.Fatalf("%v %q", err, out)
	}
	if len(*asked) != 0 {
		t.Fatal("listing tools asks nothing")
	}
	if out, _ := box(e, "mcp_tools", "shop"); !strings.Contains(out, "buy") || strings.Contains(out, "## db") {
		t.Fatalf("one server: %q", out)
	}
	if out, _ := box(e, "mcp_tools", "nope"); !strings.Contains(out, "no MCP server named nope. Connected: db, shop") {
		t.Fatalf("unknown: %q", out)
	}
}

func TestMCPToolsServerThatDoesNotStart(t *testing.T) {
	e, _, _ := mcpAgent(t, true)
	e.agent.cfg.Agent.MCP = []config.MCPServer{{Name: "x", Command: "broken"}}
	if out, _ := box(e, "mcp_tools", ""); !strings.Contains(out, "[failed: cannot start]") {
		t.Fatalf("%q", out)
	}
}

func TestMCPCallAsksThenCalls(t *testing.T) {
	e, fakes, asked := mcpAgent(t, true)
	fakes["shop"].result = mcp.CallResult{Content: []mcp.Content{{Type: "text", Text: "bought"}}}
	out, err := box(e, "mcp_call", "shop buy\n{\"item\": 1}")
	if err != nil || !strings.Contains(out, "bought") || !strings.Contains(out, "not instructions") {
		t.Fatalf("%v %q", err, out)
	}
	if len(*asked) != 1 || (*asked)[0].Rule != MCPRule || (*asked)[0].Command != `mcp_call shop buy {"item": 1}` ||
		!strings.Contains((*asked)[0].Reason, "shop-mcp") {
		t.Fatalf("asked %+v", *asked)
	}
	if fakes["shop"].calls[0] != "buy" || fakes["shop"].args[0] != `{"item": 1}` {
		t.Fatalf("call = %v %v", fakes["shop"].calls, fakes["shop"].args)
	}
	// The second call reuses the connection.
	fakes["shop"].result = mcp.CallResult{IsError: true, Content: []mcp.Content{{Type: "text", Text: "no stock"}}}
	out, _ = box(e, "mcp_call", "shop buy")
	if !strings.Contains(out, "[the tool reported an error]\n") || len(fakes["shop"].calls) != 2 {
		t.Fatalf("%q", out)
	}
	e.agent.closeMCP()
	if fakes["shop"].closed != 1 {
		t.Fatal("the run's servers are closed")
	}
}

func TestMCPCallRefusals(t *testing.T) {
	e, fakes, _ := mcpAgent(t, true)
	for cmd, want := range map[string]string{
		"shop":               "<server> <tool>",
		"shop buy extra":     "<server> <tool>",
		"nope buy":           "no MCP server named nope",
		"shop buy\nnot json": "must be a JSON object",
		"shop buy\nnull":     "must be a JSON object",
		"shop buy\n[1]":      "must be a JSON object",
	} {
		if out, err := box(e, "mcp_call", cmd); err != nil || !strings.Contains(out, want) {
			t.Errorf("%q: %v %q", cmd, err, out)
		}
	}
	if len(fakes["shop"].calls) != 0 {
		t.Fatal("nothing ran")
	}
}

func TestMCPCallNotApproved(t *testing.T) {
	e, fakes, _ := mcpAgent(t, false)
	out, err := box(e, "mcp_call", "shop buy")
	if err == nil || !strings.Contains(out, "not approved") || len(fakes["shop"].calls) != 0 {
		t.Fatalf("%v %q", err, out)
	}
	e.agent.SetApprover(nil)
	if _, err := box(e, "mcp_call", "shop buy"); err == nil {
		t.Fatal("nobody to ask is a refusal")
	}
}

func TestMCPCallFailures(t *testing.T) {
	e, fakes, _ := mcpAgent(t, true)
	fakes["shop"].err = errors.New("boom")
	if out, err := box(e, "mcp_call", "shop buy"); err != nil || !strings.Contains(out, "[failed: boom]") {
		t.Fatalf("%v %q", err, out)
	}
	e.agent.cfg.Agent.MCP = []config.MCPServer{{Name: "x", Command: "broken"}}
	if out, _ := box(e, "mcp_call", "x t"); !strings.Contains(out, "[failed: cannot start]") {
		t.Fatalf("%q", out)
	}
}

func TestMCPRefusedWhereItDoesNotBelong(t *testing.T) {
	cases := map[string]func(a *Agent){
		"none":     func(a *Agent) { a.cfg.Agent.MCP = nil },
		"child":    func(a *Agent) { a.isChild = true },
		"readonly": func(a *Agent) { a.cfg.Agent.ReadOnly = true },
	}
	for name, set := range cases {
		e, fakes, _ := mcpAgent(t, true)
		set(e.agent)
		for _, kind := range []string{"mcp_tools", "mcp_call"} {
			if out, err := box(e, kind, "shop buy"); err == nil || !strings.Contains(out, "[refused:") {
				t.Errorf("%s/%s: %v %q", name, kind, err, out)
			}
		}
		if len(fakes["shop"].calls) != 0 {
			t.Errorf("%s: something ran", name)
		}
	}
}

func TestMCPElicitationConfirm(t *testing.T) {
	e, fakes, asked := mcpAgent(t, true)
	fakes["shop"].ask = &mcp.ElicitRequest{Server: "shop", Message: "Create a paid project?\nIt costs $10",
		Schema: json.RawMessage(`{"properties":{"confirm":{"type":"boolean","default":true}},"required":["confirm"]}`)}
	if _, err := box(e, "mcp_call", "shop create"); err != nil {
		t.Fatal(err)
	}
	q := (*asked)[1]
	if q.Rule != MCPElicitRule || q.Command != `answer shop: {"confirm":true}` ||
		!strings.Contains(q.Reason, "Create a paid project? It costs $10") {
		t.Fatalf("question = %+v", q)
	}
	if got := fakes["shop"].asked; got.Action != mcp.Accept || got.Content["confirm"] != true {
		t.Fatalf("answer = %+v", got)
	}
}

func TestMCPElicitationAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		answer bool
		want   string
	}{"declined": {false, mcp.Decline}, "accepted": {true, mcp.Accept}} {
		e, fakes, _ := mcpAgent(t, tc.answer)
		fakes["db"].ask = &mcp.ElicitRequest{Mode: "url", Server: "db", Message: "pay", URL: "https://pay.example/x?a=1"}
		_, _ = box(e, "mcp_call", "db drop")
		if tc.answer {
			// the call was approved too: both questions were asked
			if fakes["db"].asked.Action != tc.want {
				t.Errorf("%s: %+v", name, fakes["db"].asked)
			}
		}
	}
}

func TestMCPElicitationURLIsShownWithItsAddress(t *testing.T) {
	e, _, asked := mcpAgent(t, true)
	req := mcp.ElicitRequest{Mode: "url", Server: "db", Message: "pay", URL: "https://pay.example/x?a=1"}
	res := e.agent.elicit(context.Background(), req)
	if res.Action != mcp.Accept || (*asked)[0].Command != "open https://pay.example/x?a=1" ||
		!strings.Contains((*asked)[0].Reason, "asks you to open this page: pay") {
		t.Fatalf("%+v %+v", res, *asked)
	}
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://", "%zz", ""} {
		if res := e.agent.elicit(context.Background(), mcp.ElicitRequest{Mode: "url", URL: bad}); res.Action != mcp.Decline {
			t.Errorf("%q: %+v", bad, res)
		}
	}
	if len(*asked) != 1 {
		t.Fatal("a refused address is not even offered")
	}
}

func TestMCPElicitationForTypedValuesIsDeclinedAndReported(t *testing.T) {
	e, fakes, asked := mcpAgent(t, true)
	fakes["shop"].ask = &mcp.ElicitRequest{Server: "shop", Message: "name?",
		Schema: json.RawMessage(`{"properties":{"repo":{"type":"string"}},"required":["repo"]}`)}
	out, _ := box(e, "mcp_call", "shop delete")
	if fakes["shop"].asked.Action != mcp.Decline || len(*asked) != 1 ||
		!strings.Contains(out, "asked the person for repo and it was declined") ||
		!strings.Contains(out, "Ask the person in the chat") {
		t.Fatalf("%+v %q", fakes["shop"].asked, out)
	}
	if e.agent.takeUnanswered() != "" {
		t.Fatal("the note is given once")
	}
}

func TestMCPElicitationApproverFailureCancels(t *testing.T) {
	e, _, _ := mcpAgent(t, true)
	e.agent.SetApprover(func(context.Context, ApprovalRequest) (bool, error) { return false, errors.New("gone") })
	if res := e.agent.elicit(context.Background(), mcp.ElicitRequest{Message: "q"}); res.Action != mcp.Cancel {
		t.Fatalf("%+v", res)
	}
}

func TestMCPDocs(t *testing.T) {
	e, _, _ := mcpAgent(t, true)
	if d := e.agent.mcpDocs(); !strings.Contains(d, "Connected: db, shop.") || !strings.Contains(d, "mcp_call") {
		t.Fatalf("%q", d)
	}
	e.agent.isChild = true
	if e.agent.mcpDocs() != "" {
		t.Fatal("a background agent is not taught MCP")
	}
	e.agent.isChild = false
	e.agent.cfg.Agent.MCP = nil
	if e.agent.mcpDocs() != "" {
		t.Fatal("no servers, no section")
	}
	if got := oneLine(strings.Repeat("a ", 10), 5); got != "a a a..." {
		t.Fatalf("oneLine = %q", got)
	}
}

func TestRealStartMCPFails(t *testing.T) {
	_, err := startMCP(context.Background(), mcp.Config{Name: "x", Command: "/nonexistent/mcp"}, mcp.Handler{})
	if err == nil {
		t.Fatal("a missing program is an error")
	}
}

func TestMCPElicitationDeclinedByThePerson(t *testing.T) {
	e, _, _ := mcpAgent(t, false)
	res := e.agent.elicit(context.Background(), mcp.ElicitRequest{Message: "delete it?"})
	if res.Action != mcp.Decline || res.Content != nil {
		t.Fatalf("%+v", res)
	}
}
