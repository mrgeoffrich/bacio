package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddUserMessage POSTs a BACI-286 steer message at a session over REST —
// the one user→agent message verb with HTTP parity, so a remote-mode
// desktop / web UI can steer a worker against a server holding the local
// store. Mirrors the endpoint the in-process surfaces hit.
func (c *remoteClient) AddUserMessage(ctx context.Context, in AddUserMessageInput) (*model.UserMessage, error) {
	body := map[string]any{"body": in.Body}
	var out model.UserMessage
	if err := c.do(ctx, http.MethodPost,
		"/agents/sessions/"+url.PathEscape(in.SessionID)+"/messages", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DrainUserMessagesForSession is the channel's push path — it talks to
// the local store directly (it doesn't go through --remote), so this is
// genuinely not needed over HTTP. Local-only, matching the question
// drains.
func (c *remoteClient) DrainUserMessagesForSession(ctx context.Context, sessionID string) ([]*model.UserMessage, error) {
	return nil, remoteAgentNotSupported("messages")
}
