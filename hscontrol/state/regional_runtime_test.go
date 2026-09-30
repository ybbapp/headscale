package state

import (
	"net/netip"
	"testing"

	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
)

type fakeCountryLookup map[netip.Addr]string

func (f fakeCountryLookup) LookupCountry(addr netip.Addr) (string, bool) {
	country, ok := f[addr.Unmap()]
	return country, ok
}

func (fakeCountryLookup) Close() error { return nil }

func TestRegionalPrimaryRoutes(t *testing.T) {
	t.Run("same country wins when foreign RTT is lower", func(t *testing.T) {
		state, viewer := regionalRuntimeFixture(t,
			map[netip.Addr]string{
				netip.MustParseAddr("198.51.100.10"): "DE",
				netip.MustParseAddr("203.0.113.10"):  "SG",
				netip.MustParseAddr("8.8.8.8"):       "DE",
				netip.MustParseAddr("1.1.1.1"):       "SG",
			},
			map[int]*tailcfg.DERPRegion{
				1: regionalTestDERPRegion(1, "8.8.8.8"),
				2: regionalTestDERPRegion(2, "1.1.1.1"),
			},
			[]regionalTestNode{
				{id: 1, countryIP: "1.1.1.1", derp: 2},
				{id: 2, countryIP: "8.8.8.8", derp: 1},
			},
			[]netip.AddrPort{netip.MustParseAddrPort("198.51.100.10:41641")},
		)
		state.setRegionalRTT(viewer.ID(), &tailcfg.NetInfo{DERPLatency: map[string]float64{
			"1-v4": 0.050,
			"2-v4": 0.005,
		}})

		got := state.RegionalPrimaryRoutesForViewer(viewer)
		require.Equal(t, types.NodeID(2), got[netip.MustParsePrefix("10.20.0.0/16")])
	})

	t.Run("same country selects lower HomeDERP RTT", func(t *testing.T) {
		state, viewer := regionalRuntimeFixture(t,
			map[netip.Addr]string{
				netip.MustParseAddr("198.51.100.10"): "DE",
				netip.MustParseAddr("8.8.8.8"):       "DE",
				netip.MustParseAddr("8.8.4.4"):       "DE",
			},
			map[int]*tailcfg.DERPRegion{
				1: regionalTestDERPRegion(1, "8.8.8.8"),
				2: regionalTestDERPRegion(2, "8.8.4.4"),
			},
			[]regionalTestNode{
				{id: 1, countryIP: "8.8.8.8", derp: 1},
				{id: 2, countryIP: "8.8.4.4", derp: 2},
			},
			[]netip.AddrPort{netip.MustParseAddrPort("198.51.100.10:41641")},
		)
		state.setRegionalRTT(viewer.ID(), &tailcfg.NetInfo{DERPLatency: map[string]float64{
			"1-v4": 0.020,
			"2-v4": 0.010,
		}})

		got := state.RegionalPrimaryRoutesForViewer(viewer)
		require.Equal(t, types.NodeID(2), got[netip.MustParsePrefix("10.20.0.0/16")])
	})

	t.Run("endpoint country conflict uses lowest RTT geolocated DERP country", func(t *testing.T) {
		state, viewer := regionalRuntimeFixture(t,
			map[netip.Addr]string{
				netip.MustParseAddr("198.51.100.10"): "DE",
				netip.MustParseAddr("203.0.113.10"):  "US",
				netip.MustParseAddr("8.8.8.8"):       "DE",
				netip.MustParseAddr("1.1.1.1"):       "US",
			},
			map[int]*tailcfg.DERPRegion{
				1: regionalTestDERPRegion(1, "8.8.8.8"),
				2: regionalTestDERPRegion(2, "1.1.1.1"),
			},
			[]regionalTestNode{
				{id: 1, countryIP: "8.8.8.8", derp: 1},
				{id: 2, countryIP: "1.1.1.1", derp: 2},
			},
			[]netip.AddrPort{
				netip.MustParseAddrPort("198.51.100.10:41641"),
				netip.MustParseAddrPort("203.0.113.10:41641"),
			},
		)
		state.setRegionalRTT(viewer.ID(), &tailcfg.NetInfo{DERPLatency: map[string]float64{
			"1-v4": 0.040,
			"2-v4": 0.010,
		}})

		got := state.RegionalPrimaryRoutesForViewer(viewer)
		require.Equal(t, types.NodeID(2), got[netip.MustParsePrefix("10.20.0.0/16")])
	})
}

type regionalTestNode struct {
	id        types.NodeID
	countryIP string
	derp      int
}

func regionalRuntimeFixture(
	t *testing.T,
	countries map[netip.Addr]string,
	derpRegions map[int]*tailcfg.DERPRegion,
	routers []regionalTestNode,
	viewerEndpoints []netip.AddrPort,
) (*State, types.NodeView) {
	t.Helper()

	const prefix = "10.20.0.0/16"
	route := netip.MustParsePrefix(prefix)
	viewer := types.Node{
		ID:        100,
		Hostname:  "viewer",
		GivenName: "viewer",
		Endpoints: viewerEndpoints,
		Hostinfo: &tailcfg.Hostinfo{
			Hostname: "viewer",
			NetInfo:  &tailcfg.NetInfo{PreferredDERP: 1},
		},
	}

	nodes := types.Nodes{&viewer}
	for _, candidate := range routers {
		ip := netip.MustParseAddr(candidate.countryIP)
		nodes = append(nodes, &types.Node{
			ID:             candidate.id,
			Hostname:       "router-" + candidate.id.String(),
			GivenName:      "router-" + candidate.id.String(),
			Endpoints:      []netip.AddrPort{netip.AddrPortFrom(ip, 41641)},
			IsOnline:       new(true),
			Hostinfo:       &tailcfg.Hostinfo{RoutableIPs: []netip.Prefix{route}, NetInfo: &tailcfg.NetInfo{PreferredDERP: candidate.derp}},
			ApprovedRoutes: []netip.Prefix{route},
		})
	}

	store := NewNodeStore(nodes, allowAllPeersFunc, TestBatchSize, TestBatchTimeout)
	store.Start()
	t.Cleanup(store.Stop)

	state := &State{
		cfg:         &types.Config{},
		nodeStore:   store,
		geoIP:       fakeCountryLookup(countries),
		regionalRTT: make(map[types.NodeID]map[int]float64),
	}
	state.cfg.Node.Routes.RegionalRouting.FallbackRegions = map[string][]string{}
	state.SetDERPMap(&tailcfg.DERPMap{Regions: derpRegions})

	nodeView, ok := store.GetNode(viewer.ID)
	require.True(t, ok)

	return state, nodeView
}

func regionalTestDERPRegion(id int, ip string) *tailcfg.DERPRegion {
	return &tailcfg.DERPRegion{
		RegionID: id,
		Nodes:    []*tailcfg.DERPNode{{Name: "derp", RegionID: id, IPv4: ip}},
	}
}
