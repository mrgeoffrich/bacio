package client

import "testing"

func TestAutoPickFreeAgent(t *testing.T) {
	cases := []struct {
		name  string
		cands []agentCandidate
		want  string
	}{
		{
			name:  "no candidates",
			cands: nil,
			want:  "",
		},
		{
			name: "first eligible wins",
			cands: []agentCandidate{
				{AgentName: "otter", HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "otter",
		},
		{
			name: "skip ended",
			cands: []agentCandidate{
				{AgentName: "otter", Ended: true, HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip busy",
			cands: []agentCandidate{
				{AgentName: "otter", Busy: true, HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip no identity slug",
			cands: []agentCandidate{
				{AgentName: "", HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip no channel",
			cands: []agentCandidate{
				{AgentName: "otter"},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip already-occupied (open dispatch un-acked)",
			cands: []agentCandidate{
				{AgentName: "otter", HasChannel: true, HasOpenDispatch: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "everyone occupied yields no pick",
			cands: []agentCandidate{
				{AgentName: "otter", HasChannel: true, HasOpenDispatch: true},
				{AgentName: "viper", HasChannel: true, Busy: true},
				{AgentName: "heron", Ended: true, HasChannel: true},
			},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := autoPickFreeAgent(c.cands); got != c.want {
				t.Errorf("autoPickFreeAgent() = %q, want %q", got, c.want)
			}
		})
	}
}
