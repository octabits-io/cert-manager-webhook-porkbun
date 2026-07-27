# cert-manager-webhook-porkbun

A [cert-manager](https://cert-manager.io/) ACME **DNS-01** solver webhook for
domains hosted at [Porkbun](https://porkbun.com/), which cert-manager has no
built-in solver for. DNS-01 is what makes wildcard certificates possible.

This is a maintained fork of
[`Talinx/cert-manager-webhook-porkbun`](https://github.com/Talinx/cert-manager-webhook-porkbun),
which has had no commits since October 2024 and has issues disabled. See
[what changed](#what-changed-from-upstream) and
[credits](#credits-and-license).

If you are choosing between forks, note that
[`pabloa/cert-manager-webhook-porkbun`](https://github.com/pabloa/cert-manager-webhook-porkbun)
is also actively released and may suit you better — see
[related work](#related-work) for an honest comparison.

---

## Install

```bash
helm repo add octabits https://octabits-io.github.io/cert-manager-webhook-porkbun
helm repo update

helm install cert-manager-webhook-porkbun octabits/cert-manager-webhook-porkbun \
  --namespace cert-manager \
  --set groupName=acme.example.com \
  --set 'rbac.secretAccess.secretNames={porkbun-api-credentials}'
```

`groupName` is the Kubernetes API group this webhook registers. Use a domain
you control so it cannot collide with another webhook in the cluster, and use
the same value in every Issuer that references the solver.

The chart is also published as an OCI artifact at
`oci://ghcr.io/octabits-io/charts/cert-manager-webhook-porkbun`.

### Credentials

Create an API key at <https://porkbun.com/account/api>, then:

```bash
kubectl -n cert-manager create secret generic porkbun-api-credentials \
  --from-literal=api-key=pk1_xxx \
  --from-literal=secret-key=sk1_xxx
```

> **API access is disabled per domain by default.** Enable it under
> Domain Management → Details → API Access for each domain you want certificates
> for. Without it the API returns a generic error that does not mention the
> setting, which makes this the most common cause of a failing setup.

For a `ClusterIssuer`, the Secret must live in cert-manager's
`--cluster-resource-namespace` (`cert-manager` by default). For a namespaced
`Issuer`, it lives in the Issuer's namespace — add that namespace to
`rbac.secretAccess.namespaces`.

### Issuer

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-dns01
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: you@example.com
    privateKeySecretRef:
      name: letsencrypt-dns01-key
    solvers:
      - dns01:
          webhook:
            groupName: acme.example.com   # must match the chart's groupName
            solverName: porkbun
            config:
              apiKey:
                name: porkbun-api-credentials
                key: api-key
              secretApiKey:
                name: porkbun-api-credentials
                key: secret-key
```

Then request a wildcard certificate:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: example-wildcard
spec:
  secretName: example-wildcard-tls
  issuerRef:
    name: letsencrypt-dns01
    kind: ClusterIssuer
  dnsNames:
    - example.com
    - "*.example.com"
```

## Solver configuration

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `apiKey` | SecretKeySelector | required | Secret holding the Porkbun API key. |
| `secretApiKey` | SecretKeySelector | required | Secret holding the Porkbun secret API key. |
| `apiKeySecretRef` | SecretKeySelector | — | Alias for `apiKey`, matching cert-manager's naming convention. |
| `secretKeySecretRef` | SecretKeySelector | — | Alias for `secretApiKey`. |
| `domain` | string | derived | Pin the registered domain to operate on. Only needed if the automatic derivation is wrong — see [zones vs. registered domains](#zones-vs-registered-domains). |
| `ttl` | int | `600` | TTL of the challenge TXT record, in seconds. Values below the Porkbun minimum are clamped up. |

## Chart values

The full list is documented inline in
[`values.yaml`](charts/cert-manager-webhook-porkbun/values.yaml) and enforced by
[`values.schema.json`](charts/cert-manager-webhook-porkbun/values.schema.json).
The ones worth knowing about:

| Key | Default | Description |
| --- | --- | --- |
| `groupName` | `acme.example.com` | API group. **Always set this.** |
| `image.digest` | `""` | Pin the image by digest instead of tag. |
| `securePort` | `8443` | Unprivileged container port. The Service still exposes 443. |
| `rbac.secretAccess.scope` | `Namespaced` | `Namespaced` or `Cluster`. |
| `rbac.secretAccess.namespaces` | `[]` | Extra namespaces to grant Secret reads in. |
| `rbac.secretAccess.secretNames` | `[]` | Restrict access to specific Secret names. Recommended. |
| `logLevel` | `0` | `2` also logs zone-resolution decisions. |
| `podDisruptionBudget.enabled` | `false` | Enable when running more than one replica. |

## Zones vs. registered domains

The Porkbun API is addressed by **registered domain**, but cert-manager hands
the solver a **DNS zone** derived from an SOA lookup. These are usually the
same, and diverge when the challenge name sits in a delegated sub-zone: a zone
of `sub.example.com` is not something Porkbun's API will accept.

The solver therefore derives the registered domain from the challenge FQDN
using the public suffix list, which gives the same answer as the zone in the
common case and the correct one otherwise. Set `domain` in the solver config to
override it, and `logLevel: 2` to see what it decided.

## What changed from upstream

### Correctness

- **A malformed response no longer panics the pod.** Responses were decoded
  into `**T` and then dereferenced without a nil check, so a `null` or
  truncated body took down the webhook.
- **API errors are now diagnosable.** Porkbun's `message` field was absent from
  every response struct, so all failures surfaced as `invalid status "ERROR"`
  with no further detail. Both the message and the HTTP status are now
  reported.
- **HTTP responses are status-checked before decoding.** A 4xx/5xx HTML error
  page previously produced a JSON decoding error that hid the real cause.
- **Requests have a timeout.** The HTTP client was a zero-value
  `http.Client` — no timeout at all — driven by `context.Background()`. A
  stalled Porkbun connection hung the solver indefinitely.
- **Transient failures are retried** with exponential backoff and jitter.
  Porkbun rate limits aggressively; a single 503 used to fail the challenge
  outright.
- **TTL is valid.** The record TTL was hard-coded to `60`, below Porkbun's
  minimum. It now defaults to 600 and is configurable, clamped up rather than
  rejected.
- **Config is always validated.** Validation was skipped whenever
  `AllowAmbientCredentials` was set — which cert-manager sets by default for
  ClusterIssuers. Porkbun has no ambient credentials, so this only ever turned
  a clear configuration error into an opaque authentication failure.
- **Subdomain splitting uses suffix matching.** `strings.Index` finds the first
  occurrence of the domain anywhere in the name, mis-splitting repeated label
  sequences.
- **Delegated sub-zones work** — see [above](#zones-vs-registered-domains).
- **Record IDs stay opaque strings.** Parsing them into a 32-bit integer is a
  live overflow risk, and would strand challenge records permanently once IDs
  cross the boundary.
- **Concurrent challenges for one domain are serialised**, removing a
  read-modify-write race between the apex and wildcard challenges of a single
  certificate and halving the request rate.
- A delete-failure error message reported the wrong status variable.

### Security

- **The webhook no longer reads every Secret in the cluster.** Upstream bound a
  ClusterRole granting `get,watch,list` on `secrets` cluster-wide. Access is now
  a namespaced Role, `get` only, optionally restricted to named Secrets.
- **Runs as non-root** (uid 65532) on an unprivileged port, on
  `distroless/static`. No shell, no package manager, no `NET_BIND_SERVICE`.
- `readOnlyRootFilesystem`, all capabilities dropped, `RuntimeDefault` seccomp
  by default — compatible with the `restricted` Pod Security Standard.
- **Current dependencies**: Go 1.26, cert-manager 1.21, Kubernetes 1.36
  libraries. Upstream shipped Go 1.19 module requirements, cert-manager 1.11.3
  and `golang.org/x/net` v0.7.0.
- Images are signed with cosign and carry SBOM and provenance attestations.
  `latest` is not published.
- CI fails on any HIGH/CRITICAL vulnerability and **re-runs weekly**, so a
  dependency that goes bad after release is caught rather than sitting until
  someone notices.

### Operational

- Adds the `flowcontrol` RBAC every aggregated apiserver needs, removing a
  steady stream of authorization errors from the logs.
- Structured logging throughout, with the record name and domain on every
  message.
- Unit tests covering each bug above, plus a gated conformance suite.
- `values.schema.json` rejects invalid configuration at install time.

## Migrating from `talinx/cert-manager-webhook-porkbun`

The solver name, config keys and rendered resource names are unchanged, so
Issuers need no edits. Point the chart at this repository and bump the version:

```diff
   chart:
     spec:
       chart: cert-manager-webhook-porkbun
-      version: "1.0.0"
+      version: "2.0.0"
       sourceRef:
         kind: HelmRepository
         name: porkbun-webhook
   values:
     groupName: acme.example.com
     image:
-      tag: "1.0.0"
+      repository: ghcr.io/octabits-io/cert-manager-webhook-porkbun
-    securityContext:
-      capabilities:
-        add:
-          - NET_BIND_SERVICE
+    rbac:
+      secretAccess:
+        secretNames: ["porkbun-api-credentials"]
```

Drop any `NET_BIND_SERVICE` capability and `runAsRoot` exceptions: the webhook
now listens on 8443 as uid 65532, and the hardened `securityContext` is the
chart default.

Upgrading swaps the Deployment and tightens RBAC. In-flight challenges are
retried by cert-manager, and existing certificates are unaffected — nothing
re-issues on upgrade.

## Development

```bash
make check            # tidy check, vet, unit tests, chart lint, manifest validation
make test             # unit tests with the race detector
make lint             # golangci-lint
make build            # container image
make scan             # Trivy scan of the built image
make test-conformance TEST_ZONE_NAME=example.com.   # needs real credentials
```

## Related work

[`pabloa/cert-manager-webhook-porkbun`](https://github.com/pabloa/cert-manager-webhook-porkbun)
is another actively released fork of the same `Talinx` ancestor (v1.1.3, March
2026). It was developed independently of this one, and it deserves credit for
identifying the registered-domain-versus-DNS-zone problem before we did.

The two forks differ in scope. `pabloa` is a focused set of changes on top of
upstream: it adds zone detection and updates some dependencies, and keeps the
rest as-is. This fork rewrites the API client and solver and reworks the chart.

Concretely, as of `pabloa` v1.1.3:

| | `Talinx` 1.0.0 | `pabloa` 1.1.3 | this fork |
| --- | --- | --- | --- |
| Delegated sub-zones | broken | fixed, by probing the API | fixed, via the public suffix list |
| Nil-deref panic on a malformed response | yes | yes | fixed |
| API error messages surfaced | no | no | fixed |
| HTTP status checked before decoding | no | no | fixed |
| Request timeout | none | none | 30s per request |
| Retry on rate limiting | no | no | backoff with jitter |
| Challenge TXT TTL | `60` | `60` | 600, configurable |
| Config validated for ClusterIssuers | no | no | fixed |
| Secret RBAC | cluster-wide `get,watch,list` | cluster-wide `get,watch,list` | namespaced `get` |
| Runs as | root, Alpine | root, Alpine | uid 65532, distroless |
| Trivy HIGH/CRITICAL in the image | 4 / 56 | 3 / 41 | 0 / 0 |
| Unit tests | none | none | 81% coverage |

Two notes in fairness to `pabloa`. Its zone probing asks Porkbun which domains
actually exist, so it can resolve cases the public suffix list gets wrong —
at the cost of one or two extra full-zone listings per challenge. And its
dependency situation reflects a real problem, not neglect: a commit updating
cert-manager and Kubernetes was reverted with *"Downgrade deps to cert-manager
v1.11.3 + k8s v0.26.4 to fix startup crash"*. This fork runs cert-manager 1.21
and Kubernetes 1.36 libraries, which required rebuilding against the current
webhook API rather than bumping in place.

If your zones are straightforward and you want the smallest delta from
upstream, `pabloa` is a reasonable choice.

## Credits and license

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

This project stands on other people's work:

- **[cert-manager/webhook-example](https://github.com/cert-manager/webhook-example)**
  — the cert-manager Authors, for the solver scaffold, the Helm chart layout
  and the conformance harness that every DNS01 webhook is built on.
- **[bcspragu/cert-manager-webhook-porkbun](https://github.com/bcspragu/cert-manager-webhook-porkbun)**
  — for writing the original Porkbun integration.
- **[Talinx/cert-manager-webhook-porkbun](https://github.com/Talinx/cert-manager-webhook-porkbun)**
  — for packaging it into a Helm chart with release automation, which is what
  made it usable in a real cluster. This repository forks that work directly.
- **[baarde/cert-manager-webhook-ovh](https://github.com/baarde/cert-manager-webhook-ovh)**
  — credited by the original author as an inspiration.
- **[pabloa/cert-manager-webhook-porkbun](https://github.com/pabloa/cert-manager-webhook-porkbun)**
  — for independently finding the sub-zone problem. No code from that fork is
  used here.

Files that remain closely derived from upstream carry a comment saying so, as
Apache-2.0 section 4(b) requires. [NOTICE](NOTICE) records the full chain.

This fork exists because upstream is unmaintained, not because of any fault in
the original authors' work. Fixes here are offered back to anyone who wants
them.
