package state

import (
	"math"
	"net/netip"
	"strconv"
	"strings"

	"github.com/juanfont/headscale/hscontrol/types"
	"tailscale.com/tailcfg"
)

type geoIPLookupCloser interface {
	LookupCountry(netip.Addr) (string, bool)
	Close() error
}

func derpRTTByRegion(netInfo *tailcfg.NetInfo) map[int]float64 {
	if netInfo == nil || len(netInfo.DERPLatency) == 0 {
		return nil
	}

	result := make(map[int]float64, len(netInfo.DERPLatency))
	for key, latency := range netInfo.DERPLatency {
		regionPart, _, _ := strings.Cut(key, "-")
		regionID, err := strconv.Atoi(regionPart)
		if err != nil || regionID <= 0 || latency <= 0 || math.IsNaN(latency) || math.IsInf(latency, 0) {
			continue
		}

		if previous, ok := result[regionID]; !ok || latency < previous {
			result[regionID] = latency
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

func (s *State) setRegionalRTT(id types.NodeID, netInfo *tailcfg.NetInfo) bool {
	if netInfo == nil {
		return false
	}

	updated := derpRTTByRegion(netInfo)
	s.regionalMu.Lock()
	defer s.regionalMu.Unlock()

	previous := s.regionalRTT[id]
	if mapsEqualFloat(previous, updated) {
		return false
	}
	if len(updated) == 0 {
		delete(s.regionalRTT, id)
	} else {
		s.regionalRTT[id] = updated
	}

	return true
}

func (s *State) clearRegionalRTT(id types.NodeID) {
	s.regionalMu.Lock()
	delete(s.regionalRTT, id)
	s.regionalMu.Unlock()
}

func (s *State) regionalRTTFor(id types.NodeID) map[int]float64 {
	s.regionalMu.RLock()
	defer s.regionalMu.RUnlock()

	current := s.regionalRTT[id]
	if len(current) == 0 {
		return nil
	}

	result := make(map[int]float64, len(current))
	for regionID, latency := range current {
		result[regionID] = latency
	}

	return result
}

func (s *State) derpCountry(regionID int) string {
	if s.geoIP == nil || regionID <= 0 {
		return ""
	}
	dm := s.derpMap.Load()
	if dm == nil {
		return ""
	}
	region := dm.Regions[regionID]
	if region == nil {
		return ""
	}

	for _, node := range region.Nodes {
		if node == nil {
			continue
		}
		for _, raw := range []string{node.IPv4, node.IPv6} {
			addr, err := netip.ParseAddr(raw)
			if err != nil || !isPublicAddr(addr) {
				continue
			}
			if country, ok := s.geoIP.LookupCountry(addr); ok {
				return country
			}
		}
	}

	return ""
}

func (s *State) endpointCountries(endpoints []netip.AddrPort) []string {
	if s.geoIP == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var countries []string
	for _, endpoint := range endpoints {
		addr := endpoint.Addr().Unmap()
		if !isPublicAddr(addr) {
			continue
		}
		country, ok := s.geoIP.LookupCountry(addr)
		if !ok {
			continue
		}
		country = strings.ToUpper(country)
		if _, exists := seen[country]; exists {
			continue
		}
		seen[country] = struct{}{}
		countries = append(countries, country)
	}

	return countries
}

func (s *State) clientCountry(viewer types.NodeView) string {
	return s.clientCountryWithRTT(viewer, s.regionalRTTFor(viewer.ID()))
}

func (s *State) clientCountryWithRTT(viewer types.NodeView, rtt map[int]float64) string {
	if s.geoIP == nil || !viewer.Valid() {
		return ""
	}
	countries := s.endpointCountries(viewer.Endpoints().AsSlice())
	if len(countries) == 1 {
		return countries[0]
	}

	hostinfo := viewer.Hostinfo()
	if !hostinfo.Valid() {
		return ""
	}
	netInfo := hostinfo.NetInfo()
	if len(countries) == 0 {
		if netInfo.Valid() {
			return s.derpCountry(int(netInfo.PreferredDERP()))
		}
		return ""
	}

	// Multiple public endpoints are unusual (for example, dual-WAN). Pick
	// the endpoint country whose DERP regions have the lowest reported RTT.
	bestCountry := s.lowestRTTCountry(countries, rtt)
	if bestCountry != "" {
		return bestCountry
	}
	if netInfo.Valid() {
		return s.derpCountry(int(netInfo.PreferredDERP()))
	}

	return ""
}

func (s *State) lowestRTTCountry(countries []string, rtt map[int]float64) string {
	bestCountry := ""
	bestRTT := math.Inf(1)
	for _, country := range countries {
		for regionID, latency := range rtt {
			if s.derpCountry(regionID) != country ||
				(latency > bestRTT || (latency == bestRTT && bestCountry != "" && country >= bestCountry)) {
				continue
			}
			bestCountry = country
			bestRTT = latency
		}
	}

	return bestCountry
}

func (s *State) routingRegion(viewer types.NodeView) string {
	return s.routingRegionWithRTT(viewer, s.regionalRTTFor(viewer.ID()))
}

func (s *State) routingRegionWithRTT(viewer types.NodeView, latency map[int]float64) string {
	if s.geoIP != nil {
		if country := s.clientCountryWithRTT(viewer, latency); country != "" {
			return country
		}
	}
	if !viewer.Valid() {
		return ""
	}
	bestRegion := 0
	bestRTT := math.Inf(1)
	for region, rtt := range latency {
		if rtt < bestRTT || (rtt == bestRTT && (bestRegion == 0 || region < bestRegion)) {
			bestRegion = region
			bestRTT = rtt
		}
	}
	if bestRegion != 0 {
		return derpRegionKey(bestRegion)
	}

	hostinfo := viewer.Hostinfo()
	if !hostinfo.Valid() {
		return ""
	}
	if netInfo := hostinfo.NetInfo(); netInfo.Valid() && netInfo.PreferredDERP() > 0 {
		return derpRegionKey(int(netInfo.PreferredDERP()))
	}

	return ""
}

func derpRegionKey(id int) string {
	return "DERP-" + strconv.Itoa(id)
}

func (s *State) routerCountry(router types.NodeView, rtt map[int]float64) string {
	if s.geoIP == nil || !router.Valid() {
		return ""
	}
	countries := s.endpointCountries(router.Endpoints().AsSlice())
	if len(countries) == 1 {
		return countries[0]
	}
	if len(countries) > 1 {
		if country := s.lowestRTTCountry(countries, rtt); country != "" {
			return country
		}
	}
	if country := s.derpCountry(nodeHomeDERP(router)); country != "" {
		return country
	}

	return ""
}

func (s *State) routerRoutingRegion(router types.NodeView, rtt map[int]float64) string {
	if s.geoIP != nil {
		if country := s.routerCountry(router, rtt); country != "" {
			return country
		}
	}
	if regionID := nodeHomeDERP(router); regionID > 0 {
		return derpRegionKey(regionID)
	}

	return ""
}

func nodeHomeDERP(node types.NodeView) int {
	if !node.Valid() {
		return 0
	}
	hostinfo := node.Hostinfo()
	if !hostinfo.Valid() {
		return 0
	}
	netInfo := hostinfo.NetInfo()
	if !netInfo.Valid() {
		return 0
	}

	return int(netInfo.PreferredDERP())
}

func mapsEqualFloat(a, b map[int]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}

	return true
}

func isPublicAddr(addr netip.Addr) bool {
	return addr.IsValid() && addr.IsGlobalUnicast() && !addr.IsPrivate() &&
		!addr.IsLoopback() && !addr.IsLinkLocalUnicast()
}
