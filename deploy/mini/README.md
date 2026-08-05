# Nexus Agent — MINI single-tenant profile

The **smallest honestly-production-shaped deployment of the full product** for a
single, trusted small-company tenant whose users log in through **Casdoor**
(OIDC). It runs the *same binaries* as the SaaS topology — this is a
**configuration collapse, not a fork** — so a customer started on this profile can
be migrated to the full split topology later without a rewrite.

> **Repo status:** this repository is currently at the spec stage. The
> [`docker-compose-mini.yml`](docker-compose-mini.yml) `build:` contexts and the
> `Dockerfile` are **target-state** — they describe the topology the
> implementation is built to satisfy. Swap `build:` for a published `image:` once
> the binaries ship.

---

## Why this profile exists

Earlier questions asked whether the platform can serve a small Casdoor customer on
"one or two containers." The full product (all 170 FRs) cannot honestly collapse to
one or two, but it *can* collapse from ~9 services to **~5 standing services +
Casdoor** — every step below is explicitly permitted by a port, contract, or
documented alternate in the spec.

## What was collapsed (and where the spec permits it)

| Collapse | Permitted by | Tradeoff you accept |
|---|---|---|
| `control-plane` + `surface-gateway` + **embedded worker pool** → **one binary** | [plan.md](../../specs/001-agent-platform/plan.md) — the control/data-plane *contract* ships day one; the **physical** split is deferred "until a customer requires BYOC … the split is a deployment change." They already share `kernel/` + `internal/`. | Keep the **package** boundary in source so the split stays a deployment change, not a refactor. Runtime execution remains queue-driven through an internal worker pool in the same process. |
| **NATS JetStream → Redis Streams** (`NEXUS_QUEUE_ADAPTER=redis`) | [research.md §4](../../specs/001-agent-platform/research.md) — the queue is an abstract port and "SQS/**Redis Streams**/Temporal-class remain drop-in alternates behind the same port." | "Weaker native pub/sub fan-out … and an extra hop." Negligible at one worker / one tenant. Requires Redis **AOF on** so runs stay durable (FR-024). |
| **Python stays runtime-separate** (`NEXUS_COMPACTION=remote_python`) | [plan.md](../../specs/001-agent-platform/plan.md) and [research.md §6/§8](../../specs/001-agent-platform/research.md) — condenser is a helper model service off the paying loop. | One extra service vs a fully in-process path, but better parity with the designed architecture. Evals + judge remain CI/offline workloads. |
| **Object storage → local filesystem** (`NEXUS_OBJECT_STORE=filesystem`) | Object storage exists only to offload *oversized* tool outputs referenced from the event log. | Not durable/replicated like S3. Fine for low-volume single tenant; back it with a real bucket or one MinIO if artifacts grow. |
| **gVisor → `runc`** (`NEXUS_SANDBOX_RUNTIME=runc`) | [research.md §5](../../specs/001-agent-platform/research.md) — "a shared kernel suffices for single-tenant BYOC." | Loses escape-resistance vs a hostile tenant. Acceptable **only** because this profile is a single *trusted* tenant. Set `runsc` on a hardened host to restore it. |
| **NATS removed; worker pool embedded in the app + condenser separate** | all of the above | ~9 services → ~5 + Casdoor. |

## What you must NOT collapse (the trust-surface floor)

These stay on at full completion because they *are* the product — dropping them
produces a different, unsellable system:

- **PostgreSQL** — the append-only event log, hash-chained audit, and row-level
  tenant scoping. Not a cache.
- **Redis (AOF on)** — atomic budget-reservation counters and session-serial locks
  (FR-083) must be atomic primitives. In this profile it *also* carries the queue.
- **Secrets/KMS seam (OpenBao)** — provider creds never in prompt (FR-034),
  per-tenant envelope encryption + sign-only audit key (FR-080/FR-081). May be
  lightweight, but must be present.
- **Linux host** — per-run sandboxes under `runc`/`runsc`, with a small warm pool in the mini profile rather than a full enterprise pool.

## The one thing you must NOT collapse: sandbox orchestration

The `internal/sandbox/` package (warm pool, TTL/reclamation, resource limits,
runtime selection) is not a separate service — it runs inside the `nexus-control`
process ([plan.md file tree](../../specs/001-agent-platform/plan.md)). What the
spec makes **normative** is a boundary:
[FR-059 constraint (a)](../../specs/001-agent-platform/spec.md) says the sandbox
orchestrator MUST NOT hold a container-runtime socket from inside a container
(socket access is root-equivalent on the host; research.md §5, the OpenClaw OC-13
shape), and constraint (b) requires every create argument (mounts, network mode,
capabilities, security options) be validated against an allowlist and never
interpolated from config or any model-derived value. On Kubernetes the spec names
one concrete create path: an RBAC-scoped ServiceAccount (task T105c).

**Single-host decision (`nexus-sandboxd`).** For this Docker profile the create
path is a minimal-privilege **host daemon** — `nexus-sandboxd`, run as a systemd
unit **directly on the host, not as a container** — that holds the only handle to
the container runtime (`runc`/`runsc`) and exposes a narrow, allowlisted
create/destroy RPC over a Unix socket (`/run/nexus/sandboxd.sock`). `nexus-control`
is given that socket read-only (`SANDBOX_BROKER_SOCKET` in `.env`) and calls only
its bounded verbs; it never sees the container-runtime socket. This is the exact
single-host analogue of the Kubernetes ServiceAccount: a privilege-separated
create identity instead of a root-equivalent socket, so FR-059(a) holds and the
broker enforces the FR-059(b) create-argument allowlist (shared with `create_args.go`,
task T119a). The one thing that creates containers is the one thing that cannot
live in a container. Do **not** "fix" a provisioning error by bind-mounting
`/var/run/docker.sock` into the app — that reintroduces exactly the escape class
the broker exists to prevent.

---

## Resulting topology

```
nexus-control   <- control-plane + surface gateway + embedded worker pool (1 binary)
nexus-ml-python <- separate condenser helper (runtime)
postgres        <- event log + audit + config + RLS
redis (AOF)     <- cache + locks + budget counters + queue + event fan-out
openbao         <- secrets / envelope keys / sign-only audit key
casdoor         <- external OIDC IdP (configuration only)
sandboxes       <- small warm pool, on the host (created via nexus-sandboxd broker)
```

**Approximately 5 standing services + Casdoor**, single Linux VM, one compact runtime with an internal worker pool.

## Quick start

```bash
cd deploy/mini
cp .env.example .env          # then edit secrets (see below)
docker compose -f docker-compose-mini.yml up -d
docker compose -f docker-compose-mini.yml ps
```

### Configure Casdoor (once)

1. Open the Casdoor console at `http://localhost:8000` and sign in
   (default dev admin `admin` / `123`).
2. Create (or reuse) an **Application** for Nexus and note its **Client ID** and
   **Client Secret**; add a redirect URI for your surface if you use the web UI.
3. Put the issuer + client values in `.env`:

   ```dotenv
   OIDC_ISSUER=http://casdoor:8000
   OIDC_CLIENT_ID=nexus-agent
   OIDC_CLIENT_SECRET=<from Casdoor>
   ```

4. Restart the control plane after OIDC changes:

  ```bash
  docker compose -f docker-compose-mini.yml up -d nexus-control
  ```

### Seed the model-provider credential (into the vault, never env/prompt)

```bash
# example against OpenBao dev mode
export BAO_ADDR=http://localhost:8200
export BAO_TOKEN=dev-root-token
bao kv put secret/nexus/providers/anthropic api_key=sk-...   # FR-034: read at tool-exec time
```

### Submit a run

```bash
# obtain a token from Casdoor for a Nexus user, then:
curl -sX POST localhost:8080/v1/runs \
  -H "Authorization: Bearer <casdoor-oidc-token>" \
  -d '{"agent_id":"<id>","input":"triage this bug","data_label":"internal"}'
```

## Environment switches (the collapse knobs)

| Variable | Mini value | Meaning |
|---|---|---|
| `NEXUS_ALL_IN_ONE` | `true` | run control-plane + embedded worker pool in one process |
| `NEXUS_QUEUE_ADAPTER` | `redis` | queue port → Redis Streams (not NATS) |
| `NEXUS_WORKER_POOL_SIZE` | `4` | number of internal workers in the mini binary |
| `NEXUS_COMPACTION` | `remote_python` | compaction delegated to the `nexus-ml-python` service |
| `NEXUS_OBJECT_STORE` | `filesystem` | local artifact volume (no S3/MinIO) |
| `NEXUS_WARM_POOL_SIZE` | `1` | small warm sandbox pool for the mini host |
| `NEXUS_SANDBOX_RUNTIME` | `runc` | trusted single tenant (`runsc` = gVisor) |

## Scaling back up (nothing here is a dead end)

Because every collapse is a config value on a stable port, growth is additive:

- More load → increase `NEXUS_WORKER_POOL_SIZE` first, then split the worker
  pool into dedicated worker replicas or a separate deployment profile when the
  workload justifies it.
- Hostile multi-tenancy → `NEXUS_SANDBOX_RUNTIME=runsc` (gVisor) or Kata on nodes
  with hardware virtualization; per-tenant RuntimeClass is already a shipped seam.
- BYOC → physically split the control/data plane (the contract already exists).
- Real artifact volume → point `NEXUS_OBJECT_STORE` at S3/MinIO.

None of these migrate the event log, the audit chain, or the encryption model —
that is the whole reason the seams ship early.
