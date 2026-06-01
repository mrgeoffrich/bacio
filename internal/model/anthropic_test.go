package model

import (
	"encoding/json"
	"testing"
)

// TestAnthropicMessage_UnmarshalContentForms locks the BACI-324 fix: a message's
// `content` may arrive as either the block-list form or the string shorthand the
// /v1/messages API accepts (the Claude client emits the string form for plain
// user turns). Both must decode; a string normalises to a single text block so
// every downstream consumer sees the uniform block-list shape.
func TestAnthropicMessage_UnmarshalContentForms(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantRole  string
		wantBlock []AnthropicBlock
	}{
		{
			name:      "string shorthand normalises to one text block",
			in:        `{"role":"user","content":"hello world"}`,
			wantRole:  "user",
			wantBlock: []AnthropicBlock{{Type: "text", Text: "hello world"}},
		},
		{
			name:      "block list decodes verbatim",
			in:        `{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`,
			wantRole:  "user",
			wantBlock: []AnthropicBlock{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}},
		},
		{
			name:      "empty-string shorthand is a single empty text block",
			in:        `{"role":"assistant","content":""}`,
			wantRole:  "assistant",
			wantBlock: []AnthropicBlock{{Type: "text", Text: ""}},
		},
		{
			name:      "null content yields no blocks",
			in:        `{"role":"user","content":null}`,
			wantRole:  "user",
			wantBlock: nil,
		},
		{
			name:      "missing content yields no blocks",
			in:        `{"role":"user"}`,
			wantRole:  "user",
			wantBlock: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m AnthropicMessage
			if err := json.Unmarshal([]byte(tc.in), &m); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if m.Role != tc.wantRole {
				t.Errorf("role = %q, want %q", m.Role, tc.wantRole)
			}
			if len(m.Content) != len(tc.wantBlock) {
				t.Fatalf("content = %+v, want %+v", m.Content, tc.wantBlock)
			}
			for i, want := range tc.wantBlock {
				if m.Content[i].Type != want.Type || m.Content[i].Text != want.Text {
					t.Errorf("block %d = %+v, want %+v", i, m.Content[i], want)
				}
			}
		})
	}
}

// TestAnthropicMessage_StringContentRoundTrips confirms a string-form content
// decodes to a block and re-marshals to the block-list form — so the persisted
// delta_json and the React viewer (which expect block lists) stay consistent.
func TestAnthropicMessage_StringContentRoundTrips(t *testing.T) {
	var m AnthropicMessage
	if err := json.Unmarshal([]byte(`{"role":"user","content":"ping"}`), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"role":"user","content":[{"type":"text","text":"ping"}]}`; string(out) != want {
		t.Errorf("re-marshal = %s, want %s", out, want)
	}
}
