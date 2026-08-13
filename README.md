# zeta-defender

`zeta-defender` evaluates a PromQL condition and temporarily enables Cloudflare
Under Attack Mode when that condition remains true.

Its runtime state machine has only three states:

* `standby`
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
  expr: 'sum(rate(http_requests_total{status=~"5.."}[5m])) >= bool 100'

policy:
  armingChecks: 5
  fighting:
    baseDuration: 10m
    maxLevel: 12

actions:
  cloudflare:
    apiToken: ${CLOUDFLARE_API_TOKEN}
    zoneID: example-zone-id
```

## Evaluation

The Prometheus result may be a scalar or instant vector.

* Zero means false.
* A non-zero finite value means true.
* For a vector, any true sample makes the result true.
* An empty vector means false.

Evaluation errors do not count as successful checks and reset arming progress.

Prefer PromQL `bool` comparisons so the result is explicitly `0` or `1`.

## Defense cycle

The first true result moves the defender from `standby` to `arming`. This first
result is not counted toward `armingChecks`.

After `armingChecks` additional consecutive true results, defense is activated
and the defender enters `fighting`.

A false result while `arming` ends the current attack cycle, returns the defender
to `standby`, and resets the fighting level to 1.

While `fighting`, no metrics requests are made. Defense remains active for:

```text
baseDuration * min(level, maxLevel)
```

When the fighting period ends, defense is deactivated before metric evaluation
resumes. The defender then returns to `arming` and observes the unprotected load
again.

If arming succeeds again, the attack cycle is considered to be continuing. The
fighting level is incremented and defense is activated again for the longer
duration.

If arming fails, the attack cycle ends and the fighting level is reset to 1.

For example, with:

```yaml
metrics:
  interval: 1m

policy:
  armingChecks: 5
  fighting:
    baseDuration: 10m
    maxLevel: 12
```

a continuing attack may progress like this:

```text
standby
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

Re-evaluation requires temporarily removing the defense so the condition can
observe unprotected load. During a continuing attack, this creates an
intentional probe window of approximately `interval * armingChecks`.

## Cloudflare action

The Cloudflare action remembers the zone's previous security level when defense
is activated and restores it when fighting ends.

At startup, zeta-defender preserves an existing Under Attack Mode setting. It
only disables protection that it successfully enabled during the current
process lifetime. If the process exits or crashes while protection is active,
the protection is left unchanged and must be disabled manually.

zeta-defender currently supports one active instance per Cloudflare zone.
Running multiple instances against the same zone is not supported.

The API token is used only in the `Authorization` header and is never logged.

## Run

```sh
go run ./cmd/zeta-defender -config config.yaml
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
  -v "$PWD/config.yaml:/etc/zeta-defender/config.yaml:ro" \
  zeta-defender
```

A single-instance Kubernetes deployment is available in [`deploy`](deploy).
Update its ConfigMap and image, create
the API token Secret as described there, and apply it with:

```sh
kubectl apply -k deploy
```

Pull requests and pushes to `main` run formatting, module, vet, race-test, and
container-build checks. Pushing a semantic version tag such as `v1.2.3` builds
multi-platform images and publishes them to `ghcr.io/zetaoss/zeta-defender`.

## Runtime metrics

The HTTP server exposes Prometheus metrics at `/metrics` and a process liveness
check at `/healthz`.

The default listen address is `:8080`.

`zeta_defender_level` is the single state metric:

* `0` — standby
* `1-99` — arming progress (`1 + successful checks`)
* `100` — reserved
* `100 + fightingLevel` — active fighting

For example:

```text
0     standby
1     arming, no successful additional check yet
2     arming, 1 successful additional check
...
101   fighting level 1
102   fighting level 2
...
112   fighting level 12
```

`zeta_defender_fighting_seconds_total` is a monotonically increasing counter of
the total time, in seconds, that the defender has spent in the `fighting` state.
It can be used to calculate fighting time over longer periods, for example:

```promql
increase(zeta_defender_fighting_seconds_total[1d])
```

returns the number of seconds spent fighting during the last day.

Additional counters report:

* condition evaluations
* evaluation errors
* Cloudflare enable outcomes
* Cloudflare disable outcomes
