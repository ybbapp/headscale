# Regional Routing Implementation Plan

## Objective

Implement P0 Regional Routing for overlapping subnet routes on the `v0.29.4-region` branch. A client should use a router in its own region while that region has a healthy eligible router; cross-region fallback is for local-region unavailability. P1 Recommended Exit Node is out of scope.

The routing feature is active by default for overlapping approved subnet prefixes. The default region signal is DERP RTT. Enabling GeoIP automatically changes client-region selection to GeoIP affinity. In RTT mode, use client-to-DERP-region latency as a region proxy; Tailscale does not report client-to-subnet-router RTT.

## Status

P0 implementation is present in the working tree. P1 Recommended Exit Node remains out of scope. Targeted GeoIP, configuration, state-selection, MapRequest RTT, and mapper tests pass. No release tag has been created and no beta host has been deployed; the user will perform real-device acceptance.

## Configuration and Defaults

Every added field must have a default so existing config files continue to load without edits.

- `geoip.enabled`: `false`.
- `geoip.database_path`: `/var/lib/headscale/GeoLite2-Country.mmdb`.
- `geoip.source_url`: `https://raw.githubusercontent.com/P3TERX/GeoLite.mmdb/download/GeoLite2-Country.mmdb`.
- `geoip.update_interval`: `72h`, matching the selected mirror's current three-day workflow cadence.
- Regional routing's default client-region source is minimum measured DERP RTT, with `PreferredDERP` as fallback; when `geoip.enabled` is true, GeoIP takes precedence automatically. GeoIP lookup failure or an unmapped country falls back to DERP RTT; if the region remains unknown, use the current global primary.
- With GeoIP enabled, use public endpoint country when it resolves consistently. If multiple public endpoints resolve to different countries, select the country with the lowest client-reported RTT among DERP regions whose public IP maps to that country; if no such RTT is available, fall back to the client's `PreferredDERP` country. Router public endpoint country takes priority; for conflicting endpoint countries use the viewer's lowest RTT among those countries, then fall back to the router's `HomeDERP` country.
- `node.routes.regional_routing.fallback_regions` defaults to an empty map of viewer country/region to ordered fallback countries/regions. GeoIP mode uses ISO country codes; RTT mode uses `DERP-<region-id>` keys and values. No client region is encoded as an ACL ownership tag.
- A healthy router in the client's selected country/region always wins, regardless of a lower RTT elsewhere. RTT ranks candidates only within that selected country/region. If it has no healthy router, explicit `fallback_regions` order overrides the default fallback to the healthy advertised region with the lowest DERP RTT.

Use only online, healthy nodes that currently announce and have approval for the same non-exit subnet prefix. Non-overlapping prefixes retain the existing route behavior.

## Implementation

1. Add a small GeoIP reader/updater component in new files. It fetches the configured MMDB URL on startup when GeoIP is enabled and then at the configured interval. Download to a temporary file, validate that it opens as MMDB, and atomically replace the active reader only after validation. On download or parse failure, keep the last good database; if none exists, continue with RTT-based selection instead of failing server startup, log the fallback and retry with exponential backoff from 5 minutes up to 6 hours until a valid database is available.
2. Resolve each node's region from its public `Endpoints`; use the public IPs in the node's `PreferredDERP`/`HomeDERP` region as fallback. Do not persist or log raw IPs or GeoIP results.
3. Keep the latest per-node DERP RTT measurements in memory only. Update them from MapRequest without treating ordinary latency jitter as a database or peer-visible Hostinfo change; when the viewer's selected regional route changes, enqueue a targeted full map update.
4. Keep the current global primary snapshot as compatibility fallback. For overlapping prefixes, select a healthy router in the viewer's country/DERP region first. Within that region choose the lowest measured DERP RTT; when RTT is unavailable preserve a healthy previous choice, otherwise choose the lowest NodeID. Only when no healthy local candidate exists, use configured ordered fallback regions, then the healthy region with the lowest RTT.
5. Pass the viewer region into mapper route construction. For each viewer, place the subnet prefix in `AllowedIPs` / `PrimaryRoutes` on the selected regional router and send full peer-node updates when route ownership changes; `PeerChange` patches do not carry route fields.
6. Keep changes narrow: add a GeoIP package and focused regional-routing tests, then make the smallest required changes to config parsing/defaults, runtime route state, MapRequest/session handling, and map-response construction. Do not add database migrations.

## Tests and Acceptance

- Config tests confirm old configs load with defaults and GeoIP defaults off.
- GeoIP unit tests cover country lookup for public addresses, private/invalid/unknown IP rejection, valid database replacement, and failed update preserving the last good reader.
- Route-election tests cover healthy previous primary, lower-NodeID healthy replacement, all-unhealthy behavior, prefix withdrawal, and per-region fallback.
- Map tests prove EU and Asia viewers receive the same subnet prefix through their own healthy regional router, a healthy local router wins even when another region's DERP RTT is lower, and same-country candidates are ranked by DERP RTT. Endpoint country conflict uses the lowest-RTT associated DERP country.
- Regression tests prove non-overlapping routes and unknown-region viewers retain global-primary behavior.
- No schema migration or persisted client source-IP field is introduced.
- Build the beta image by tagging the implementation commit `v0.29.4-region` and using the existing wildcard tag-triggered GitHub Actions release workflow. Confirm the GHCR image tag `ghcr.io/ybbapp/headscale:v0.29.4-region`; do not deploy it to the beta host. The user performs final real-device beta acceptance.

## Source Notes

- Selected GeoIP upstream: [P3TERX/GeoLite.mmdb](https://github.com/P3TERX/GeoLite.mmdb), direct Country database URL listed above.
- The mirror fetches GeoLite data from MaxMind using the maintainer's MaxMind license key and currently publishes on a three-day schedule; it is not MaxMind-hosted. MaxMind's GeoLite terms require keeping database data current.
- Existing Headscale global route primary is derived runtime state, not a persisted node field. Announced prefixes, approved routes, DERP NetInfo, endpoints, and assigned Tailscale addresses already have existing storage paths; do not add DDL for region selection.
