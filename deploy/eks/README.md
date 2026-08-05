# deploy/eks — Kubernetes (DOKS / EKS) deployment (future)

Placeholder for the **Kubernetes** deployment manifests / Helm chart, paired with
[`infra/terraform/eks`](../../infra/terraform/eks/README.md). The droplet stage in
[`deploy/do`](../do) ships today; k8s is the next stage and is **additive**.

## What lands here when built

```
eks/
  helm/nexus/            # or raw manifests/kustomize overlays
    values-staging.yaml
    values-production.yaml
  README.md
```

Key differences from the compose profiles:

- **Ingress/TLS** — an ingress-gateway (Caddy ingress or ingress-nginx +
  cert-manager) replaces the single Caddy container in `deploy/do`.
- **Sandbox create path** — the `nexus-sandboxd` host daemon becomes an
  RBAC-scoped ServiceAccount (spec `FR-059`, task `T105c`); the app never holds a
  container-runtime socket.
- **Stateful deps** — Postgres / Redis / OpenBao / Casdoor become managed services
  or in-cluster StatefulSets.
- **Images** — the same Docker Hub images CI already publishes; only the scheduler
  and networking change.

CI is not wired to this directory yet — add a deploy job when the manifests exist.
