package recipetest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pageArgs struct {
	ID string `json:"id"`
}

type noArgs struct{}

// mcpHelper is a stdio MCP server with three tools: read, write and env (which
// prints the environment the connector gave the process).
func mcpHelper() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	text := func(s string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "read", Description: "reads a page"}, func(ctx context.Context, r *mcp.CallToolRequest, in pageArgs) (*mcp.CallToolResult, any, error) {
		return text("page " + in.ID), nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "write", Description: "mutates"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		return text("written"), nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "env", Description: "environment"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		return text(strings.Join(os.Environ(), "\n")), nil, nil
	})
	return srv
}

func connectorInput(t *testing.T, name string, tools []string, sc *Scenario) ConnectorInput {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sc.Env["CONNECTOR_TEST_HELPER"] = "1"
	return ConnectorInput{Name: "wiki", Scenario: sc, Env: map[string]string{"CONNECTOR_TEST_HELPER": "CONNECTOR_TEST_HELPER_VAR", "WIKI_TOKEN": "CONNECTOR_TEST_TOKEN_VAR"},
		Config: connector.Config{Name: name, Transport: connector.TransportStdio, Command: exe, Tools: tools,
			Env:         map[string]string{"CONNECTOR_TEST_HELPER": "CONNECTOR_TEST_HELPER_VAR", "WIKI_TOKEN": "CONNECTOR_TEST_TOKEN_VAR"},
			CallTimeout: 20 * time.Second, MaxResultBytes: 1 << 10}}
}

func calls(cs ...Call) *Scenario { return &Scenario{Env: map[string]string{}, Calls: cs} }

// A connector that does what its descriptor says passes: the offered tools are
// the allowlist, a call outside it is refused, and the report has one line per call.
func TestAConformingConnectorPasses(t *testing.T) {
	sc := calls(Call{Tool: "read", Args: map[string]any{"id": "7"}, ExpectTextContains: "page 7"}, Call{Tool: "write", ExpectError: true})
	res := RunConnector(context.Background(), connectorInput(t, "acme/wiki", []string{"read"}, sc))
	if len(res) != 3 {
		t.Fatalf("%+v", res)
	}
	for _, r := range res {
		if r.Status != Pass {
			t.Errorf("%+v", r)
		}
	}
}

func TestEveryConnectorProblemIsNamed(t *testing.T) {
	for name, tc := range map[string]struct {
		in    func(t *testing.T) ConnectorInput
		check string // the row that must fail
		want  string
	}{
		"absent tool": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read", "absent"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}}))
		}, "connector", "tool absent is in the allowlist but is not offered: the server does not offer it"},
		"name over the cap": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/"+strings.Repeat("w", 55), []string{"read"}, calls(Call{Tool: "read"}))
		}, "connector", "tool read is in the allowlist but is not offered"},
		"wrong text": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}, ExpectTextContains: "page 2"}))
		}, "call 1 read", "does not contain"},
		"an allowed tool that must fail": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}, ExpectError: true}))
		}, "call 1 read", "expected true"},
		"a secret in a result": {func(t *testing.T) ConnectorInput {
			sc := calls(Call{Tool: "env"})
			sc.Env["WIKI_TOKEN"] = "tok-secret-value"
			return connectorInput(t, "acme/wiki", []string{"env"}, sc)
		}, "connector", "the value of WIKI_TOKEN appears in a call result"},
		"does not start": {func(t *testing.T) ConnectorInput {
			in := connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read"}))
			in.Config.Command = os.Args[0] + "-missing"
			return in
		}, "connector", "cannot start"},
	} {
		t.Run(name, func(t *testing.T) {
			var failed Result
			for _, r := range RunConnector(context.Background(), tc.in(t)) {
				if r.Status == Fail && r.Check == tc.check {
					failed = r
				}
			}
			if failed.Status != Fail || !strings.Contains(failed.Detail, tc.want) {
				t.Errorf("%q: %+v", tc.check, failed)
			}
		})
	}
}

func TestAnHTTPConnectorIsSkipped(t *testing.T) {
	in := connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read"}))
	in.Config.Transport = connector.TransportHTTP
	res := RunConnector(context.Background(), in)
	if len(res) != 1 || res[0].Status != Skip || !strings.Contains(res[0].Detail, "live server") {
		t.Errorf("%+v", res)
	}
}
