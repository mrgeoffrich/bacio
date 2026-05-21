package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/channel"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// AttachTranscript implements channel.Source. Given a completed
// subagent's agentID and an issue key, it locates the subagent's
// transcript under ~/.claude/projects/, renders a markdown digest, and
// links it to the issue as a project_complete document.
//
// Transcript location contract (Claude Code 2.1.x): every Task-spawned
// subagent's conversation is persisted at
//
//	~/.claude/projects/<project-slug>/<parent-session-id>/subagents/agent-<agentID>.jsonl
//
// with a sibling agent-<agentID>.meta.json carrying {agentType,
// description, ...}. The channel knows the project dir but NOT the
// parent session id, so the resolver globs across every session dir.
func (s *channelSource) AttachTranscript(ctx context.Context, issueKey, agentID, note string) (string, error) {
	if s.repo == nil {
		return "", fmt.Errorf("channel has no resolved repo — cannot attach transcript")
	}
	// Validate the issue belongs to this channel's repo before doing
	// any filesystem work.
	iss, err := s.c.GetIssueByKey(ctx, s.repo, issueKey)
	if err != nil {
		return "", fmt.Errorf("issue %s: %w", issueKey, err)
	}
	canonicalKey := iss.Key

	tr, err := locateSubagentTranscript(s.projectDir, agentID)
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(tr.path)
	if err != nil {
		return "", fmt.Errorf("read transcript %s: %w", tr.path, err)
	}

	meta := channel.TranscriptMeta{
		AgentID:         agentID,
		AgentType:       tr.agentType,
		Description:     tr.description,
		ParentSessionID: tr.parentSessionID,
		SourcePath:      tr.path,
		SizeBytes:       int64(len(raw)),
		Note:            note,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	digest := channel.RenderTranscriptDigest(raw, meta)

	filename := fmt.Sprintf("bacio-transcript-%s-agent-%s.md", canonicalKey, agentID)
	if _, err := s.c.UpsertDocument(ctx, s.repo, client.DocCreateInput{
		Filename:   filename,
		Type:       model.DocTypeProjectComplete,
		Body:       digest,
		SourcePath: tr.path,
	}, false); err != nil {
		return "", fmt.Errorf("store transcript digest as document %s: %w", filename, err)
	}

	linkDesc := "subagent transcript"
	if tr.agentType != "" {
		linkDesc = tr.agentType + " subagent transcript"
	}
	linkDesc += fmt.Sprintf(" (agent-%s)", agentID)
	if _, err := s.c.LinkDocument(ctx, s.repo, inputs.DocLinkInput{
		Filename:    filename,
		IssueKey:    canonicalKey,
		Description: linkDesc,
	}, false); err != nil {
		return "", fmt.Errorf("link transcript digest to %s: %w", canonicalKey, err)
	}

	return fmt.Sprintf("attached transcript agent-%s to %s as doc %s (%d bytes raw → %d byte digest)",
		agentID, canonicalKey, filename, len(raw), len(digest)), nil
}

// locatedTranscript is the result of resolving a subagent transcript
// file plus its meta sidecar.
type locatedTranscript struct {
	path            string // absolute path to the .jsonl
	parentSessionID string // the parent session dir the file was filed under
	agentType       string // from agent-<id>.meta.json, "" if absent
	description     string // from agent-<id>.meta.json, "" if absent
}

// locateSubagentTranscript finds the transcript .jsonl for agentID. It
// computes the ~/.claude/projects/ slug from projectDir, then globs
// every session dir under it for a subagents/agent-<agentID>.jsonl. It
// returns a clear, actionable error when nothing matches.
func locateSubagentTranscript(projectDir, agentID string) (locatedTranscript, error) {
	var zero locatedTranscript
	if strings.TrimSpace(projectDir) == "" {
		return zero, fmt.Errorf("channel has no resolved project dir — cannot locate subagent transcripts")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return zero, fmt.Errorf("resolve home dir: %w", err)
	}
	slug := claudeProjectSlug(projectDir)
	projRoot := filepath.Join(home, ".claude", "projects", slug)

	pattern := filepath.Join(projRoot, "*", "subagents", "agent-"+agentID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return zero, fmt.Errorf("glob transcripts: %w", err)
	}
	if len(matches) == 0 {
		return zero, fmt.Errorf(
			"no transcript found for agent-%s under %s — is the Claude Code harness recent enough "+
				"to persist per-subagent transcripts (2.1.x+), and did the subagent run in this project?",
			agentID, projRoot)
	}
	// >1 match (agentIds are unique, but be defensive): pick the newest.
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool {
			fi, _ := os.Stat(matches[i])
			fj, _ := os.Stat(matches[j])
			if fi == nil || fj == nil {
				return false
			}
			return fi.ModTime().After(fj.ModTime())
		})
	}
	path := matches[0]

	out := locatedTranscript{
		path:            path,
		parentSessionID: filepath.Base(filepath.Dir(filepath.Dir(path))),
	}
	// Read the sibling meta sidecar — best effort.
	metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
	if metaRaw, err := os.ReadFile(metaPath); err == nil {
		var m struct {
			AgentType   string `json:"agentType"`
			Description string `json:"description"`
		}
		if json.Unmarshal(metaRaw, &m) == nil {
			out.agentType = m.AgentType
			out.description = m.Description
		}
	}
	return out, nil
}

// claudeProjectSlug derives the ~/.claude/projects/ directory name from
// an absolute project path. Claude Code slugifies the path by replacing
// every '/' and '.' with '-' (e.g. "/Users/geoff/Repos/bacio" ->
// "-Users-geoff-Repos-bacio"). Verified against observed project dirs.
func claudeProjectSlug(projectDir string) string {
	r := strings.NewReplacer("/", "-", ".", "-")
	return r.Replace(projectDir)
}

// compile-time assertion that channelSource satisfies channel.Source.
var _ channel.Source = (*channelSource)(nil)
