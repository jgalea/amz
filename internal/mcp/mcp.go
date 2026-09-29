// Package mcp is a small Model Context Protocol server over stdio:
// JSON-RPC 2.0, one message per line, with initialize, tools/list and
// tools/call. It is enough for an agent to call amz's read
// commands as tools.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const protocolVersion = "2024-11-05"

// Tool is one callable: its schema and the function behind it. The
// function returns something JSON-encodable, which is sent back as
// text content.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Run         func(ctx context.Context, args map[string]any) (any, error)
}

type Server struct {
	Name    string
	Version string
	Tools   []Tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests from in and writes responses to out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(response{JSONRPC: "2.0", Error: &rpcError{-32700, "parse error: " + err.Error()}})
			continue
		}
		resp, reply := s.handle(ctx, req)
		if reply {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req request) (response, bool) {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
	case "notifications/initialized", "notifications/cancelled":
		return resp, false
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		var list []map[string]any
		for _, t := range s.Tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		resp.Result = map[string]any{"tools": list}
	case "tools/call":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "invalid params: " + err.Error()}
			break
		}
		tool := s.find(p.Name)
		if tool == nil {
			resp.Error = &rpcError{-32602, "unknown tool " + p.Name}
			break
		}
		if p.Args == nil {
			p.Args = map[string]any{}
		}
		result, err := tool.Run(ctx, p.Args)
		if err != nil {
			resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}
			break
		}
		text, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			resp.Error = &rpcError{-32603, err.Error()}
			break
		}
		resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}}
	default:
		if len(req.ID) == 0 {
			return resp, false
		}
		resp.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return resp, len(req.ID) > 0
}

func (s *Server) find(name string) *Tool {
	for i := range s.Tools {
		if s.Tools[i].Name == name {
			return &s.Tools[i]
		}
	}
	return nil
}

// Schema builds an object schema from property definitions.
func Schema(props map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func Str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func Num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func Bool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

// String reads an argument as a string ("" when absent).
func String(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return ""
}

func Number(args map[string]any, key string) float64 {
	if v, ok := args[key].(float64); ok {
		return v
	}
	return 0
}

func Flag(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// Stdio serves on the process's stdin and stdout.
func (s *Server) Stdio(ctx context.Context) error {
	return s.Serve(ctx, os.Stdin, os.Stdout)
}
