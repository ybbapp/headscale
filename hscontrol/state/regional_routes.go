package state

import (
	"math"
	"slices"
	"strings"

	"github.com/juanfont/headscale/hscontrol/types"
)

// RegionalRouteCandidate is an online, approved advertiser for the route being
// selected. Candidates are scoped to one route by the caller.
type RegionalRouteCandidate struct {
	NodeID   types.NodeID
	Country  string
	HomeDERP int
	Healthy  bool
}

// SelectRegionalRoute selects a healthy advertiser for a viewer. An empty
// viewerCountry or an empty healthy candidate set returns false so the caller
// can retain its existing global-primary behavior.
//
// RTT values are seconds, keyed by DERP region ID. Country matching is
// case-insensitive; country values are expected to be ISO country codes.
func SelectRegionalRoute(
	viewerCountry string,
	candidates []RegionalRouteCandidate,
	derpLatencyByRegion map[int]float64,
	previous types.NodeID,
	fallbackCountries []string,
) (types.NodeID, bool) {
	if strings.TrimSpace(viewerCountry) == "" {
		return 0, false
	}

	byCountry := make(map[string][]RegionalRouteCandidate)
	for _, candidate := range candidates {
		if !candidate.Healthy || candidate.NodeID == 0 || strings.TrimSpace(candidate.Country) == "" {
			continue
		}

		country := strings.ToUpper(strings.TrimSpace(candidate.Country))
		byCountry[country] = append(byCountry[country], candidate)
	}

	if len(byCountry) == 0 {
		return 0, false
	}

	viewerCountry = strings.ToUpper(strings.TrimSpace(viewerCountry))
	if local := byCountry[viewerCountry]; len(local) > 0 {
		return selectCandidateInCountry(local, derpLatencyByRegion, previous), true
	}

	seen := make(map[string]struct{}, len(fallbackCountries))
	for _, country := range fallbackCountries {
		country = strings.ToUpper(strings.TrimSpace(country))
		if country == "" {
			continue
		}
		if _, ok := seen[country]; ok {
			continue
		}
		seen[country] = struct{}{}

		if fallback := byCountry[country]; len(fallback) > 0 {
			return selectCandidateInCountry(fallback, derpLatencyByRegion, previous), true
		}
	}

	return selectFallbackCountry(byCountry, derpLatencyByRegion, previous), true
}

func selectCandidateInCountry(
	candidates []RegionalRouteCandidate,
	derpLatencyByRegion map[int]float64,
	previous types.NodeID,
) types.NodeID {
	var (
		bestID  types.NodeID
		bestRTT float64
		hasRTT  bool
	)

	for _, candidate := range candidates {
		rtt, ok := regionalRouteRTT(candidate, derpLatencyByRegion)
		if !ok {
			continue
		}

		if !hasRTT || rtt < bestRTT || (rtt == bestRTT && candidate.NodeID < bestID) {
			bestID = candidate.NodeID
			bestRTT = rtt
			hasRTT = true
		}
	}

	if hasRTT {
		for _, candidate := range candidates {
			if candidate.NodeID == previous {
				rtt, ok := regionalRouteRTT(candidate, derpLatencyByRegion)
				if ok && rtt == bestRTT {
					return previous
				}
			}
		}

		return bestID
	}

	return previousOrLowestNodeID(candidates, previous)
}

func selectFallbackCountry(
	byCountry map[string][]RegionalRouteCandidate,
	derpLatencyByRegion map[int]float64,
	previous types.NodeID,
) types.NodeID {
	var (
		bestID      types.NodeID
		bestCountry string
		bestRTT     float64
		hasRTT      bool
	)

	countries := make([]string, 0, len(byCountry))
	for country := range byCountry {
		countries = append(countries, country)
	}
	slices.Sort(countries)

	for _, country := range countries {
		for _, candidate := range byCountry[country] {
			rtt, ok := regionalRouteRTT(candidate, derpLatencyByRegion)
			if !ok {
				continue
			}

			if !hasRTT || rtt < bestRTT ||
				(rtt == bestRTT && (country < bestCountry ||
					(country == bestCountry && candidate.NodeID < bestID))) {
				bestID = candidate.NodeID
				bestCountry = country
				bestRTT = rtt
				hasRTT = true
			}
		}
	}

	if hasRTT {
		return bestID
	}

	all := make([]RegionalRouteCandidate, 0)
	for _, country := range countries {
		all = append(all, byCountry[country]...)
	}

	return previousOrLowestNodeID(all, previous)
}

func previousOrLowestNodeID(candidates []RegionalRouteCandidate, previous types.NodeID) types.NodeID {
	lowest := types.NodeID(0)
	for _, candidate := range candidates {
		if candidate.NodeID == previous {
			return previous
		}
		if lowest == 0 || candidate.NodeID < lowest {
			lowest = candidate.NodeID
		}
	}

	return lowest
}

func regionalRouteRTT(
	candidate RegionalRouteCandidate,
	derpLatencyByRegion map[int]float64,
) (float64, bool) {
	if candidate.HomeDERP <= 0 {
		return 0, false
	}

	rtt, ok := derpLatencyByRegion[candidate.HomeDERP]
	if !ok || rtt <= 0 || math.IsNaN(rtt) || math.IsInf(rtt, 0) {
		return 0, false
	}

	return rtt, true
}
