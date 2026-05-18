// Per-issue claimant DTO + mapper. Lives alongside the per-session DTOs
// in this package because both are camelCase per-row reshapings of
// model.AgentClaim consumed by the desktop's Wails-bound surface — same
// vocabulary, same package, same naming.
//
// The api keeps emitting []*model.AgentClaim raw (snake_case) on its
// /issues/{key} and /brief endpoints — a deliberately different surface,
// not a duplication of this DTO. If the REST surface ever switches to
// camelCase DTOs the call site moves; the shape is the same.

package agentcards

import (
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// ClaimantDTO is the per-issue claim-history entry the desktop's
// IssueDetail surfaces. CamelCase JSON tags match the rest of the
// agentcards DTOs (BACI-50). Open is the derived
// model.AnyOpenClaim-style flag for a single row — true iff ReleasedAt
// is nil.
type ClaimantDTO struct {
	SessionID  string     `json:"sessionId"`
	AgentName  string     `json:"agentName"`
	Prompt     string     `json:"prompt"`
	ClaimedAt  time.Time  `json:"claimedAt"`
	ReleasedAt *time.Time `json:"releasedAt"`
	Open       bool       `json:"open"`
}

// MapClaimants converts the raw claim history (newest first, open +
// released) into the desktop-shaped DTO with the derived Open flag set
// per row. Nil entries in the slice are skipped — defensive, matches
// model.AnyOpenClaim's tolerance.
func MapClaimants(claims []*model.AgentClaim) []ClaimantDTO {
	out := make([]ClaimantDTO, 0, len(claims))
	for _, c := range claims {
		if c == nil {
			continue
		}
		out = append(out, ClaimantDTO{
			SessionID:  c.SessionID,
			AgentName:  c.AgentName,
			Prompt:     c.Prompt,
			ClaimedAt:  c.ClaimedAt,
			ReleasedAt: c.ReleasedAt,
			Open:       c.ReleasedAt == nil,
		})
	}
	return out
}
