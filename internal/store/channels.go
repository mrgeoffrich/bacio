package store

import (
	"database/sql"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// UpsertAgentChannelIn is the validated tuple UpsertAgentChannel
// consumes. A channel keys itself on (Host, ClaudePID) — the `claude`
// process it descends from — because Claude Code never tells it the
// session id.
type UpsertAgentChannelIn struct {
	RepoID     int64
	AgentID    *int64
	Host       string
	ClaudePID  int64
	ChannelPID int64
}

// UpsertAgentChannel records (or heartbeats) a live `bacio channel`
// subprocess. Keyed on (host, claude_pid): re-running just bumps
// last_seen_at and refreshes the mutable fields, so the channel's poll
// loop can call this every tick. repo_id / agent_id are mutable on
// update because the channel re-resolves its identity each poll — a
// rename mid-life is picked up here.
func (s *Store) UpsertAgentChannel(in UpsertAgentChannelIn) error {
	var agentID any
	if in.AgentID != nil {
		agentID = *in.AgentID
	}
	_, err := s.DB.Exec(`
		INSERT INTO agent_channels
		    (repo_id, agent_id, host, claude_pid, channel_pid, last_seen_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(host, claude_pid) DO UPDATE SET
		    repo_id      = excluded.repo_id,
		    agent_id     = excluded.agent_id,
		    channel_pid  = excluded.channel_pid,
		    last_seen_at = CURRENT_TIMESTAMP`,
		in.RepoID, agentID, in.Host, in.ClaudePID, in.ChannelPID,
	)
	return err
}

// LinkSessionChannel stamps claude_pid onto a session and lights up
// channel_seen_at IFF a live agent_channels row matches (host,
// claude_pid). It's the hook side of the channel<->session join: the
// hook knows the session id and walks the process tree to the same
// `claude` pid the channel recorded. claude_pid is always written (it's
// cheap and keeps the row current); channel_seen_at is only advanced
// when there's a fresh channel to point at, so it freshness-decays on
// its own once the channel dies. A claudePID of 0 (hook couldn't walk
// to `claude`) matches nothing — channel_seen_at is left untouched.
func (s *Store) LinkSessionChannel(sessionID string, claudePID int64, host string) error {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return err
	}
	freshCutoff := time.Now().Add(-model.AgentLivenessThreshold).UTC().Format("2006-01-02 15:04:05")
	_, err := s.DB.Exec(`
		UPDATE agent_sessions
		SET claude_pid = ?,
		    channel_seen_at = CASE
		        WHEN EXISTS (
		            SELECT 1 FROM agent_channels c
		            WHERE c.host = ? AND c.claude_pid = ? AND c.last_seen_at >= ?
		        ) THEN CURRENT_TIMESTAMP
		        ELSE channel_seen_at
		    END
		WHERE session_id = ?`,
		claudePID, host, claudePID, freshCutoff, sessionID,
	)
	return err
}

// pruneAgentChannels drops agent_channels rows whose heartbeat went
// stale (older than AgentLivenessThreshold). Unlike sessions/history,
// channel rows carry no historical value — they're pure liveness state
// — so the cutoff is the liveness window, not a 60-day retention. A
// live channel re-heartbeats every few seconds and is never at risk.
func pruneAgentChannels(db *sql.DB) error {
	cutoff := time.Now().Add(-model.AgentLivenessThreshold).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`DELETE FROM agent_channels WHERE last_seen_at < ?`, cutoff)
	return err
}
