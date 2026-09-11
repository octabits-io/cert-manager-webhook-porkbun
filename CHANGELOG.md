# Changelog

## 2.0.7

Security release. No functional changes to the solver; the rendered manifests
differ from 2.0.6 only in the version labels and the image tag.

### Security

- `go.etcd.io/etcd/client/pkg/v3` 3.6.14, clearing CVE-2026-73500
  (GHSA-6vch-q96h-7gc3, GO-2026-6107, HIGH). `tlsListener.acceptLoop` spawned a
  handshake goroutine per accepted connection with no deadline and no bound, so
  a flood of connections that never complete the handshake could exhaust
  memory. **Deployments running 2.0.6 were not exposed**: the package enters
  through the apiserver's etcd client and `govulncheck` placed it in the
  imported-but-never-called tier. The webhook accepts its own TLS connections
  through `k8s.io/apiserver`, not through this listener, and opens no etcd
  connection at all.
- `github.com/google/cel-go` 0.30.0, clearing GHSA-gcjh-h69q-9w9g
  (GO-2026-6094, MEDIUM), in which `NativeTypes` and `ParseStructTag` exposed
  unexported struct fields to CEL expressions. Also not reachable: cel-go is
  linked in through the apiserver's CEL validation machinery, this solver
  declares no CEL expressions, and the vulnerable constructors are never
  called.
- Both advisories were outstanding against 2.0.6 and **neither tripped the
  HIGH/CRITICAL source or image gate** — verified by running `trivy fs` against
  the 2.0.6 tree, which reports a clean `go.mod` even for the HIGH. They
  surfaced only in `govulncheck`'s imported-packages tier. That is the mirror
  image of 2.0.6, where Trivy caught the gRPC advisory and `govulncheck` missed
  it: neither scanner is sufficient alone, which is why `/cve-scan` runs both.

### Changed

- `github.com/cert-manager/cert-manager` 1.21.2, which is what carries the two
  bumps above as transitive minimums rather than as explicit promotions. Its own
  fixes — bounding ACME response bodies, keeping ACME server responses out of
  `Issuer` status, and hardening the admission path against an unset resource —
  **do not apply to this binary**: only 17 cert-manager packages are linked
  here, all under `pkg/acme/webhook`, `pkg/apis` and `pkg/logs`, and none of the
  ACME issuer, controller or admission-webhook code is among them. Those fixes
  protect a cert-manager deployment, not this solver. cert-manager published no
  release notes for 1.21.2, so the change set is the
  [`v1.21.1...v1.21.2` range](https://github.com/cert-manager/cert-manager/compare/v1.21.1...v1.21.2).
- `golang.org/x/net` 0.59.0, with `x/crypto` 0.57.0, `x/mod` 0.41.0, `x/text`
  0.42.0, `x/sync` 0.23.0, `x/sys` 0.48.0 and `x/term` 0.46.0 following as its
  transitive minimums.
- 2.0.6's explicit promotion of `google.golang.org/grpc` to 1.83.2 is now
  carried by the parent — cert-manager 1.21.2 requires exactly that version, as
  it does `x/crypto` 0.56.0 — so the note asking for it to be dropped once a
  cert-manager release caught up is settled. Nothing in `go.mod` reaches ahead
  of the module graph any more.
- The `k8s.io` libraries stay on 0.36.4 and `sigs.k8s.io/controller-runtime` on
  0.24.1. 0.37.0 is available, and is what controller-runtime 0.25.0 requires,
  but cert-manager 1.21.2 still pins 0.36.2; moving apimachinery ahead of what
  cert-manager's webhook server was built against is the mismatch this project
  avoids. It waits for a cert-manager release that leads.
- `docker/setup-qemu-action` moves from v4.2.0 to v4.3.0 in the release
  workflow. Action-internal dependency bumps only.

## 2.0.6

Security release. No functional changes to the solver; the rendered manifests
are unchanged from 2.0.5.

### Security

- `google.golang.org/grpc` 1.83.2, clearing CVE-2026-84304
  (GHSA-vp52-pcj8-j9qc, HIGH). gRPC-Go buffered every fragmented HTTP/2 DATA
  frame as its own `recvMsg`, so a stream of one-byte frames could exhaust
  heap while staying inside the connection and stream flow-control windows.
  **Deployments running 2.0.5 were not exposed**: the vulnerable transport is
  linked in only through the apiserver's etcd client, and this webhook opens
  no gRPC connections, so nothing ever reaches that code. It failed the source
  and image scan gates all the same. `govulncheck` did not report it — the Go
  vulnerability database carries the advisory under its GHSA identifier with
  no `GO-` entry yet, which is worth remembering the next time a clean
  `govulncheck` run is read as an all-clear.
- `golang.org/x/crypto` 0.56.0, clearing CVE-2026-56855 and CVE-2026-78662,
  both denial of service in `x/crypto/ssh` channel handling. Neither was
  reachable: `x/crypto` enters through `cryptobyte`, and `x/crypto/ssh` is not
  linked into the binary at all.

grpc is a transitive dependency of cert-manager, which at v1.21.1 — the latest
release — still pins the vulnerable 1.82.1, so the bump is an explicit
promotion into the indirect block rather than a parent bump. It can be dropped
once a cert-manager release carries 1.83.1 or later.

## 2.0.5

Metadata release. The webhook binary and the rendered manifests are unchanged
from 2.0.3.

### Added

- A chart README. Artifact Hub renders the README packaged inside the chart,
  and there was none, so the listing showed only the metadata block and no
  documentation at all. It covers installation from both the Helm repository
  and the OCI registry, the credential Secret and the per-domain API access
  setting, an Issuer and Certificate example, the solver config keys, every
  chart value, and the zone-versus-registered-domain behaviour. A unit test
  now asserts the file exists and is packaged.

### Fixed

- GitHub releases had stopped at 2.0.1: the release workflow never created
  them, and the ones for 2.0.0 and 2.0.1 had been cut by hand. `release.yaml`
  now creates the release for the pushed tag, with the packaged chart
  attached. The missing 2.0.2, 2.0.3 and 2.0.4 releases have been backfilled
  with the exact tarballs already published to the Helm repository.

## 2.0.4

Metadata release. The webhook binary and the rendered manifests are unchanged
from 2.0.3.

### Fixed

- `artifacthub.io/alternativeName` was set to `porkbun-webhook`, which Artifact
  Hub rejects because an alternative name has to be a substring or superstring
  of the package name. Registration failed for 2.0.2 and 2.0.3, so the Artifact
  Hub listing stayed on 2.0.1 and showed none of the metadata those releases
  carried. The annotation is removed; `porkbun` is already a keyword and the
  last label of the package name, so nothing is lost.
- Those two tarballs still embed the annotation and would fail on every
  Artifact Hub processing run, so they are skipped via `artifacthub-repo.yml`.
  Republishing them would change digests that are already public. Both remain
  installable from the Helm repository and from GitHub releases; only the
  Artifact Hub version history skips them.

### Added

- The Artifact Hub annotations are validated by a unit test. `helm lint` does
  not know these annotations and the only signal of a bad one is a line in a
  tracking log on artifacthub.io, which is how the above shipped twice. The
  test also checks that the image tag in `artifacthub.io/images` matches
  `appVersion`.
- `repositoryID` in `artifacthub-repo.yml`, which earns the Verified Publisher
  badge, and an Artifact Hub badge in the README.

## 2.0.3

Maintenance release. No functional changes to the solver.

### Security

- The image is now built on Go 1.27. This clears the six standard-library
  advisories `govulncheck` reported as reachable from the webhook —
  GO-2026-6218 (`net/url`), GO-2026-6091 (`html/template`), GO-2026-6090
  (`crypto/tls`), GO-2026-6089 (`net/http`), GO-2026-5972 (`encoding/asn1`)
  and GO-2026-5026 (`net/http` idna). **Deployments running 2.0.2 were not
  exposed to these**: that image shipped stdlib 1.26.6, which is already the
  fix line for all six. The advisories were outstanding against the source
  tree, not the published artifact.
- `golang.org/x/mod` 0.40.0, clearing CVE-2026-56864 and CVE-2026-56865
  (a malicious GOSUMDB or GOPROXY able to serve forged module content), with
  `x/tools` 0.49.0 following. Neither was reachable from the webhook binary —
  `x/mod` enters only through the conformance-test path, and the vulnerable
  code is `x/mod/sumdb`, which the `go` command executes rather than this
  solver.

### Changed

- The `k8s.io` libraries move to 0.36.4, a patch within the same API version.
  cert-manager stays at 1.21.1.

## 2.0.2

Maintenance release. No functional changes to the solver.

### Security

- `golang.org/x/net` 0.58.0, carrying a `dnsmessage` bounds fix, with
  `x/crypto` 0.55.0, `x/mod` 0.38.0, `x/text` 0.41.0 and `x/tools` 0.48.0
  following as its transitive minimums. No advisory was outstanding against
  2.0.1; this is a refresh, not a fix for a known-reachable vulnerability.
- The runtime base moves to `gcr.io/distroless/static-debian13:nonroot`,
  refreshing the CA bundle vintage. The image still contains only that bundle,
  `/etc/passwd` and tzdata.

### Changed

- The same `x/net` bump refreshes the public suffix list that registered-domain
  detection depends on, so newly delegated suffixes resolve correctly.

### Added

- Chart metadata for discoverability: keywords, maintainers, an icon, and the
  `artifacthub.io` category, images, links and changes annotations.
- A landing page served at the Helm repository URL, which was previously a 404
  in a browser. It carries the install instructions, an Open Graph card and
  schema.org metadata.

### Fixed

- The release job now has `artifact-metadata: write`, so image provenance
  attestations are stored rather than failing at the end of a successful build.

## 2.0.1

Security release. No functional changes.

### Fixed

- Dependency updates clearing two vulnerabilities that were reachable from the
  webhook binary: CVE-2026-56852 / GO-2026-5970 (infinite loop in
  `golang.org/x/text` normalization, HIGH) and GO-2026-5158 (uncapped baggage
  header parsing in `go.opentelemetry.io/otel`). Also picks up
  `cel-go` 0.29.0, clearing GHSA-gcjh-h69q-9w9g (not reachable from this
  webhook). Bumps cert-manager to 1.21.1 and the k8s.io modules to 0.36.3.

## 2.0.0

First release of this fork, based on
[`talinx/cert-manager-webhook-porkbun`](https://github.com/talinx/cert-manager-webhook-porkbun)
1.0.0 (last updated 2024-10-17).

The solver name (`porkbun`), the solver config keys (`apiKey`/`secretApiKey`)
and the chart's rendered resource names are unchanged, so existing Issuers and
Certificates keep working. The chart's defaults changed, which is why this is a
major version.

### Fixed

- Decoding a `null` or truncated API response no longer panics the webhook
  process. Responses were decoded into `**T` and dereferenced unchecked.
- Porkbun's `message` field is now parsed and reported. Every failure
  previously surfaced as `invalid status "ERROR"` with no detail.
- HTTP status codes are checked before the body is decoded, so a non-JSON error
  page is reported as an HTTP error rather than a decoding failure.
- The HTTP client now has a 30s timeout, and each Present/CleanUp is bounded to
  2 minutes. Previously a zero-value `http.Client` and `context.Background()`
  meant a stalled connection hung the solver indefinitely.
- Transient failures (429, 5xx, network errors, rate-limit messages) are
  retried with exponential backoff and full jitter.
- The challenge TXT record TTL defaults to 600 rather than the hard-coded 60,
  which is below Porkbun's minimum. Configurable via `ttl`, clamped up.
- Solver config is validated unconditionally. Validation was skipped when
  `AllowAmbientCredentials` was set, which cert-manager sets by default for
  ClusterIssuers.
- Subdomain splitting uses a case-insensitive label-boundary suffix match
  instead of `strings.Index`, which mis-split repeated label sequences.
- The registered domain is derived from the public suffix list, so challenges
  in delegated sub-zones reach the Porkbun API correctly. Overridable via
  `domain`.
- Record IDs are carried as opaque strings end to end, removing a latent 32-bit
  overflow.
- Concurrent challenges for the same domain are serialised, removing a
  read-modify-write race between a certificate's apex and wildcard challenges.
- A delete-failure error message reported the retrieve status instead of the
  delete status.
- Deleting an already-deleted record is treated as success.
- Path segments derived from configuration are validated and escaped.

### Security

- Secret access is a namespaced Role with `get` only, optionally restricted to
  named Secrets. It was a ClusterRole granting `get,watch,list` on Secrets in
  every namespace.
- The image is `distroless/static:nonroot` and runs as uid 65532 on port 8443.
  No shell, no package manager, and no `NET_BIND_SERVICE` capability.
- `readOnlyRootFilesystem`, all capabilities dropped and `RuntimeDefault`
  seccomp are chart defaults, compatible with the `restricted` Pod Security
  Standard.
- Dependencies updated to Go 1.26, cert-manager 1.21 and Kubernetes 1.36
  libraries, from Go 1.19 / cert-manager 1.11.3 / `golang.org/x/net` v0.7.0.
- Images are signed with cosign and carry SBOM and provenance attestations.
  The `latest` tag is no longer published.

### Added

- `flowcontrol` RBAC, required by every aggregated apiserver.
- Unit tests covering each fix above, and a build-tagged conformance suite.
- `values.schema.json`, rejecting invalid configuration at install time.
- Configurable `logLevel`, `extraArgs`, `podDisruptionBudget`,
  `topologySpreadConstraints`, `priorityClassName`, image digest pinning.
- CI: race tests, golangci-lint, chart rendering and schema validation, image
  smoke tests, and Trivy scans of both source and image — re-run weekly so a
  dependency that goes bad after release is caught.

### Changed

- Default image is `ghcr.io/octabits-io/cert-manager-webhook-porkbun`.
- Default container port is 8443. The Service still listens on 443.
- Resource requests and limits now have defaults.
- The serving certificate has an explicit `renewBefore` and uses ECDSA keys.
