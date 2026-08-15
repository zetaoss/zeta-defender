# zeta-defender

`zeta-defender` evaluates a PromQL condition and temporarily enables Cloudflare
Under Attack Mode when that condition remains true.

Its runtime state machine has only three states:

* `normal`
* `arming`
* `fighting`

## Configuration

Copy [`config.example.yaml`](config.example.yaml) and set
`CLOUDFLARE_API_TOKEN`.

Environment variables in the YAML are expanded before decoding.

```yaml
server:
  listen: ":8080"

metrics:
  endpoint: http://prometheus:9090
  interval: 1m
  expr: 'rate(node_cpu_usage_seconds_total[5m]) / on(node) kube_node_status_allocatable{node=~".*my-pool-1.*",resource="cpu",unit="core"} > bool 0.9'

policy:
  arming:
    levels: 5
  fighting:
    levelDuration: 10m
    levels: 12

actions:
  cloudflare:
    apiToken: ${CLOUDFLARE_API_TOKEN}
    zoneID: example-zone-id
    # Security level applied when returning to normal operation.
    normalSecurityLevel: essentially_off
    # Startup mode: preserve, normal, or fighting.
    startupMode: preserve
```

## Evaluation

The Prometheus result may be a scalar or instant vector.

* Zero means false.
* A non-zero finite value means true.
* For a vector, any true sample makes the result true.
* An empty vector means false.

Evaluation errors reset arming progress.

Prefer PromQL `bool` comparisons so the result is explicitly `0` or `1`.

## Defense cycle

The first matching evaluation moves the defender from `normal` to arming level
0. Arming exposes `policy.arming.levels` levels, numbered from 0. Each additional
consecutive matching evaluation advances one level; a match after the final
arming level activates defense and moves the defender to `fighting`.

A non-matching evaluation while `arming` ends the current attack cycle, returns
the defender to `normal`, and resets the fighting level to 1.

While `fighting`, no metrics requests are made. Defense remains active for:

```text
levelDuration * fighting level
```

When the fighting period ends, the defender requests deactivation before metric
evaluation resumes, then returns to `arming`. If zeta-defender enabled UAM and
the setting is still `under_attack`, `normalSecurityLevel` is applied and the
next evaluations observe the unprotected load again. Out-of-band changes are left
unchanged.

If evaluations continue to match the condition through every arming level, the
attack cycle is considered to be continuing. The fighting level is incremented
and defense is activated again for the longer duration.

If the condition stops matching while arming, the attack cycle ends and the
fighting level is reset to 1.

For example, with:

```yaml
metrics:
  interval: 1m

policy:
  arming:
    levels: 5
  fighting:
    levelDuration: 10m
    levels: 12
```

a continuing attack may progress like this:

```text
normal
  -> arming
  -> fighting level 1 for 10m
  -> defense off
  -> arming
  -> fighting level 2 for 20m
  -> defense off
  -> arming
  -> fighting level 3 for 30m
  -> ...
```

This gradually reduces how often defense is released during a long-running
attack while still periodically checking whether protection is still needed.

When zeta-defender owns the active defense, re-evaluation temporarily removes it
so the condition can observe unprotected load. During a continuing attack, this
creates an intentional probe window of approximately
`interval * policy.arming.levels`.

## Cloudflare action

In Cloudflare's API, Under Attack Mode is managed through the
[`security_level`](https://developers.cloudflare.com/api/resources/zones/subresources/settings/methods/edit/)
setting. zeta-defender treats its operational meaning as two logical states:

* **Normal**: `normalSecurityLevel`, applied by `startupMode: normal` and when
  zeta-defender leaves an owned `fighting` period.
* **Active defense (`under_attack`)**: [Under Attack Mode](https://developers.cloudflare.com/fundamentals/reference/under-attack-mode/),
  which presents an interstitial Managed Challenge to help mitigate L7 DDoS
  attacks.

The API also exposes `off`, `essentially_off`, `low`, `medium`, and `high`.
Any of these values can be selected as `normalSecurityLevel`; the default is
`essentially_off`, matching the value Cloudflare applies when Under Attack Mode
is disabled in the current dashboard. `under_attack` is reserved for the
`fighting` state and is not a valid normal level.

> [!NOTE]
> Under Attack Mode can disrupt clients that cannot process an interstitial
> HTML challenge, including API clients and webhooks. Use Cloudflare
> Configuration Rules for scoped Security Level behavior, or Turnstile
> pre-clearance where appropriate.

The Cloudflare action tracks whether it changed the zone to `under_attack`. It
applies `normalSecurityLevel` when fighting ends only if it owns that change and
the setting is still `under_attack`. `startupMode` controls startup behavior:

* `preserve` (default) leaves the existing setting unchanged and starts the
  controller in `normal`.
* `normal` immediately applies `normalSecurityLevel` and starts the controller
  in `normal`.
* `fighting` immediately applies `under_attack` and starts the controller in
  fighting level 1 for `levelDuration`.

Pre-existing Under Attack Mode remains unowned in `preserve` mode and is not
disabled by zeta-defender. The same ownership rule applies when `fighting` is
selected but UAM was already active: the controller starts fighting, but UAM is
left active when that period ends. If the process exits or crashes while
protection is active, the protection is also left unchanged.

zeta-defender applies security levels on startup and state transitions; it does
not continuously overwrite out-of-band changes made while the controller stays
in `normal` or `arming`.

zeta-defender currently supports one active instance per Cloudflare zone.
Running multiple instances against the same zone is not supported.

The API token is used only in the `Authorization` header and is never logged.

## Run

```sh
go run ./cmd/zeta-defender --config config.yaml
```

Print either binary's version with `defender --version` or
`defendertool version`. Logs use the human-readable `text` format by default;
select structured output with `--log-format json`. The Kubernetes deployment
enables JSON logs.

Query the zone's current Cloudflare security level with the read-only companion
CLI. It uses the same configuration file as the daemon:

```sh
go run ./cmd/defendertool --config config.yaml status
```

`SIGINT` and `SIGTERM` stop metric polling and the HTTP server gracefully.
Active Cloudflare protection is left unchanged on shutdown.

Run the same formatting, module, vet, race-test, and container-build checks used
by CI with:

```sh
make checks
```

## Container

Build the image and run it with a configuration file mounted read-only:

```sh
docker build -t zeta-defender .
docker run --rm \
  -p 8080:8080 \
  -e CLOUDFLARE_API_TOKEN \
  -v "$PWD/config.yaml:/zeta-defender/etc/config.yaml:ro" \
  zeta-defender
```

A single-instance Kubernetes deployment is available in [`deploy`](deploy).
Update its ConfigMap and image, create
the API token Secret as described there, and apply it with:

```sh
make -C deploy apply
```

Following Fluent Bit's application-owned layout, the image installs binaries
in `/zeta-defender/bin` and uses `/zeta-defender/etc/config.yaml` as their
default configuration path. The runtime image also includes Alpine's `sh` for
operational inspection. To query the current Cloudflare security level from the
running Pod:

```sh
kubectl exec -it deploy/zeta-defender -- sh
/zeta-defender/bin/defendertool status
```

Pull requests and pushes to `main` run formatting, module, vet, race-test, and
container-build checks. Pushing a semantic version tag such as `v1.2.3` builds
multi-platform images and publishes them to `ghcr.io/zetaoss/zeta-defender`.

## Runtime metrics

The HTTP server exposes Prometheus metrics at `/metrics` and a process liveness
check at `/healthz`.

The default listen address is `:8080`.

`zeta_defender_level` is the single state metric:

* `0` — normal (`0xx` state range)
* `100 + arming level` — arming progress (`1xx` state range)
* `200 + fightingLevel` — active fighting (`2xx` state range)

For example:

```text
0     normal
100   arming level 0
101   arming level 1
...
104   arming level 4 (with policy.arming.levels: 5)
201   fighting level 1
202   fighting level 2
...
212   fighting level 12
```

Both `policy.arming.levels` and `policy.fighting.levels` must be between `1` and
`99`, keeping their values within the `1xx` and `2xx` state ranges.

`zeta_defender_fighting_seconds_total` is a monotonically increasing counter of
the total time, in seconds, that the defender has spent in the `fighting` state.
It can be used to calculate fighting time over longer periods, for example:

```promql
increase(zeta_defender_fighting_seconds_total[1d])
```

returns the number of seconds spent fighting during the last day.
