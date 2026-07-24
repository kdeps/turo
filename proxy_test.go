package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func msgContent(t *testing.T, received string, idx int) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(received), &payload); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	msgs, ok := payload["messages"].([]any)
	if !ok || idx >= len(msgs) {
		t.Fatalf("no message %d in %q", idx, received)
	}
	return msgs[idx].(map[string]any)["content"].(string)
}

func TestProxyHandler_ReducesUserLeavesSystem(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL, level: "full", filler: true})
	body := `{"model":"x","messages":[` +
		`{"role":"system","content":"You are a helpful assistant that should always be polite."},` +
		`{"role":"user","content":"Please utilize this approach to demonstrate the functionality."}]}`
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Fatalf("response not passed through: %q", w.Body.String())
	}
	if sys := msgContent(t, received, 0); !strings.Contains(sys, "polite") {
		t.Fatalf("system role should be verbatim by default, got %q", sys)
	}
	usr := msgContent(t, received, 1)
	if strings.Contains(usr, "Please") || strings.Contains(strings.ToLower(usr), " the ") {
		t.Fatalf("user role should be reduced, got %q", usr)
	}
}

func TestProxyHandler_AllReducesSystem(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
	}))
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL, level: "full", filler: true, all: true})
	body := `{"messages":[{"role":"system","content":"Please always be very polite and helpful."}]}`
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	if sys := msgContent(t, received, 0); strings.Contains(sys, "Please") {
		t.Fatalf("with all=true the system role should be reduced, got %q", sys)
	}
}

func TestProxyHandler_NonChatPassThrough(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
	}))
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL, level: "full", filler: true})
	body := `{"messages":[{"role":"user","content":"Please utilize this."}]}`
	w := httptest.NewRecorder()
	// /v1/models is not a chat path — body must pass through untouched.
	h(w, httptest.NewRequest(http.MethodPost, "/v1/models", strings.NewReader(body)))

	if received != body {
		t.Fatalf("non-chat path should pass body through unchanged:\n got %q\nwant %q", received, body)
	}
}

func TestProxyPreview(t *testing.T) {
	if got := proxyPreview("line one\n\tline two   line three"); got != "line one line two line three" {
		t.Fatalf("newlines/tabs should collapse to single spaces, got %q", got)
	}
	long := strings.Repeat("a", proxyPreviewMax+50)
	got := proxyPreview(long)
	if !strings.HasSuffix(got, "...") || len([]rune(got)) != proxyPreviewMax+3 {
		t.Fatalf("long preview should truncate to %d runes + ellipsis, got %d", proxyPreviewMax, len([]rune(got)))
	}
}

// The default (verbose off) is silent but must still reduce the payload.
func TestProxyHandler_SilentStillReduces(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
	}))
	defer upstream.Close()

	h := proxyHandler(proxyConfig{upstream: upstream.URL, level: "full", filler: true}) // verbose: false
	body := `{"messages":[{"role":"user","content":"Please utilize this approach."}]}`
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	if usr := msgContent(t, received, 0); strings.Contains(usr, "Please") {
		t.Fatalf("silent proxy must not disable reduction, got %q", usr)
	}
}

func TestShouldReduce(t *testing.T) {
	cases := []struct {
		role string
		all  bool
		want bool
	}{
		{"user", false, true}, {"tool", false, true},
		{"system", false, false}, {"assistant", false, false},
		{"system", true, true}, {"assistant", true, true},
	}
	for _, c := range cases {
		if got := shouldReduce(c.role, c.all); got != c.want {
			t.Errorf("shouldReduce(%q, %v) = %v, want %v", c.role, c.all, got, c.want)
		}
	}
}
