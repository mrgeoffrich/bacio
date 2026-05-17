package dispatcher

import "testing"

func TestAutoPickFreeAgent(t *testing.T) {
	cases := []struct {
		name  string
		cands []AgentCandidate
		want  string
	}{
		{
			name:  "no candidates",
			cands: nil,
			want:  "",
		},
		{
			name: "first eligible wins",
			cands: []AgentCandidate{
				{AgentName: "otter", HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "otter",
		},
		{
			name: "skip ended",
			cands: []AgentCandidate{
				{AgentName: "otter", Ended: true, HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip busy",
			cands: []AgentCandidate{
				{AgentName: "otter", Busy: true, HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip no identity slug",
			cands: []AgentCandidate{
				{AgentName: "", HasChannel: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip no channel",
			cands: []AgentCandidate{
				{AgentName: "otter"},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "skip already-occupied (open dispatch un-acked)",
			cands: []AgentCandidate{
				{AgentName: "otter", HasChannel: true, HasOpenDispatch: true},
				{AgentName: "viper", HasChannel: true},
			},
			want: "viper",
		},
		{
			name: "everyone occupied yields no pick",
			cands: []AgentCandidate{
				{AgentName: "otter", HasChannel: true, HasOpenDispatch: true},
				{AgentName: "viper", HasChannel: true, Busy: true},
				{AgentName: "heron", Ended: true, HasChannel: true},
			},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AutoPickFreeAgent(c.cands); got != c.want {
				t.Errorf("AutoPickFreeAgent() = %q, want %q", got, c.want)
			}
		})
	}
}
