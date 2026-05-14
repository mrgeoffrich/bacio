package main

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

func TestPickFreeAgent(t *testing.T) {
	pending := DispatchDTO{Status: string(model.DispatchPending)}
	delivered := DispatchDTO{Status: string(model.DispatchDelivered)}
	acked := DispatchDTO{Status: string(model.DispatchAcked)}

	cases := []struct {
		name  string
		cards []AgentCard
		want  string
	}{
		{
			name:  "no agents",
			cards: nil,
			want:  "",
		},
		{
			name: "first idle agent wins",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active"},
				{AgentName: "viper", Status: "idle"},
			},
			want: "otter",
		},
		{
			name: "skip ended",
			cards: []AgentCard{
				{AgentName: "otter", Status: "ended"},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip busy (open claim)",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Busy: true},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip agent with no identity slug",
			cards: []AgentCard{
				{AgentName: "", Status: "active"},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip agent with pending dispatch already queued",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{pending}},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip agent with delivered-but-unacked dispatch",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{delivered}},
				{AgentName: "viper", Status: "idle"},
			},
			want: "viper",
		},
		{
			name: "acked dispatch does not occupy the agent",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{acked}},
				{AgentName: "viper", Status: "idle"},
			},
			want: "otter",
		},
		{
			name: "everyone occupied yields no pick",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{delivered}},
				{AgentName: "viper", Status: "active", Busy: true},
				{AgentName: "heron", Status: "ended"},
			},
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickFreeAgent(c.cards); got != c.want {
				t.Errorf("pickFreeAgent() = %q, want %q", got, c.want)
			}
		})
	}
}
