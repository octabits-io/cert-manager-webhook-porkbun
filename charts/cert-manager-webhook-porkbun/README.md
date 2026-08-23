# cert-manager-webhook-porkbun

A [cert-manager](https://cert-manager.io/) ACME **DNS-01** solver webhook for
domains hosted at [Porkbun](https://porkbun.com/), which cert-manager has no
built-in solver for. Install this chart, point an Issuer at it, and Let's
Encrypt will issue wildcard certificates for your Porkbun domains on
Kubernetes — DNS-01 is the only challenge type that can.

This is a maintained fork of
[`Talinx/cert-manager-webhook-porkbun`](https://github.com/Talinx/cert-manager-webhook-porkbun).
The solver name, solver config keys and rendered resource names are unchanged,
so existing Issuers need no edits. The full list of fixes over upstream is in
the [repository README](https://github.com/octabits-io/cert-manager-webhook-porkbun#what-changed-from-upstream).

## Requirements

- Kubernetes with the aggregation layer enabled (the webhook registers an
  `APIService`).
- [cert-manager](https://cert-manager.io/docs/installation/) already installed.
  Built against cert-manager 1.21 and the Kubernetes 1.36 client libraries.
- A Porkbun API key, with API access enabled on each domain.

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

The chart is also published as an OCI artifact:

```bash
helm install cert-manager-webhook-porkbun \
  oci://ghcr.io/octabits-io/charts/cert-manager-webhook-porkbun \
  --namespace cert-manager --set groupName=acme.example.com
```

Pass `--version` to pin a chart version; OCI installs resolve to the newest
otherwise.

## Credentials

Create an API key at <https://porkbun.com/account/api>, then:

```bash
kubectl -n cert-manager create secret generic porkbun-api-credentials \
  --from-literal=api-key=pk1_xxx \
  --from-literal=secret-key=sk1_xxx
```

> **API access is disabled per domain by default.** Enable it under
> Domain Management → Details → API Access for each domain you want
> certificates for. Without it the API returns a generic error that does not
> mention the setting, which makes this the most common cause of a failing
> setup.

For a `ClusterIssuer`, the Secret must live in cert-manager's
`--cluster-resource-namespace` (`cert-manager` by default). For a namespaced
`Issuer`, it lives in the Issuer's namespace — add that namespace to
`rbac.secretAccess.namespaces`.

## Issuer

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

These keys go under `dns01.webhook.config` in the Issuer, not in the chart's
values.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `apiKey` | SecretKeySelector | required | Secret holding the Porkbun API key. |
| `secretApiKey` | SecretKeySelector | required | Secret holding the Porkbun secret API key. |
| `apiKeySecretRef` | SecretKeySelector | — | Alias for `apiKey`, matching cert-manager's naming convention. |
| `secretKeySecretRef` | SecretKeySelector | — | Alias for `secretApiKey`. |
| `domain` | string | derived | Pin the registered domain to operate on. Only needed if the automatic derivation is wrong — see [Zones vs. registered domains](#zones-vs-registered-domains). |
| `ttl` | int | `600` | TTL of the challenge TXT record, in seconds. Values below the Porkbun minimum are clamped up, never rejected. |

## Chart values

Every value is documented inline in
[`values.yaml`](https://github.com/octabits-io/cert-manager-webhook-porkbun/blob/main/charts/cert-manager-webhook-porkbun/values.yaml)
and enforced at install time by
[`values.schema.json`](https://github.com/octabits-io/cert-manager-webhook-porkbun/blob/main/charts/cert-manager-webhook-porkbun/values.schema.json).

| Key | Default | Description |
| --- | --- | --- |
| `groupName` | `acme.example.com` | API group the webhook serves. **Always set this.** |
| `certManager.namespace` | `cert-manager` | Namespace cert-manager runs in. |
| `certManager.serviceAccountName` | `cert-manager` | ServiceAccount cert-manager calls the webhook with. |
| `image.repository` | `ghcr.io/octabits-io/cert-manager-webhook-porkbun` | Image repository. |
| `image.tag` | `""` | Defaults to the chart's `appVersion`. |
| `image.digest` | `""` | Pin the image by digest instead of tag. Takes precedence over `tag`. |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | |
| `replicaCount` | `1` | |
| `securePort` | `8443` | Unprivileged container port. The Service still exposes 443. The schema rejects 443 here. |
| `logLevel` | `0` | klog verbosity. `2` also logs zone-resolution decisions. Capped at 6 by the schema. |
| `extraArgs` | `[]` | Extra webhook command-line arguments. |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | `""` | Defaults to the chart's fullname. |
| `serviceAccount.annotations` | `{}` | |
| `rbac.secretAccess.scope` | `Namespaced` | `Namespaced` or `Cluster`. `Cluster` grants Secret reads in every namespace. |
| `rbac.secretAccess.namespaces` | `[]` | Extra namespaces to grant Secret reads in, on top of the release namespace. |
| `rbac.secretAccess.secretNames` | `[]` | Restrict access to specific Secret names. Recommended. |
| `service.type` | `ClusterIP` | |
| `service.port` | `443` | |
| `resources` | `10m` / `32Mi` requests, `128Mi` limit | No CPU limit on purpose: throttling here only slows issuance. |
| `podSecurityContext` | non-root, uid 65532, `RuntimeDefault` | |
| `securityContext` | no privilege escalation, read-only root, all capabilities dropped | |
| `podDisruptionBudget.enabled` | `false` | Enable when running more than one replica. |
| `pki.rootCA.duration` | `43800h` | Lifetime of the webhook's self-signed root. |
| `pki.serving.duration` / `pki.serving.renewBefore` | `8760h` / `720h` | Lifetime of the webhook's serving certificate. |
| `nodeSelector`, `tolerations`, `affinity`, `topologySpreadConstraints`, `priorityClassName` | empty | Standard scheduling controls. |

## Zones vs. registered domains

The Porkbun API is addressed by **registered domain**, but cert-manager hands
the solver a **DNS zone** derived from an SOA lookup. These are usually the
same, and diverge when the challenge name sits in a delegated sub-zone: a zone
of `sub.example.com` is not something Porkbun's API will accept.

The solver therefore derives the registered domain from the challenge FQDN
using the public suffix list, which gives the same answer as the zone in the
common case and the correct one otherwise. Set `domain` in the solver config to
override it, and `logLevel: 2` to see what it decided.

## Security

- The webhook is granted `get` on Secrets only, through a namespaced Role by
  default, and optionally restricted to named Secrets. It never lists or
  watches them.
- The image is `distroless/static-debian13:nonroot` — no shell, no package
  manager — running as uid 65532 on an unprivileged port, with
  `readOnlyRootFilesystem`, all capabilities dropped and a `RuntimeDefault`
  seccomp profile. Compatible with the `restricted` Pod Security Standard.
- `logLevel` is capped at 6, because client-go logs full API response bodies at
  v≥8, which would write the credential Secret into pod logs.
- Images are multi-arch (`linux/amd64`, `linux/arm64`), signed with cosign, and
  carry SBOM and provenance attestations. `latest` is deliberately not
  published.
- The webhook mints its own serving chain via a self-signed cert-manager
  Issuer; nothing needs to be supplied.

## Upgrading

Chart versions follow the image version. Upgrading swaps the Deployment;
in-flight challenges are retried by cert-manager and existing certificates are
unaffected, since nothing re-issues on upgrade.

```bash
helm repo update
helm upgrade cert-manager-webhook-porkbun octabits/cert-manager-webhook-porkbun \
  --namespace cert-manager --reuse-values
```

Migrating from `talinx/cert-manager-webhook-porkbun` needs no Issuer changes;
see the
[migration notes](https://github.com/octabits-io/cert-manager-webhook-porkbun#migrating-from-talinxcert-manager-webhook-porkbun).

## Uninstalling

```bash
helm uninstall cert-manager-webhook-porkbun --namespace cert-manager
```

Remove or repoint any Issuer that references the solver first, or its
challenges will fail. Certificates already issued keep working.

## Links

- [Source and full documentation](https://github.com/octabits-io/cert-manager-webhook-porkbun)
- [Changelog](https://github.com/octabits-io/cert-manager-webhook-porkbun/blob/main/CHANGELOG.md)
- [Issues](https://github.com/octabits-io/cert-manager-webhook-porkbun/issues)
- [Container image](https://github.com/octabits-io/cert-manager-webhook-porkbun/pkgs/container/cert-manager-webhook-porkbun)

Apache License 2.0. This chart is derived from
[`cert-manager/webhook-example`](https://github.com/cert-manager/webhook-example)
by way of `Talinx/cert-manager-webhook-porkbun`; see
[NOTICE](https://github.com/octabits-io/cert-manager-webhook-porkbun/blob/main/NOTICE).
