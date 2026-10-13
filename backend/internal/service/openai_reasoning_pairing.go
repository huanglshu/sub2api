package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAI Responses pairs every model-generated output item with the reasoning
// item that produced it and resolves the pair through the item id. When both
// items are replayed with their upstream ids the server validates the pair;
// when the gateway strips reasoning ids (a replayed rs_* id 404s under
// store=false, so it must go) the surviving output item id leaves the pair
// unresolvable and the request is rejected with:
//
//	Item 'msg_...' of type 'message' was provided without its required
//	'reasoning' item: 'rs_...'
//	Item 'fc_...' of type 'function_call' was provided without its required
//	'reasoning' item: 'rs_...'
//
// The same happens when a whole provider-specific reasoning item is dropped on
// the cross-mode failover path. Tool-call execution pairing is keyed by call_id
// (function_call <-> function_call_output) and never depends on the item id, so
// the id of any reasoning-paired output item must be dropped whenever reasoning
// ids in the same request are dropped or rewritten. User/system messages and
// tool outputs are not part of a reasoning pairing and keep their ids.

// openAIReasoningPairedItemType reports whether itemType is a model-generated
// output item that the upstream resolves against a preceding reasoning item.
func openAIReasoningPairedItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call",
		"custom_tool_call",
		"tool_search_call",
		"local_shell_call",
		"mcp_tool_call",
		"web_search_call",
		"image_generation_call",
		"code_interpreter_call":
		return true
	default:
		return false
	}
}

// openAIReasoningPairedItem reports whether item is a model-generated output
// item that still carries an id and therefore keeps a reasoning pairing the
// upstream will try to resolve.
func openAIReasoningPairedItem(item map[string]any) bool {
	if strings.TrimSpace(firstNonEmptyString(item["id"])) == "" {
		return false
	}
	itemType := strings.TrimSpace(firstNonEmptyString(item["type"]))
	if openAIReasoningPairedItemType(itemType) {
		return true
	}
	return itemType == "message" &&
		strings.EqualFold(strings.TrimSpace(firstNonEmptyString(item["role"])), "assistant")
}

func openAIReasoningPairedItemRaw(item gjson.Result) bool {
	if strings.TrimSpace(item.Get("id").String()) == "" {
		return false
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	if openAIReasoningPairedItemType(itemType) {
		return true
	}
	return itemType == "message" &&
		strings.EqualFold(strings.TrimSpace(item.Get("role").String()), "assistant")
}

// dropOpenAIReasoningPairedItemIDRaw removes the id from a single raw JSON input
// item when it is a reasoning-paired output item. It returns the original slice
// unchanged when nothing was removed.
func dropOpenAIReasoningPairedItemIDRaw(item []byte) ([]byte, bool) {
	if len(item) == 0 || !openAIReasoningPairedItemRaw(parseRawJSONView(item)) {
		return item, false
	}
	updated, err := sjson.DeleteBytes(item, "id")
	if err != nil {
		return item, false
	}
	return updated, true
}

func openAIInputItemIsReasoning(item map[string]any) bool {
	return strings.TrimSpace(firstNonEmptyString(item["type"])) == "reasoning"
}

func openAIInputHasReasoningItem(items []any) bool {
	for _, raw := range items {
		if item, ok := raw.(map[string]any); ok && openAIInputItemIsReasoning(item) {
			return true
		}
	}
	return false
}

func openAIRawInputHasReasoningItem(input gjson.Result) bool {
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) == "reasoning" {
			found = true
			return false
		}
		return true
	})
	return found
}

// openAIRawReasoningIDIsStripped reports whether a raw reasoning item loses its
// id under the predefined id-shape rules used by
// sanitizeOpenAIResponsesInputItemIDs (empty, over-long, or wrong prefix), or
// whether an rs_* item_reference is dropped outright.
func openAIRawReasoningIDIsStripped(item gjson.Result) bool {
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType == "item_reference" {
		return strings.HasPrefix(strings.TrimSpace(item.Get("id").String()), "rs_")
	}
	if itemType != "reasoning" {
		return false
	}
	return shouldStripOpenAIResponsesInputItemID("reasoning", item.Get("id").String())
}

// openAIRawInputStripsAnyReasoningID reports whether the input contains a
// reasoning item whose id would be dropped by
// sanitizeOpenAIResponsesInputItemIDs, which is the exact condition that leaves
// a paired assistant message id dangling.
func openAIRawInputStripsAnyReasoningID(input gjson.Result) bool {
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if openAIRawReasoningIDIsStripped(item) {
			found = true
			return false
		}
		return true
	})
	return found
}

// openAIInputStoreFalseStripsReasoning reports whether the API-key store=false
// replay normalizer breaks a message/reasoning pair for the given decoded input:
// either a reasoning item is dropped for lacking usable encrypted content, or
// its rs_* id is stripped so a replayed id lookup does not 404, or a reasoning
// item_reference with an rs_* id is dropped entirely.
func openAIInputStoreFalseStripsReasoning(items []any) bool {
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(firstNonEmptyString(item["type"])) == "item_reference" {
			if strings.HasPrefix(strings.TrimSpace(firstNonEmptyString(item["id"])), "rs_") {
				return true
			}
			continue
		}
		if !openAIInputItemIsReasoning(item) {
			continue
		}
		encrypted, hasEncrypted := item["encrypted_content"].(string)
		if !hasEncrypted || strings.TrimSpace(encrypted) == "" {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(firstNonEmptyString(item["id"])), "rs_") {
			return true
		}
	}
	return false
}

// openAIRawInputStoreFalseStripsReasoning mirrors
// openAIInputStoreFalseStripsReasoning for the zero-copy raw replay path.
func openAIRawInputStoreFalseStripsReasoning(input gjson.Result) bool {
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "item_reference" {
			if strings.HasPrefix(strings.TrimSpace(item.Get("id").String()), "rs_") {
				found = true
				return false
			}
			return true
		}
		if itemType != "reasoning" {
			return true
		}
		encrypted := item.Get("encrypted_content")
		if encrypted.Type != gjson.String || strings.TrimSpace(encrypted.Str) == "" ||
			strings.HasPrefix(strings.TrimSpace(item.Get("id").String()), "rs_") {
			found = true
			return false
		}
		return true
	})
	return found
}

func stripOpenAIReasoningPairedIDsFromItems(items []any) bool {
	changed := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || !openAIReasoningPairedItem(item) {
			continue
		}
		delete(item, "id")
		changed = true
	}
	return changed
}

// stripOpenAIReasoningPairedIDs drops the id from reasoning-paired output items
// in a decoded request body. Callers invoke it after they have removed or
// rewritten reasoning item ids in the same input.
func stripOpenAIReasoningPairedIDs(reqBody map[string]any) bool {
	if len(reqBody) == 0 {
		return false
	}
	switch input := reqBody["input"].(type) {
	case []any:
		if !stripOpenAIReasoningPairedIDsFromItems(input) {
			return false
		}
		reqBody["input"] = input
		return true
	case []map[string]any:
		changed := false
		for i := range input {
			if openAIReasoningPairedItem(input[i]) {
				delete(input[i], "id")
				changed = true
			}
		}
		if !changed {
			return false
		}
		reqBody["input"] = input
		return true
	case map[string]any:
		if !openAIReasoningPairedItem(input) {
			return false
		}
		delete(input, "id")
		reqBody["input"] = input
		return true
	default:
		return false
	}
}
