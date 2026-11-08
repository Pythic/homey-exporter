# homey-exporter

Prometheus exporter for [Homey Pro](https://homey.app). One static binary that polls the
Homey Web API over your LAN and exposes every numeric and boolean device capability, every
Logic variable and Homey's own system health as metrics. Nothing runs on the Homey itself.

```
homey_device_measure_power{device_name="P1 meter",zone="Utility room",sub="phase2",units="W",...} 2752
homey_device_measure_temperature{device_name="Living room thermostat",zone="Living room",units="°C",...} 21.5
homey_logic_variable{name="Setpoint"} 21.5
homey_system_temperature_celsius 58.9
```

Tested against Homey Pro (2026) on firmware 13.5.3. Any Homey Pro with the local Web API
(Homey Pro 2019 on firmware 10+, Homey Pro Early 2023 and later) should work; Homey Cloud
(the bridge-based subscription) has no local API and is not supported.

## Quick start

1. Create an API key on the Homey: Homey app → **Settings → API Keys → New API key**.
   Read access to *Devices*, *Zones*, *Logic* and *System* is enough. Copy the token.
2. Find the Homey's IP (Homey app → Settings → General, or your router). The API listens on
   `http://<homey-ip>`. The cloud relay `https://<cloud-id>.connect.athom.com` also works
   with the same token when the exporter cannot reach the LAN.
3. Run:

```sh
# binary (download from the releases page, or: go install github.com/pythic/homey-exporter@latest)
HOMEY_URL=http://192.168.1.10 HOMEY_TOKEN=... ./homey-exporter

# or docker
docker run -d --name homey-exporter -p 9414:9414 \
  -e HOMEY_URL=http://192.168.1.10 -e HOMEY_TOKEN=... \
  ghcr.io/pythic/homey-exporter:latest
```

4. Open <http://localhost:9414/metrics>. Add the target to Prometheus:

```yaml
scrape_configs:
  - job_name: homey
    static_configs:
      - targets: ["localhost:9414"]
```

[`examples/docker-compose.yml`](examples/docker-compose.yml) starts the exporter together with
VictoriaMetrics (Prometheus-compatible storage, 5 years retention) and Grafana with the data
source and the dashboard below pre-provisioned: copy `examples/.env.example` to `examples/.env`,
fill in the URL and token, `docker compose up -d`.

## Grafana dashboard

[`examples/dashboards/homey.json`](examples/dashboards/homey.json) (import it by hand or let the
compose example provision it) shows grid power and current per phase, a boiler estimate, floor
heating groups (power, floor temperature, on/off timeline), room temperature and humidity,
Logic variables and Homey's own CPU, temperature and memory per app. Everything is selected
through dashboard variables, so it works on any Homey without editing:

| variable | pick |
|---|---|
| Energy meter (P1) | the device that reports `measure_power.phase1..3` |
| Boiler phase / other loads / baseline | the phase your boiler is on, the metered loads that share it (they are subtracted) and a baseline in W; the remainder is plotted as the boiler estimate and integrated to kWh |
| Floor heating groups | thermostats and relays whose `measure_power` you want stacked |
| Rooms | sensors and thermostats for the temperature and humidity panels |
| Logic variables | variables to plot (booleans as 0/1) |

The provider has `allowUiUpdates: true`, so your variable choices can be saved from the UI.

## Configuration

One JSON file, passed with `-config` (default `./homey-exporter.json`; a missing file is fine when
the environment supplies the values). See [`config.example.json`](config.example.json).

| key | env override | default | meaning |
|---|---|---|---|
| `homey_url` | `HOMEY_URL` | required | Base URL of the Homey Web API |
| `token` | `HOMEY_TOKEN` | required | Homey API key (Personal Access Token) |
| `token_file` | `HOMEY_TOKEN_FILE` | | Read the token from this file instead (keeps secrets out of the config) |
| `listen` | `HOMEY_EXPORTER_LISTEN` | `:9414` | Address for `/metrics`, `/healthz` and `/` |
| `cache_seconds` | | `10` | One fetch from Homey is reused for scrapes within this window |
| `timeout_seconds` | | `10` | HTTP timeout per Homey API call |

Environment variables win over the file. The token is never logged. The Docker image runs as
a non-root user, so a mounted `token_file` must be readable by uid 65532; in containers the
`HOMEY_TOKEN` environment variable (for example via `env_file`) is the simpler route.

The exporter fetches on scrape (six API calls, about one second on a Homey with 100 devices),
so Prometheus' `scrape_interval` sets the poll rate; `cache_seconds` protects the Homey when
several scrapers hit the exporter at once.

## Metrics

### Devices

Every capability of type `number` or `boolean` with a non-null value becomes a gauge named
after the capability. A sub-capability (`measure_power.phase2`) keeps the base name and goes
into the `sub` label. Booleans are 0/1. Capabilities of type `string` and `enum` are skipped
(see roadmap).

| metric | labels |
|---|---|
| `homey_device_<capability>` | `device_id`, `device_name`, `zone`, `class`, `sub`, `units` |
| `homey_device_available` | `device_id`, `device_name`, `zone`, `class` |

Examples: `homey_device_measure_power`, `homey_device_measure_temperature`,
`homey_device_measure_humidity`, `homey_device_meter_power`, `homey_device_onoff`,
`homey_device_dim`, `homey_device_alarm_motion`, `homey_device_target_temperature`.

### Logic variables

| metric | labels |
|---|---|
| `homey_logic_variable` | `id`, `name` (number and boolean variables) |

### System

| metric | labels |
|---|---|
| `homey_system_info` | `version`, `model`, `model_id`, `platform`, `node_version` |
| `homey_system_load_average` | `period` = `1m`, `5m`, `15m` |
| `homey_system_uptime_seconds` | |
| `homey_system_cpu_seconds_total` | `cpu`, `mode` |
| `homey_system_temperature_celsius` | SoC temperature |
| `homey_system_throttled`, `homey_system_undervoltage` | 0/1 |
| `homey_system_memory_total_bytes`, `_free_bytes`, `_swap_bytes` | |
| `homey_system_memory_component_bytes` | `id`, `name`: Homey core, each app, Insights, ... |
| `homey_system_storage_total_bytes`, `_free_bytes`, `_used_bytes` | |
| `homey_system_storage_component_bytes` | `id`, `name` |

Network details (SSID, addresses, MAC) are not read.

### Exporter

| metric | meaning |
|---|---|
| `homey_exporter_up` | 1 when the last fetch of all six endpoints succeeded |
| `homey_exporter_scrape_duration_seconds` | duration of the last fetch |
| `homey_exporter_scrape_errors_total` | failed API calls since start |
| `homey_exporter_last_success_timestamp_seconds` | last fully successful fetch |
| `homey_exporter_build_info` | `version`, `commit` |

Plus the standard Go runtime and process metrics. `/healthz` returns 503 while the Homey is
unreachable.

A Homey with 100 devices produces roughly 700 series.

## Example queries

```promql
# house power per phase
homey_device_measure_power{device_name="P1 meter", sub=~"phase."}

# kWh imported today (P1 meter counter)
increase(homey_device_meter_power{device_name="P1 meter", sub="imported"}[1d])

# which apps eat memory
topk(5, homey_system_memory_component_bytes)

# alert when Homey is unreachable for 5 minutes
homey_exporter_up == 0
```

## Roadmap

- `enum` and `string` capabilities as info metrics (`homey_device_<capability>_info{value=...}`)
- per-capability `last_updated` timestamps
- push updates over Homey's realtime socket instead of polling
- Grafana dashboard

## Development

```sh
./test-runner.sh -v -c -r   # go vet + tests with race detector and coverage
golangci-lint run
go build .
```

Tests run against fixtures in `testdata/`; no Homey needed.

## License

MIT, see [LICENSE](LICENSE). Not affiliated with Athom. The metric naming follows the
convention of the (JavaScript, on-Homey) [Prometheus.io app](https://github.com/rickardp/homey-prometheus-exporter)
by rickardp so dashboards translate easily.
