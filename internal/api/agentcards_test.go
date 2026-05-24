package api_test

import (
	"encoding/json"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestAgentCardsListRepoEmpty covers the smoke case: an empty repo
// returns an empty AgentCard array, not null. The reshape on the web
// bundle relies on the array shape — a null would crash the .map().
func TestAgentCardsListRepoEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/agents/cards")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if string(body) == "null\n" || string(body) == "null" {
		t.Fatalf("expected [] not null: %s", body)
	}
	var cards []map[string]any
	if err := json.Unmarshal(body, &cards); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected 0 cards: %v", cards)
	}
}

// TestAgentCardsListAllEmpty mirrors the above for the cross-repo
// variant.
func TestAgentCardsListAllEmpty(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/agents/cards")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var cards []map[string]any
	if err := json.Unmarshal(body, &cards); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected 0 cards: %v", cards)
	}
}

// TestAgentCardsListRepoRegisteredOnly seeds a registered session and
// a stub (registered_at NULL), and confirms only the registered one
// surfaces — the cards endpoint inherits the RegisteredOnly filter
// from agentcards.Assemble.
func TestAgentCardsListRepoRegisteredOnly(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	// Registered session (MarkRegistered stamps registered_at).
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      "sess-registered",
		RepoID:         repo.ID,
		Actor:          "agent-claude",
		Model:          "claude-opus-4-7",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Stub session — MarkRegistered false leaves registered_at NULL.
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-stub",
		RepoID:    repo.ID,
		Actor:     "unregistered",
	}); err != nil {
		t.Fatalf("create stub: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/agents/cards")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var cards []map[string]any
	if err := json.Unmarshal(body, &cards); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("expected exactly 1 card (registered only): got %d (%v)", len(cards), cards)
	}
	if cards[0]["sessionId"] != "sess-registered" {
		t.Fatalf("wrong session surfaced: %v", cards[0])
	}
	// Spot-check derived camelCase fields that the web bundle reads.
	for _, key := range []string{"agentName", "actor", "status", "hasChannel", "lastSeenAt", "claims", "dispatches", "todos", "todosDone", "todosTotal"} {
		if _, ok := cards[0][key]; !ok {
			t.Fatalf("missing field %q in card: %v", key, cards[0])
		}
	}
}

// TestAgentCardsBusyDerivation hangs a claim off a registered session
// and confirms `busy`/`busyIssue` come back correct.
func TestAgentCardsBusyDerivation(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "test issue")

	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      "sess-busy",
		RepoID:         repo.ID,
		Actor:          "agent-claude",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("sess-busy", iss.ID, "do the thing"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/agents/cards")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var cards []map[string]any
	if err := json.Unmarshal(body, &cards); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("expected 1 card: %d (%v)", len(cards), cards)
	}
	card := cards[0]
	if busy, _ := card["busy"].(bool); !busy {
		t.Fatalf("expected busy=true: %v", card)
	}
	if got, _ := card["busyIssue"].(string); got != iss.Key {
		t.Fatalf("busyIssue: want %q got %q", iss.Key, got)
	}
	claims, _ := card["claims"].([]any)
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim: %v", card["claims"])
	}
}
