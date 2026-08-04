# Changelog

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
