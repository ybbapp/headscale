# Configuration

- Headscale loads its configuration from a YAML file
- It searches for `config.yaml` in the following paths:
    - `/etc/headscale`
    - `$HOME/.headscale`
    - the current working directory
- To load the configuration from a different path, use:
    - the command line flag `-c`, `--config`
    - the environment variable `HEADSCALE_CONFIG`
- Validate the configuration file with: `headscale configtest`

!!! example "Get the [example configuration from the GitHub repository](https://github.com/juanfont/headscale/blob/main/config-example.yaml)"

    Always select the [same GitHub tag](https://github.com/juanfont/headscale/tags) as the released version you use to
    ensure you have the correct example configuration. The `main` branch might contain unreleased changes.

    === "View on GitHub"

        - Development version: <https://github.com/juanfont/headscale/blob/main/config-example.yaml>
        - Version {{ headscale.version }}: https://github.com/juanfont/headscale/blob/v{{ headscale.version }}/config-example.yaml

    === "Download with `wget`"

        ```shell
        # Development version
        wget -O config.yaml https://raw.githubusercontent.com/juanfont/headscale/main/config-example.yaml

        # Version {{ headscale.version }}
        wget -O config.yaml https://raw.githubusercontent.com/juanfont/headscale/v{{ headscale.version }}/config-example.yaml
        ```

    === "Download with `curl`"

        ```shell
        # Development version
        curl -o config.yaml https://raw.githubusercontent.com/juanfont/headscale/main/config-example.yaml

        # Version {{ headscale.version }}
        curl -o config.yaml https://raw.githubusercontent.com/juanfont/headscale/v{{ headscale.version }}/config-example.yaml
        ```

## GeoIP

GeoIP country lookup is optional and disabled by default. When enabled,
Headscale reads a MaxMind DB (MMDB) country database from `geoip.database_path`.
Only globally routable public addresses are eligible for lookup; private,
loopback, link-local, and reserved addresses return no country.

```yaml
geoip:
  enabled: true
  database_path: /var/lib/headscale/GeoLite2-Country.mmdb
  source_url: https://raw.githubusercontent.com/P3TERX/GeoLite.mmdb/download/GeoLite2-Country.mmdb
  update_interval: 72h
```

`source_url` defaults to the P3TERX GeoLite2 Country mirror. Headscale attempts
a download at startup and then at `update_interval`; failed or invalid updates
leave the last valid database in use. Configure a different source URL if your
deployment uses another provider. Without a valid local database or successful
download, GeoIP lookups return no country and regional routing falls back to
DERP RTT. While no valid database is available, Headscale retries from 5 minutes
after the failed startup download, doubling the delay up to 6 hours. A successful
download switches back to the configured refresh interval. Startup and retry
logs include the database path, outcome, and next retry delay without logging
the source URL. If `source_url` is explicitly empty, automatic downloads and
retries are disabled; a valid local database is then required for GeoIP.

Regional router fallback order is optional and configured by viewer country
when GeoIP is active, or by `DERP-<region-id>` when using RTT regions. Country
codes are two-letter ISO 3166-1 alpha-2 values. Entries are tried in the listed
order when the viewer's country or DERP region has no healthy router.

```yaml
node:
  routes:
    regional_routing:
      fallback_regions:
        DE: [FR, NL]
```
