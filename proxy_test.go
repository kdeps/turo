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

func TestReducePayloadSafeMode(t *testing.T) {
	prose := "Please utilize this approach to demonstrate the functionality."
	table := "| A | B |\n|---|---|\n| one | two |\n| three | four |"
	body := `{"model":"x","messages":[` +
		`{"role":"user","content":"` + prose + `"},` +
		`{"role":"user","content":` + jsonStr(table) + `},` +
		`{"role":"tool","content":"` + prose + `"},` +
		`{"role":"user","content":[` +
		`{"type":"tool_result","content":[{"type":"text","text":"` + prose + `"}]},` +
		`{"type":"text","text":"` + prose + `"}]}` +
		`]}`

	// safe mode ON: content-shaped — prose reduces even in tool role / tool_result;
	// tables and machine dumps pass through.
	out, beforeOn, afterOn := reducePayload([]byte(body), proxyConfig{all: true, level: "full", filler: true, safeMode: true})
	msgs := payloadMsgs(t, out)
	if s := msgs[0]["content"].(string); strings.Contains(s, "Please") {
		t.Errorf("prose user msg should reduce, got %q", s)
	}
	if s := msgs[1]["content"].(string); s != table {
		t.Errorf("markdown table should pass through, got %q", s)
	}
	if s := msgs[2]["content"].(string); strings.Contains(s, "Please") {
		t.Errorf("prose tool role should reduce under smart safe mode, got %q", s)
	}
	blocks := msgs[3]["content"].([]any)
	tr := blocks[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(tr, "Please") {
		t.Errorf("prose tool_result text should reduce, got %q", tr)
	}
	if txt := blocks[1].(map[string]any)["text"].(string); strings.Contains(txt, "Please") {
		t.Errorf("plain text block should reduce, got %q", txt)
	}

	// safe mode OFF: table reduces too.
	out2, beforeOff, afterOff := reducePayload([]byte(body), proxyConfig{all: true, level: "full", filler: true, safeMode: false})
	m2 := payloadMsgs(t, out2)
	if s := m2[1]["content"].(string); s == table {
		t.Errorf("with safe mode off, table should reduce, got verbatim %q", s)
	}

	// Gain accounting: passthrough fields must still count toward before/after.
	if beforeOn <= 0 || afterOn <= 0 {
		t.Fatalf("safe mode gain totals empty: before=%d after=%d", beforeOn, afterOn)
	}
	if afterOn > beforeOn {
		t.Fatalf("safe mode after > before: %d > %d", afterOn, beforeOn)
	}
	if beforeOn < beforeOff {
		t.Errorf("safe mode before=%d should include passthrough tokens (>= off before=%d)", beforeOn, beforeOff)
	}
	// Off mode also squeezes the table → more absolute savings.
	savedOn := beforeOn - afterOn
	savedOff := beforeOff - afterOff
	if savedOff < savedOn {
		t.Errorf("safe mode off should save at least as many tokens: off=%d on=%d", savedOff, savedOn)
	}
	if afterOff > afterOn {
		t.Errorf("safe mode off should not produce more output tokens: off=%d on=%d", afterOff, afterOn)
	}
}

// TestReducePayloadSafeModeSmartToolResults checks content-shaped tool I/O:
// prose tool results reduce; bash / code dumps do not.
func TestReducePayloadSafeModeSmartToolResults(t *testing.T) {
	prose := "Please utilize this approach to demonstrate the functionality."
	bash := "$ go test ./...\nok  \tgithub.com/kdeps/turo\t1.234s\nFAIL\tgithub.com/kdeps/turo/x\t0.100s\n--- FAIL: TestFoo (0.00s)\n"
	code := "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n"
	body := `{"model":"x","messages":[` +
		`{"role":"tool","content":` + jsonStr(prose) + `},` +
		`{"role":"tool","content":` + jsonStr(bash) + `},` +
		`{"role":"user","content":[` +
		`{"type":"tool_result","content":[{"type":"text","text":` + jsonStr(prose) + `}]},` +
		`{"type":"tool_result","content":[{"type":"text","text":` + jsonStr(code) + `}]}` +
		`]}` +
		`]}`

	out, _, _ := reducePayload([]byte(body), proxyConfig{all: true, level: "full", filler: true, safeMode: true})
	msgs := payloadMsgs(t, out)
	if s := msgs[0]["content"].(string); strings.Contains(s, "Please") {
		t.Errorf("prose tool content should reduce, got %q", s)
	}
	if s := msgs[1]["content"].(string); s != bash {
		t.Errorf("bash tool dump should pass through, got %q", s)
	}
	blocks := msgs[2]["content"].([]any)
	trProse := blocks[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(trProse, "Please") {
		t.Errorf("prose tool_result should reduce, got %q", trProse)
	}
	trCode := blocks[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if trCode != code {
		t.Errorf("fenced code tool_result should pass through, got %q", trCode)
	}
}

// TestReducePayloadSafeModeReducesAroundProtected checks that mixed blobs —
// prose intro/outro wrapped around a machine dump — shrink the prose while
// leaving the dump byte-identical. Previously safe mode skipped the whole field.
func TestReducePayloadSafeModeReducesAroundProtected(t *testing.T) {
	intro := "Please utilize this approach to demonstrate the functionality carefully."
	bash := "$ go test ./...\nok  \tgithub.com/kdeps/turo\t1.234s\nFAIL\tgithub.com/kdeps/turo/x\t0.100s\n--- FAIL: TestFoo (0.00s)"
	outro := "Based on this, it appears the functionality is broken and you should fix the approach."
	mixed := intro + "\n\n" + bash + "\n\n" + outro
	code := "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```"
	mixedFence := intro + "\n\n" + code + "\n\n" + outro

	body := `{"model":"x","messages":[` +
		`{"role":"tool","content":` + jsonStr(mixed) + `},` +
		`{"role":"user","content":` + jsonStr(mixedFence) + `}` +
		`]}`

	out, before, after := reducePayload([]byte(body), proxyConfig{all: true, level: "full", filler: true, safeMode: true})
	msgs := payloadMsgs(t, out)

	gotBash := msgs[0]["content"].(string)
	if !strings.Contains(gotBash, bash) {
		t.Errorf("bash dump should survive intact inside mixed blob, got %q", gotBash)
	}
	if strings.Contains(gotBash, "Please") || strings.Contains(gotBash, "utilize") {
		t.Errorf("intro prose around bash should reduce, got %q", gotBash)
	}
	if strings.Contains(gotBash, "it appears") {
		t.Errorf("outro prose around bash should reduce, got %q", gotBash)
	}

	gotFence := msgs[1]["content"].(string)
	if !strings.Contains(gotFence, code) {
		t.Errorf("fenced code should survive intact inside mixed blob, got %q", gotFence)
	}
	if strings.Contains(gotFence, "Please") {
		t.Errorf("prose around fence should reduce, got %q", gotFence)
	}

	if after >= before {
		t.Fatalf("mixed safe-mode blob should save tokens: before=%d after=%d", before, after)
	}
	// Pure wholesale skip would save nothing on these structured fields.
	if saved := before - after; saved < 3 {
		t.Fatalf("expected meaningful savings around protected spans, saved=%d (before=%d after=%d)", saved, before, after)
	}
}

// TestReducePayloadSafeModeGainCountsPassthrough locks the gain bug where
// unreduced safe-mode fields were omitted from before/after, making
// tokens-saved % look far higher than the true request compression.
func TestReducePayloadSafeModeGainCountsPassthrough(t *testing.T) {
	// One short reducible user line + one large structured blob that safe
	// mode leaves alone. If passthrough is ignored, before ≈ short line only
	// and saved% looks huge; with correct accounting before includes the blob.
	prose := "Please utilize this approach carefully."
	blob := strings.Repeat("| colA | colB |\n| --- | --- |\n| value1 | value2 |\n", 40)
	body := `{"model":"x","messages":[` +
		`{"role":"user","content":"` + prose + `"},` +
		`{"role":"tool","content":` + jsonStr(blob) + `}` +
		`]}`

	cfg := proxyConfig{all: true, level: "full", filler: true, safeMode: true}
	_, before, after := reducePayload([]byte(body), cfg)
	proseN := estimateTokens(prose)
	blobN := estimateTokens(blob)
	if before < proseN+blobN {
		t.Fatalf("before=%d want at least prose+blob=%d (passthrough must count)", before, proseN+blobN)
	}
	// Blob is unreduced → after includes full blobN; only prose shrinks.
	if after < blobN {
		t.Fatalf("after=%d want at least blob=%d", after, blobN)
	}
	saved := before - after
	if saved <= 0 {
		t.Fatalf("expected some savings on the prose line, saved=%d", saved)
	}
	if saved >= blobN {
		t.Fatalf("saved=%d looks like passthrough was treated as reduced (blob=%d)", saved, blobN)
	}
	// Savings must be a small fraction of the full request, not ~70% of prose only.
	if pct := saved * 100 / before; pct > 40 {
		t.Fatalf("saved %d%% of full request looks inflated (before=%d after=%d saved=%d)", pct, before, after, saved)
	}

	// Same payload without safe mode: tool blob reduces, more absolute savings.
	_, b2, a2 := reducePayload([]byte(body), proxyConfig{all: true, level: "full", filler: true, safeMode: false})
	if b2-a2 <= saved {
		t.Fatalf("safe mode off should save more tokens: off=%d on=%d", b2-a2, saved)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func payloadMsgs(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	raw := p["messages"].([]any)
	out := make([]map[string]any, len(raw))
	for i, m := range raw {
		out[i] = m.(map[string]any)
	}
	return out
}
