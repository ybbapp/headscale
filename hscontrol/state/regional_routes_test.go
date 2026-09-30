package state

import (
	"testing"

	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/stretchr/testify/require"
)

func TestSelectRegionalRoute(t *testing.T) {
	tests := []struct {
		name              string
		viewerCountry     string
		candidates        []RegionalRouteCandidate
		latencyByRegion   map[int]float64
		previous          types.NodeID
		fallbackCountries []string
		want              types.NodeID
		wantOK            bool
	}{
		{
			name:          "same country beats lower RTT foreign candidate",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
				{NodeID: 2, Country: "SG", HomeDERP: 2, Healthy: true},
			},
			latencyByRegion: map[int]float64{1: 0.050, 2: 0.010},
			want:            1,
			wantOK:          true,
		},
		{
			name:          "lowest RTT wins within same country",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
				{NodeID: 2, Country: "DE", HomeDERP: 2, Healthy: true},
			},
			latencyByRegion: map[int]float64{1: 0.020, 2: 0.010},
			previous:        1,
			want:            2,
			wantOK:          true,
		},
		{
			name:          "previous candidate breaks equal RTT tie",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
				{NodeID: 2, Country: "DE", HomeDERP: 2, Healthy: true},
			},
			latencyByRegion: map[int]float64{1: 0.010, 2: 0.010},
			previous:        2,
			want:            2,
			wantOK:          true,
		},
		{
			name:          "lowest node ID breaks RTT tie without previous",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 2, Country: "DE", HomeDERP: 2, Healthy: true},
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
			},
			latencyByRegion: map[int]float64{1: 0.010, 2: 0.010},
			want:            1,
			wantOK:          true,
		},
		{
			name:          "previous healthy candidate is kept when local RTT is unavailable",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
				{NodeID: 2, Country: "DE", HomeDERP: 2, Healthy: true},
			},
			previous: 2,
			want:     2,
			wantOK:   true,
		},
		{
			name:          "lowest node ID is deterministic when local RTT is unavailable",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 2, Country: "DE", HomeDERP: 2, Healthy: true},
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
			},
			want:   1,
			wantOK: true,
		},
		{
			name:          "configured fallback country order precedes RTT",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "FR", HomeDERP: 1, Healthy: true},
				{NodeID: 2, Country: "NL", HomeDERP: 2, Healthy: true},
			},
			latencyByRegion:   map[int]float64{1: 0.050, 2: 0.010},
			fallbackCountries: []string{"FR", "NL"},
			want:              1,
			wantOK:            true,
		},
		{
			name:          "lowest RTT country is used when no configured fallback matches",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "FR", HomeDERP: 1, Healthy: true},
				{NodeID: 2, Country: "NL", HomeDERP: 2, Healthy: true},
			},
			latencyByRegion: map[int]float64{1: 0.050, 2: 0.010},
			want:            2,
			wantOK:          true,
		},
		{
			name:          "unhealthy local candidate is not selected",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: false},
				{NodeID: 2, Country: "FR", HomeDERP: 2, Healthy: true},
			},
			latencyByRegion: map[int]float64{1: 0.010, 2: 0.020},
			want:            2,
			wantOK:          true,
		},
		{
			name:          "unknown viewer country leaves global fallback to caller",
			viewerCountry: "",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: true},
			},
			wantOK: false,
		},
		{
			name:          "no healthy candidates leaves global fallback to caller",
			viewerCountry: "DE",
			candidates: []RegionalRouteCandidate{
				{NodeID: 1, Country: "DE", HomeDERP: 1, Healthy: false},
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SelectRegionalRoute(
				tt.viewerCountry,
				tt.candidates,
				tt.latencyByRegion,
				tt.previous,
				tt.fallbackCountries,
			)

			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.want, got)
			}
		})
	}
}
