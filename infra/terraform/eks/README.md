# Terraform — EKS / DOKS (future)

This directory is a deliberate placeholder for the **Kubernetes** deployment stage.
The DigitalOcean droplet stage lives in [`../do`](../do) and ships today; k8s is an
**additive module**, not a rewrite — the root there is structured so this can slot
in without reworking the droplet path.

## When this gets built

Provision managed Kubernetes (DOKS first, EKS when we move to AWS) plus:

- node pools sized for the warm sandbox pool,
- an ingress/gateway (Caddy or an ingress-nginx + cert-manager pair) replacing the
  single-host Caddy container,
- the `nexus-sandboxd` privilege-separation boundary re-expressed as an
  RBAC-scoped ServiceAccount create path (spec `FR-059`, task `T105c`) instead of
  the single-host host daemon used by the compose profiles,
- the same Postgres / Redis / OpenBao / Casdoor dependencies as managed services
  or in-cluster StatefulSets.

## Layout when implemented

```
eks/
  versions.tf providers.tf variables.tf outputs.tf
  cluster.tf node_pools.tf
  ingress.tf            # gateway + TLS (cert-manager / Caddy ingress)
  environments/{staging,production}.tfvars
```

State backend stays DigitalOcean Spaces (S3-compatible), keyed per environment —
identical to the `do/` root — so nothing about the state story changes.

> Nothing here is wired into CI yet. The `terraform.yml` workflow points at
> `infra/terraform/do`; add a matrix entry here when this stage is implemented.
