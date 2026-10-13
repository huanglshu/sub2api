//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestFilterCodexInput_DropsAssistantMessageIDWhenReasoningIDStripped covers
// the store=false OAuth path: reasoning ids are always stripped there, so a
// replayed assistant message id would reference a reasoning item the upstream
// can no longer resolve, producing "was provided without its required
// 'reasoning' item".
func TestFilterCodexInput_DropsAssistantMessageIDWhenReasoningIDStripped(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "reasoning",
			"id":   "rs_053ffa337c167cf1006acda50f5c108197b6f5fb8162f475af",
			"summary": []any{
				map[string]any{"type": "summary_text", "text": "thinking"},
			},
		},
		map[string]any{
			"type":    "message",
			"id":      "msg_053ffa337c167cf1006acda5107458819786143a2606d737ab",
			"role":    "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "hi"}},
		},
		map[string]any{
			"type":    "message",
			"id":      "msg_user_keep",
			"role":    "user",
			"content": "hello",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})
	require.Len(t, filtered, 3)

	reasoning := filtered[0].(map[string]any)
	_, hasReasoningID := reasoning["id"]
	require.False(t, hasReasoningID, "reasoning id must be stripped on the OAuth/store=false path")

	assistant := filtered[1].(map[string]any)
	_, hasAssistantID := assistant["id"]
	require.False(t, hasAssistantID, "assistant message id must be dropped with its reasoning id")

	user := filtered[2].(map[string]any)
	require.Equal(t, "msg_user_keep", user["id"], "user message ids are not part of a reasoning pair")
}

// TestFilterCodexInput_KeepsAssistantMessageIDWithoutReasoning ensures the
// pairing logic only fires when reasoning items actually break the pair, so
// ordinary continuation references keep their ids.
func TestFilterCodexInput_KeepsAssistantMessageIDWithoutReasoning(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "message",
			"id":      "msg_assistant_keep",
			"role":    "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "hi"}},
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})
	require.Len(t, filtered, 1)
	require.Equal(t, "msg_assistant_keep", filtered[0].(map[string]any)["id"])
}

// TestSanitizeOpenAICrossModeFailoverReasoning_DropsPairedMessageID verifies
// that dropping a provider-specific encrypted reasoning item on the
// passthrough -> non-passthrough failover also drops the paired assistant
// message id.
func TestSanitizeOpenAICrossModeFailoverReasoning_DropsPairedMessageID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":[` +
		`{"type":"message","id":"msg_user_1","role":"user","content":"hi"},` +
		`{"type":"reasoning","id":"rs_kiro_1","encrypted_content":"ENC","summary":[{"type":"summary_text","text":"t"}]},` +
		`{"type":"message","id":"msg_assistant_1","role":"assistant","content":[{"type":"output_text","text":"yo"}]},` +
		`{"type":"message","id":"msg_user_2","role":"user","content":"again"}` +
		`]}`)

	sanitized, changed, err := SanitizeOpenAICrossModeFailoverReasoning(body)
	require.NoError(t, err)
	require.True(t, changed)

	require.Equal(t, int64(3), gjson.GetBytes(sanitized, "input.#").Int())
	require.Equal(t, "msg_user_1", gjson.GetBytes(sanitized, "input.0.id").String())
	require.False(t, gjson.GetBytes(sanitized, "input.1.id").Exists(),
		"assistant message id must be dropped once its reasoning item is gone")
	require.Equal(t, "msg_user_2", gjson.GetBytes(sanitized, "input.2.id").String())
}

// TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplay_DropsPairedMessageID
// covers the API-key store=false path, which drops reasoning items that carry
// no usable encrypted content.
func TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplay_DropsPairedMessageID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","store":false,"input":[` +
		`{"type":"reasoning","id":"rs_server_only","summary":[{"type":"summary_text","text":"x"}]},` +
		`{"type":"message","id":"msg_assistant_1","role":"assistant","content":[{"type":"output_text","text":"yo"}]},` +
		`{"type":"message","id":"msg_user_1","role":"user","content":"hi"}` +
		`]}`)

	normalized, changed, err := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, false)
	require.NoError(t, err)
	require.True(t, changed)

	require.Equal(t, int64(2), gjson.GetBytes(normalized, "input.#").Int())
	require.False(t, gjson.GetBytes(normalized, "input.0.id").Exists(),
		"assistant message id must be dropped with the reasoning item")
	require.Equal(t, "msg_user_1", gjson.GetBytes(normalized, "input.1.id").String())
}

// TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplay_KeepsMessageIDWithoutBreak
// guards that a request whose reasoning item survives unchanged is not
// rewritten: the reasoning item has encrypted content (so it is not dropped)
// and does not carry an rs_* id (so no id lookup runs), leaving the pair intact.
func TestNormalizeOpenAIAPIKeyStoreFalseReasoningReplay_KeepsMessageIDWithoutBreak(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","store":false,"input":[` +
		`{"type":"reasoning","id":"item_bad","encrypted_content":"cipher","summary":[]},` +
		`{"type":"message","id":"msg_assistant_1","role":"assistant","content":[{"type":"output_text","text":"yo"}]}` +
		`]}`)

	normalized, changed, err := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, false)
	require.NoError(t, err)
	require.False(t, changed, "intact reasoning pairs must not be rewritten")
	require.Equal(t, "item_bad", gjson.GetBytes(normalized, "input.0.id").String())
	require.Equal(t, "msg_assistant_1", gjson.GetBytes(normalized, "input.1.id").String())
}

// TestSanitizeOpenAIResponsesInputItemIDs_DropsPairedMessageID covers the
// generic id sanitizer that strips malformed reasoning ids.
func TestSanitizeOpenAIResponsesInputItemIDs_DropsPairedMessageID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":[` +
		`{"type":"reasoning","id":"item_bad_reasoning","summary":[]},` +
		`{"type":"message","id":"msg_assistant_1","role":"assistant","content":[{"type":"output_text","text":"yo"}]},` +
		`{"type":"message","id":"msg_user_1","role":"user","content":"hi"}` +
		`]}`)

	sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.True(t, changed)

	require.False(t, gjson.GetBytes(sanitized, "input.0.id").Exists())
	require.False(t, gjson.GetBytes(sanitized, "input.1.id").Exists(),
		"assistant message id must be dropped with the malformed reasoning id")
	require.Equal(t, "msg_user_1", gjson.GetBytes(sanitized, "input.2.id").String())
}

// TestSanitizeOpenAIResponsesInputItemIDs_KeepsMessageIDWithValidReasoningID
// ensures a structurally valid request is not rewritten.
func TestSanitizeOpenAIResponsesInputItemIDs_KeepsMessageIDWithValidReasoningID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":[` +
		`{"type":"reasoning","id":"rs_valid","summary":[]},` +
		`{"type":"message","id":"msg_assistant_1","role":"assistant","content":[{"type":"output_text","text":"yo"}]}` +
		`]}`)

	sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(sanitized))
}

// TestFilterCodexInput_DropsFunctionCallIDWhenReasoningIDStripped covers the
// reported regression where the dangling item was a tool call rather than a
// message: "Item 'fc_...' of type 'function_call' was provided without its
// required 'reasoning' item". The tool call id must be dropped with the
// reasoning id, while call_id (the execution pairing key) survives.
func TestFilterCodexInput_DropsFunctionCallIDWhenReasoningIDStripped(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "reasoning",
			"id":   "rs_0a005b518b362adb006acde6e812388197af09c24647e31c66",
			"summary": []any{
				map[string]any{"type": "summary_text", "text": "thinking"},
			},
		},
		map[string]any{
			"type":      "function_call",
			"id":        "fc_0a005b518b362adb006acde6ea57fc8197b2b7cb70394f8463",
			"call_id":   "fc_keep_1",
			"name":      "shell",
			"arguments": "{}",
		},
		map[string]any{
			"type":    "function_call_output",
			"id":      "fc_output_keep",
			"call_id": "fc_keep_1",
			"output":  "done",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{PreserveReferences: true})
	require.Len(t, filtered, 3)

	call := filtered[1].(map[string]any)
	_, hasCallItemID := call["id"]
	require.False(t, hasCallItemID, "function_call id must be dropped with its reasoning id")
	require.Equal(t, "fc_keep_1", call["call_id"], "call_id execution pairing must survive")

	output := filtered[2].(map[string]any)
	require.Equal(t, "fc_output_keep", output["id"], "tool output ids are not reasoning-paired")
}

// TestSanitizeOpenAIResponsesInputItemIDs_DropsPairedToolCallID covers the
// generic id sanitizer for a malformed reasoning id followed by a tool call.
func TestSanitizeOpenAIResponsesInputItemIDs_DropsPairedToolCallID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":[` +
		`{"type":"reasoning","id":"item_bad_reasoning","summary":[]},` +
		`{"type":"function_call","id":"fc_good","call_id":"call_1","name":"shell","arguments":"{}"}` +
		`]}`)

	sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.True(t, changed)

	require.False(t, gjson.GetBytes(sanitized, "input.0.id").Exists())
	require.False(t, gjson.GetBytes(sanitized, "input.1.id").Exists(),
		"paired tool call id must be dropped with the malformed reasoning id")
	require.Equal(t, "call_1", gjson.GetBytes(sanitized, "input.1.call_id").String())
}

// TestSanitizeOpenAICrossModeFailoverReasoning_DropsPairedToolCallID verifies
// that dropping an encrypted reasoning item on cross-mode failover also drops
// the paired tool call id.
func TestSanitizeOpenAICrossModeFailoverReasoning_DropsPairedToolCallID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":[` +
		`{"type":"reasoning","id":"rs_kiro_1","encrypted_content":"ENC","summary":[{"type":"summary_text","text":"t"}]},` +
		`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"done"}` +
		`]}`)

	sanitized, changed, err := SanitizeOpenAICrossModeFailoverReasoning(body)
	require.NoError(t, err)
	require.True(t, changed)

	require.Equal(t, int64(2), gjson.GetBytes(sanitized, "input.#").Int())
	require.Equal(t, "function_call", gjson.GetBytes(sanitized, "input.0.type").String())
	require.False(t, gjson.GetBytes(sanitized, "input.0.id").Exists(),
		"tool call id must be dropped once its reasoning item is gone")
	require.Equal(t, "call_1", gjson.GetBytes(sanitized, "input.0.call_id").String())
}

// TestTrimOpenAIEncryptedReasoningItems_DropsPairedToolCallID covers the
// invalid_encrypted_content retry / lineage strip path. When a reasoning item
// collapses to a bare skeleton and is removed, the tool call id that would have
// referenced it must be dropped too.
func TestTrimOpenAIEncryptedReasoningItems_DropsPairedToolCallID(t *testing.T) {
	callID := "fc_0a005b518b362adb006acde6ea57fc8197b2b7cb70394f8463"
	reqBody := map[string]any{
		"input": []any{
			map[string]any{
				// No id: trimming the null content leaves a bare skeleton that
				// is removed entirely.
				"type":    "reasoning",
				"content": nil,
			},
			map[string]any{
				"type":      "function_call",
				"id":        callID,
				"call_id":   "fc_keep_1",
				"name":      "shell",
				"arguments": "{}",
			},
		},
	}

	changed := trimOpenAIEncryptedReasoningItems(reqBody)
	require.True(t, changed)

	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)

	call, ok := input[0].(map[string]any)
	require.True(t, ok)
	_, hasCallItemID := call["id"]
	require.False(t, hasCallItemID, "tool call id must be dropped with its reasoning item")
	require.Equal(t, "fc_keep_1", call["call_id"], "call_id execution pairing must survive")
}
