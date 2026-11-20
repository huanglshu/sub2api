//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestForward_OAuthReplayedToolCallKeepsReasoningPairing drives the real Forward
// pipeline for an OpenAI OAuth (Codex) account with a replayed tool call and its
// reasoning sibling, mirroring the reported gpt-6-luna failure. The forwarding
// body must never carry a tool call id whose reasoning item id was stripped.
func TestForward_OAuthReplayedToolCallKeepsReasoningPairing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model":"gpt-6-luna",
		"stream":true,
		"instructions":"test",
		"previous_response_id":"resp_prev",
		"tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}}],
		"input":[
			{"type":"reasoning","id":"rs_0c8ac4a17b74c3b1006acdecbe9da48194862e0a67684e0b34","summary":[{"type":"summary_text","text":"thinking"}]},
			{"type":"function_call","id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","name":"shell","arguments":"{}"},
			{"type":"function_call_output","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","output":"ok"}
		]
	}`)

	cases := map[string][]byte{
		"oauth_native": body,
		"oauth_passthrough": []byte(`{
			"model":"gpt-6-luna","stream":true,"instructions":"test","store":false,
			"tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}}],
			"input":[
				{"type":"reasoning","id":"rs_0c8ac4a17b74c3b1006acdecbe9da48194862e0a67684e0b34","encrypted_content":"gAAA","summary":[]},
				{"type":"function_call","id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","name":"shell","arguments":"{}"},
				{"type":"function_call_output","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","output":"ok"}
			]
		}`),
		"api_key_store_false": []byte(`{
			"model":"gpt-6-luna","stream":true,"instructions":"test","store":false,
			"tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}}],
			"input":[
				{"type":"reasoning","id":"rs_0c8ac4a17b74c3b1006acdecbe9da48194862e0a67684e0b34","encrypted_content":"gAAA","summary":[]},
				{"type":"function_call","id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","name":"shell","arguments":"{}"},
				{"type":"function_call_output","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","output":"ok"}
			]
		}`),
		// Codex may replay the reasoning item as an item_reference with an rs_*
		// id; the store=false normalizer drops it entirely, so the paired tool
		// call id must go with it.
		"api_key_store_false_item_reference": []byte(`{
			"model":"gpt-6-luna","stream":true,"instructions":"test","store":false,
			"tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}}],
			"input":[
				{"type":"item_reference","id":"rs_0c8ac4a17b74c3b1006acdecbe9da48194862e0a67684e0b34"},
				{"type":"function_call","id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","name":"shell","arguments":"{}"},
				{"type":"function_call_output","call_id":"fc_0c8ac4a17b74c3b1006acdecc3241081948d267c48da6271cd","output":"ok"}
			]
		}`),
	}

	for name, caseBody := range cases {
		t.Run(name, func(t *testing.T) {
			runReasoningPairingForward(t, name, caseBody)
		})
	}
}

func runReasoningPairingForward(t *testing.T, name string, body []byte) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")

	upstreamSSE := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(&config.Config{}),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := &Account{
		ID: 133, Name: name, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
		Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1),
	}
	switch name {
	case "oauth_passthrough":
		account.Extra = map[string]any{"openai_passthrough": true}
	case "api_key_store_false":
		account.Type = AccountTypeAPIKey
		account.Credentials = map[string]any{"api_key": "sk-test"}
	case "api_key_store_false_item_reference":
		account.Type = AccountTypeAPIKey
		account.Credentials = map[string]any{"api_key": "sk-test"}
	}

	_, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)

	forwarded := upstream.lastBody
	require.True(t, gjson.ValidBytes(forwarded), "forwarded body must be JSON")

	// Walk every function_call in the forwarded input: if it still carries an
	// id, some reasoning item with that pairing must remain in the same request.
	input := gjson.GetBytes(forwarded, "input")
	reasoningIDs := map[string]struct{}{}
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) == "reasoning" {
			if id := strings.TrimSpace(item.Get("id").String()); id != "" {
				reasoningIDs[id] = struct{}{}
			}
		}
		return true
	})
	input.ForEach(func(_, item gjson.Result) bool {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType != "function_call" && itemType != "message" {
			return true
		}
		id := strings.TrimSpace(item.Get("id").String())
		if id == "" {
			return true
		}
		// A surviving output item id is only valid when at least one reasoning
		// item id also survived, i.e. the pair was not split.
		require.NotEmpty(t, reasoningIDs,
			"item %s/%s kept its id but no reasoning id survived", itemType, id)
		return true
	})
}
