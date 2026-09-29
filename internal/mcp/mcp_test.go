package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestServe(t *testing.T) {
	s := &Server{Name: "t", Version: "1", Tools: []Tool{{
		Name: "echo", Description: "echoes", Schema: Schema(map[string]any{"text": Str("text")}, "text"),
		Run: func(_ context.Context, args map[string]any) (any, error) {
			if String(args, "text") == "fail" {
				return nil, errors.New("boom")
			}
			return map[string]any{"text": String(args, "text"), "n": Number(args, "n"), "b": Flag(args, "b")}, nil
		},
	}}}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi","n":2,"b":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"text":"fail"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"missing"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"nothing/here"}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("want 7 responses (the notification gets none), got %d:\n%s", len(lines), out.String())
	}
	var resp []map[string]any
	for _, l := range lines {
		var r map[string]any
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("bad JSON %q: %v", l, err)
		}
		resp = append(resp, r)
	}
	if info := resp[0]["result"].(map[string]any)["serverInfo"].(map[string]any); info["name"] != "t" {
		t.Errorf("initialize: %v", resp[0])
	}
	tools := resp[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Errorf("tools/list: %v", resp[1])
	}
	content := resp[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(content, `"text": "hi"`) || !strings.Contains(content, `"n": 2`) || !strings.Contains(content, `"b": true`) {
		t.Errorf("tools/call result: %s", content)
	}
	if isErr, _ := resp[3]["result"].(map[string]any)["isError"].(bool); !isErr {
		t.Errorf("tool error should come back as isError content: %v", resp[3])
	}
	if resp[4]["error"] == nil || resp[5]["error"] == nil || resp[6]["error"] == nil {
		t.Errorf("unknown tool, unknown method and parse error must all be JSON-RPC errors: %v %v %v", resp[4], resp[5], resp[6])
	}
}
