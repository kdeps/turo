package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// proxyConfig holds the reverse-proxy settings.
type proxyConfig struct {
	listen   string // host:port to listen on
	upstream string // real LLM base URL (e.g. https://api.openai.com)
	all      bool   // reduce every role, not just user + tool
	level    string
	filler   bool
	synonyms bool
	gloss    bool
	defmatch bool
	arrows   bool
	markdown bool // keep markdown/HTML structure and reduce only the prose inside it
	special  bool // preserve tokens with special characters (C++, $5, array[0], …)
	safeMode bool // pass structured content (tool I/O, code/tables/lists) through unreduced
	verbose  bool // print proxy activity (banner, token summary, before -> after text); off = silent
}

// runProxy starts an OpenAI/Anthropic-compatible reverse proxy that runs each
// chat message's content through turo before forwarding to the upstream. The
// response is streamed back untouched. Point an agent at it with
// OPENAI_BASE_URL / ANTHROPIC_BASE_URL.
func runProxy(cfg proxyConfig) error {
	if cfg.verbose {
		fmt.Fprintf(os.Stderr, "turo proxy listening on %s -> %s (reducing %s)\n",
			cfg.listen, cfg.upstream, rolesLabel(cfg.all))
	}
	return http.ListenAndServe(cfg.listen, proxyHandler(cfg)) //nolint:gosec // local dev proxy
}

// proxyHandler builds the reverse-proxy HTTP handler for cfg.
func proxyHandler(cfg proxyConfig) http.HandlerFunc {
	base := strings.TrimRight(cfg.upstream, "/")
	client := &http.Client{}

	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		if isChatPath(r.URL.Path) && len(body) > 0 {
			if reduced, before, after := reducePayload(body, cfg); reduced != nil {
				body = reduced
				recordGain("proxy", before, after)
				if cfg.verbose {
					fmt.Fprintf(os.Stderr, "turo proxy: %s  %d -> %d tokens (est)\n", r.URL.Path, before, after)
				}
			}
		}

		outURL := base + r.URL.Path
		if r.URL.RawQuery != "" {
			outURL += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, outURL, bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		copyHeaders(req.Header, r.Header)
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		streamCopy(w, resp.Body)
	}
}

// proxyPreviewMax bounds how much of a message is echoed in verbose mode so a
// long prompt does not flood the terminal.
const proxyPreviewMax = 300

// proxyPreview renders a message for verbose logging: newlines collapsed to
// spaces and truncated to proxyPreviewMax runes.
func proxyPreview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > proxyPreviewMax {
		return string(r[:proxyPreviewMax]) + "..."
	}
	return s
}

func rolesLabel(all bool) string {
	if all {
		return "all roles"
	}
	return "user + tool roles"
}

// isChatPath reports whether a request path is a chat-completions, messages,
// or Responses endpoint whose body carries reducible message content.
func isChatPath(path string) bool {
	return strings.HasSuffix(path, "/chat/completions") ||
		strings.HasSuffix(path, "/messages") ||
		strings.HasSuffix(path, "/responses")
}

// reducePayload reduces the content of eligible messages in an OpenAI/Anthropic
// (or OpenAI Responses) request body. Returns the rewritten body and estimated
// before/after token totals of the reduced fields, or nil if the body is not
// reducible JSON.
func reducePayload(body []byte, cfg proxyConfig) ([]byte, int, int) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, 0
	}
	before, after := 0, 0
	red := func(role, s string) string {
		out := reduce(s, cfg.level, 0, cfg.filler, cfg.synonyms, cfg.gloss, cfg.defmatch, cfg.arrows, cfg.markdown, cfg.special)
		before += estimateTokens(s)
		after += estimateTokens(out)
		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "  [%s] %s\n       -> %s\n", role, proxyPreview(s), proxyPreview(out))
		}
		return out
	}

	// True once we see a field the reducer knows how to walk. Without this we
	// would re-marshal arbitrary non-chat JSON (e.g. /v1/models) if a caller
	// ever bypassed isChatPath.
	known := false

	// Anthropic top-level system prompt / Responses instructions (only when
	// reducing all roles).
	if cfg.all {
		if s, ok := payload["system"].(string); ok {
			payload["system"] = red("system", s)
			known = true
		}
		if s, ok := payload["instructions"].(string); ok {
			payload["instructions"] = red("instructions", s)
			known = true
		}
	}

	// Chat Completions + Anthropic Messages: top-level messages array.
	if msgs, ok := payload["messages"].([]any); ok {
		reduceMessageList(msgs, cfg, red)
		known = true
	}

	// OpenAI Responses API (Grok Build default): top-level input is a string
	// or an array of message / tool items — no messages field.
	if in, ok := payload["input"]; ok {
		known = true
		switch v := in.(type) {
		case string:
			if shouldReduce("user", cfg.all) && !(cfg.safeMode && isStructured(v)) {
				payload["input"] = red("user", v)
			}
		case []any:
			reduceMessageList(v, cfg, red)
		}
	}

	if !known {
		return nil, 0, 0
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, 0
	}
	return out, before, after
}

// reduceMessageList walks a messages or Responses input array and reduces
// eligible text fields in place.
func reduceMessageList(msgs []any, cfg proxyConfig, red func(role, s string) string) {
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		// Responses tool I/O items use type instead of (or alongside) role.
		itemType, _ := mm["type"].(string)
		if cfg.safeMode && isResponsesToolItem(itemType) {
			continue
		}
		role := responsesRole(mm, itemType)
		if !shouldReduce(role, cfg.all) {
			continue
		}
		// Safe mode: OpenAI tool messages are structured I/O — pass through.
		if cfg.safeMode && role == "tool" {
			continue
		}
		// Responses function_call_output.output is a string tool result.
		if s, ok := mm["output"].(string); ok && itemType == "function_call_output" {
			if !(cfg.safeMode && isStructured(s)) {
				mm["output"] = red(role, s)
			}
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			if cfg.safeMode && isStructured(c) {
				continue
			}
			mm["content"] = red(role, c)
		case []any: // multimodal / Anthropic / Responses content blocks
			for _, part := range c {
				pm, ok := part.(map[string]any)
				if !ok {
					continue
				}
				// Skip structured tool blocks: tool_use args and tool_result
				// output (web results, file/code reads, TUI dumps).
				if cfg.safeMode {
					if bt, _ := pm["type"].(string); bt == "tool_use" || bt == "tool_result" {
						continue
					}
				}
				t, ok := contentPartText(pm)
				if !ok {
					continue
				}
				if cfg.safeMode && isStructured(t) {
					continue
				}
				setContentPartText(pm, red(role, t))
			}
		}
	}
}

// isResponsesToolItem reports Responses API item types whose structured
// payload should pass through unreduced in safe mode.
func isResponsesToolItem(itemType string) bool {
	switch itemType {
	case "function_call", "function_call_output", "tool_use", "tool_result",
		"computer_call", "computer_call_output", "file_search_call",
		"web_search_call", "code_interpreter_call", "mcp_call", "mcp_list_tools":
		return true
	}
	return false
}

// responsesRole maps a Responses/chat item to a synthetic role for shouldReduce.
// function_call_output is treated as tool; bare message items use their role.
func responsesRole(mm map[string]any, itemType string) string {
	if role, ok := mm["role"].(string); ok && role != "" {
		return role
	}
	switch itemType {
	case "function_call_output", "tool_result", "computer_call_output":
		return "tool"
	case "function_call", "tool_use", "computer_call", "file_search_call",
		"web_search_call", "code_interpreter_call", "mcp_call":
		return "assistant"
	}
	return "user"
}

// contentPartText extracts a reducible text string from a content part.
// Handles chat/Anthropic {"text":"..."} and Responses parts that store the
// string under "text" with type input_text/output_text.
func contentPartText(pm map[string]any) (string, bool) {
	if t, ok := pm["text"].(string); ok {
		return t, true
	}
	return "", false
}

// setContentPartText writes the reduced string back onto the part's text field.
func setContentPartText(pm map[string]any, s string) {
	pm["text"] = s
}

// shouldReduce reports whether a message role is eligible for reduction.
// The safe default is user + tool content; system and assistant history are
// left verbatim unless all is set (they are lossier to touch).
func shouldReduce(role string, all bool) bool {
	if all {
		return true
	}
	return role == "user" || role == "tool"
}

// hopByHop headers must not be forwarded across the proxy.
var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Content-Length": true,
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if hopByHop[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// streamCopy forwards the response body, flushing after each chunk so
// server-sent event streams reach the client incrementally.
func streamCopy(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
