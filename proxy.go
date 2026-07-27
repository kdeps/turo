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
// before/after token totals across every text field the proxy considered —
// reduced fields contribute their compressed size; safe-mode (and other)
// passthroughs contribute the same count to both sides so turo gain does not
// inflate % saved by ignoring unreduced tool I/O and structured blobs.
func reducePayload(body []byte, cfg proxyConfig) ([]byte, int, int) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, 0
	}
	before, after := 0, 0
	// red compresses and counts a field toward both totals.
	red := func(role, s string) string {
		out := reduce(s, cfg.level, 0, cfg.filler, cfg.synonyms, cfg.gloss, cfg.defmatch, cfg.arrows, cfg.markdown, cfg.special)
		before += estimateTokens(s)
		after += estimateTokens(out)
		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "  [%s] %s\n       -> %s\n", role, proxyPreview(s), proxyPreview(out))
		}
		return out
	}
	// pass counts text left unreduced (safe-mode skips, ineligible roles) so
	// gain's tokens-in/out match the full request the upstream still receives.
	pass := func(s string) {
		if s == "" {
			return
		}
		n := estimateTokens(s)
		before += n
		after += n
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
		reduceMessageList(msgs, cfg, red, pass)
		known = true
	}

	// OpenAI Responses API (Grok Build default): top-level input is a string
	// or an array of message / tool items — no messages field.
	if in, ok := payload["input"]; ok {
		known = true
		switch v := in.(type) {
		case string:
			if !shouldReduce("user", cfg.all) || (cfg.safeMode && isStructured(v)) {
				pass(v)
			} else {
				payload["input"] = red("user", v)
			}
		case []any:
			reduceMessageList(v, cfg, red, pass)
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
// eligible text fields in place. pass is invoked for every text field left
// unreduced so token accounting stays honest under -proxy-safe-mode.
//
// Safe mode is content-shaped, not role-shaped: tool *calls* (JSON args) always
// pass through, but tool *results* and other text reduce unless isStructured
// says they look like code, shell dumps, tables, JSON, etc.
func reduceMessageList(msgs []any, cfg proxyConfig, red func(role, s string) string, pass func(string)) {
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := mm["type"].(string)
		// Tool *calls* are JSON/schema machinery — never reduce in safe mode.
		if cfg.safeMode && isToolCallItem(itemType) {
			passMessageText(mm, pass)
			continue
		}
		role := responsesRole(mm, itemType)
		if !shouldReduce(role, cfg.all) {
			// Ineligible roles still ride to the upstream — count them so
			// gain % is against the full request, not only reduced roles.
			passMessageText(mm, pass)
			continue
		}
		// Responses function_call_output.output / computer_call_output text.
		if s, ok := mm["output"].(string); ok && isToolResultItem(itemType) {
			mm["output"] = safeRed(cfg, role, s, red, pass)
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			// tool role, user, assistant — all content-shaped under safe mode.
			mm["content"] = safeRed(cfg, role, c, red, pass)
		case []any: // multimodal / Anthropic / Responses content blocks
			for _, part := range c {
				pm, ok := part.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := pm["type"].(string)
				// tool_use args stay intact; tool_result text is content-shaped.
				if cfg.safeMode && bt == "tool_use" {
					passContentPart(pm, pass)
					continue
				}
				if bt == "tool_result" {
					reduceOrPassContentPart(pm, cfg, role, red, pass)
					continue
				}
				t, ok := contentPartText(pm)
				if !ok {
					// Images / nested non-text — count any nested strings only.
					passContentPart(pm, pass)
					continue
				}
				setContentPartText(pm, safeRed(cfg, role, t, red, pass))
			}
		}
	}
}

// safeRed reduces s unless safe mode is on and the text looks structured
// (code, shell output, tables, JSON, …). Passthrough still counts toward gain.
func safeRed(cfg proxyConfig, role, s string, red func(role, s string) string, pass func(string)) string {
	if cfg.safeMode && isStructured(s) {
		pass(s)
		return s
	}
	return red(role, s)
}

// reduceOrPassContentPart walks a content block (including nested tool_result
// content arrays), reducing prose leaves and passing structured ones.
func reduceOrPassContentPart(pm map[string]any, cfg proxyConfig, role string, red func(role, s string) string, pass func(string)) {
	if t, ok := pm["text"].(string); ok {
		pm["text"] = safeRed(cfg, role, t, red, pass)
	}
	switch c := pm["content"].(type) {
	case string:
		pm["content"] = safeRed(cfg, role, c, red, pass)
	case []any:
		for _, part := range c {
			if nested, ok := part.(map[string]any); ok {
				reduceOrPassContentPart(nested, cfg, role, red, pass)
			}
		}
	}
}

// passMessageText feeds every reducible string on a message/item into pass so
// safe-mode skips still appear in gain's before/after totals.
func passMessageText(mm map[string]any, pass func(string)) {
	if s, ok := mm["output"].(string); ok {
		pass(s)
	}
	switch c := mm["content"].(type) {
	case string:
		pass(c)
	case []any:
		for _, part := range c {
			if pm, ok := part.(map[string]any); ok {
				passContentPart(pm, pass)
			}
		}
	}
}

// passContentPart walks a content block for text fields (including nested
// tool_result content arrays) and counts them as unreduced.
func passContentPart(pm map[string]any, pass func(string)) {
	if t, ok := pm["text"].(string); ok {
		pass(t)
	}
	switch c := pm["content"].(type) {
	case string:
		pass(c)
	case []any:
		for _, part := range c {
			if nested, ok := part.(map[string]any); ok {
				passContentPart(nested, pass)
			}
		}
	}
}

// isToolCallItem reports Responses/chat item types that carry call args or
// tool machinery (not free-text results). Safe mode always leaves these alone.
func isToolCallItem(itemType string) bool {
	switch itemType {
	case "function_call", "tool_use", "computer_call", "file_search_call",
		"web_search_call", "code_interpreter_call", "mcp_call", "mcp_list_tools":
		return true
	}
	return false
}

// isToolResultItem reports item types whose payload is tool *output* text —
// safe mode decides per-string via isStructured rather than skipping wholesale.
func isToolResultItem(itemType string) bool {
	switch itemType {
	case "function_call_output", "tool_result", "computer_call_output":
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
