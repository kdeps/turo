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
}

// runProxy starts an OpenAI/Anthropic-compatible reverse proxy that runs each
// chat message's content through turo before forwarding to the upstream. The
// response is streamed back untouched. Point an agent at it with
// OPENAI_BASE_URL / ANTHROPIC_BASE_URL.
func runProxy(cfg proxyConfig) error {
	fmt.Fprintf(os.Stderr, "turo proxy listening on %s -> %s (reducing %s)\n",
		cfg.listen, cfg.upstream, rolesLabel(cfg.all))
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
				fmt.Fprintf(os.Stderr, "turo proxy: %s  %d -> %d tokens (est)\n", r.URL.Path, before, after)
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
		defer resp.Body.Close()

		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		streamCopy(w, resp.Body)
	}
}

func rolesLabel(all bool) string {
	if all {
		return "all roles"
	}
	return "user + tool roles"
}

// isChatPath reports whether a request path is a chat-completions or messages
// endpoint whose body carries reducible message content.
func isChatPath(path string) bool {
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/messages")
}

// reducePayload reduces the content of eligible messages in an OpenAI/Anthropic
// request body. Returns the rewritten body and estimated before/after token
// totals of the reduced fields, or nil if the body is not reducible JSON.
func reducePayload(body []byte, cfg proxyConfig) ([]byte, int, int) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, 0
	}
	before, after := 0, 0
	red := func(s string) string {
		out := reduce(s, cfg.level, 0, 0, cfg.filler, cfg.synonyms, cfg.gloss)
		before += estimateTokens(s)
		after += estimateTokens(out)
		return out
	}

	// Anthropic top-level system prompt (only when reducing all roles).
	if cfg.all {
		if s, ok := payload["system"].(string); ok {
			payload["system"] = red(s)
		}
	}

	msgs, ok := payload["messages"].([]any)
	if !ok {
		return nil, 0, 0
	}
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		if !shouldReduce(role, cfg.all) {
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			mm["content"] = red(c)
		case []any: // multimodal / Anthropic content blocks: reduce text parts
			for _, part := range c {
				if pm, ok := part.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok {
						pm["text"] = red(t)
					}
				}
			}
		}
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, 0
	}
	return out, before, after
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
