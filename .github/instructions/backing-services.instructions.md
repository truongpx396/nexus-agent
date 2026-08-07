---
description: 'Production practices for backing services: PostgreSQL, Redis, NATS JetStream, Qdrant, MinIO/S3, Casdoor, and Caddy. Configuration, resilience, security, and operational standards for the AISAT-STUDIO stack.'
applyTo: '**/*.sql,**/migrations/**,**/Caddyfile,**/Caddyfile.*,**/*.Caddyfile,**/docker-compose*.yml,**/docker-compose*.yaml,**/compose*.yml,**/compose*.yaml,**/*.env,**/.env.*,**/k8s/**,**/deploy/**,**/config/**,**/*natsstore*,**/*jetstream*,**/*qdrant*,**/*redis*,**/*casdoor*'
---

# Backing Services — Production Practices

Standards for provisioning, configuring, and operating the backing services used by
AISAT-STUDIO: **PostgreSQL** (with RLS), **Redis**, **NATS JetStream**, **Qdrant**,
**MinIO / S3**, **Casdoor** (OIDC), and **Caddy** (edge). These rules apply to
migrations, infra config, deployment manifests, and any application code that connects
to these services.

Complements [security-and-owasp.instructions.md](./security-and-owasp.instructions.md)
(secrets, TLS, headers), [go.instructions.md](./go.instructions.md), and
[python.instructions.md](./python.instructions.md).

## Cross-Cutting Principles

Apply these to **every** backing service before service-specific rules:

- **No hardcoded credentials.** All connection strings, passwords, and keys come from
  environment variables or a secret manager — never committed. See secrets anti-patterns
  S1–S4.
- **TLS in transit.** Encrypt every connection between app and service in staging/prod
  (Postgres `sslmode=verify-full`, `rediss://`, NATS TLS, HTTPS to Qdrant/MinIO/Casdoor).
  Plaintext is acceptable only on an isolated local dev network.
- **Least privilege.** Each service gets a dedicated, scoped account — never the admin/root
  user for app traffic. One database role, one Redis ACL user, one NATS account per bounded
  concern.
- **Pinned versions.** Reference exact image tags (`postgres:16.4`, not `postgres:latest`)
  so deployments are reproducible. Renovate/Dependabot handles upgrades.
- **Health, readiness, and liveness.** Every service the app depends on must have a probe.
  The app's `/readyz` fails when a required backing service is unreachable; `/livez` reflects
  only in-process health. Never mark ready while a dependency is down.
- **Bounded resources.** Set explicit connection-pool sizes, timeouts, memory limits, and
  disk quotas. An unbounded pool or queue is a latent outage.
- **Idempotency at the data layer.** Leases, queue groups, and locks are throughput
  optimizations. The only correctness guarantee is a unique constraint / conditional write
  in the datastore itself.
- **Graceful shutdown.** On SIGTERM, stop accepting new work, drain in-flight requests,
  close pools cleanly, and unsubscribe consumers before exit.

---

## PostgreSQL

Primary system of record. Uses Row-Level Security (RLS) for workspace isolation, partitioned
tables by `created_at`, and a primary + read-replica topology.

### Connections & Pooling

- **Front the database with a pooler (PgBouncer) in transaction mode.** Go/Python app pools
  are sized *per replica*; without a pooler, `replicas × pool_size` can exhaust
  `max_connections`. Formula: keep total app connections well under
  `max_connections − superuser_reserved_connections − replication slots`.
- Set explicit pool bounds and lifetimes in the app:
  - Go (`pgxpool`): `MaxConns`, `MinConns`, `MaxConnLifetime` (e.g. 30m),
    `MaxConnIdleTime`, `HealthCheckPeriod`.
  - Python (`asyncpg`/SQLAlchemy): `min_size`, `max_size`, `max_inactive_connection_lifetime`,
    `command_timeout`.
- Always set a **statement timeout** (`SET statement_timeout` / `options=-c statement_timeout=...`)
  and `idle_in_transaction_session_timeout` so a stuck query cannot hold a connection forever.
- Use `sslmode=verify-full` with a pinned CA in staging/prod.
- **PgBouncer + prepared statements:** transaction-mode pooling breaks server-side prepared
  statements. Use `pgxpool` with `statement_cache_capacity=0` / simple-protocol, or
  asyncpg `statement_cache_size=0`, or point migrations at the direct port (bypass PgBouncer).

### Row-Level Security (RLS)

- Enable `FORCE ROW LEVEL SECURITY` on every tenant-scoped table so even the table owner is
  subject to policies.
- The app role must **not** have `BYPASSRLS`. Only migration/admin tooling may bypass, and
  only via a separate role.
- Set tenant context per request with `SET LOCAL app.workspace_id = ...` inside the
  transaction — `SET LOCAL` (not `SET`) so it resets when the transaction ends and cannot
  leak across pooled connections.
- Policies read `current_setting('app.workspace_id', true)`; the `true` (missing_ok) prevents
  errors but a policy must treat NULL context as "deny all", never "allow all".

```sql
-- GOOD — force RLS, deny when context is unset
ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE documents FORCE ROW LEVEL SECURITY;

CREATE POLICY workspace_isolation ON documents
  USING (workspace_id = current_setting('app.workspace_id', true)::uuid);
```

### Migrations

- Migrations are **forward-only and reversible in intent**: additive first, destructive later.
  Never `DROP` a column in the same release that stops writing to it — use expand/contract.
- Use `CREATE INDEX CONCURRENTLY` and `ALTER TABLE ... ADD COLUMN` with defaults that don't
  rewrite the table (Postgres 11+ handles constant defaults cheaply).
- Wrap DDL in transactions where supported, but `CONCURRENTLY` cannot run in a transaction —
  isolate those migrations.
- Take an explicit `lock_timeout` before DDL on hot tables so a migration fails fast instead
  of queueing behind long reads and blocking all traffic.
- Every migration is checked into version control, applied in CI, and re-runnable (`IF NOT
  EXISTS` / idempotent where feasible).

### Schema & Performance

- Partition large, time-series tables (`created_at`) and attach a retention/detach job for
  old partitions rather than mass `DELETE`.
- Add a UNIQUE constraint for every idempotency key (`idem_key`) — this is the correctness
  backstop for credit ledger and event processing, not the queue.
- Route read-only, replica-safe queries to the read replica; keep the primary for writes and
  read-after-write consistency. Guard against replica lag for flows that read their own writes.
- Index foreign keys and RLS predicate columns (`workspace_id`). Avoid `SELECT *`.

### Backups & DR

- Continuous WAL archiving + point-in-time recovery (PITR); test restores on a schedule — an
  untested backup is not a backup.
- Document RPO/RPO targets; monitor replication lag and backup freshness with alerts.

---

## Redis

Used for credit balances, LangGraph checkpoints, cache, rate limiting, the billing outbox,
opaque session records, and SSE pub/sub. Correctness-critical data lives here, so treat it as
more than a cache.

### Topology & Persistence

- Separate concerns by logical DB or key prefix, and know each key's durability class:
  - **Durable** (credit balances, outbox, sessions, checkpoints): enable AOF
    (`appendfsync everysec`) so a restart doesn't lose committed state.
  - **Ephemeral** (cache, rate-limit counters): TTL-bounded; loss is tolerable.
- For HA, run **Redis Sentinel or Cluster** — a single node is a single point of failure for
  billing and sessions. Clients must reconnect through Sentinel/Cluster topology, not a fixed IP.
- Enable `rediss://` TLS and require AUTH (ACL user with a scoped command set) in staging/prod.

### Correctness & Atomicity

- Mutate shared counters atomically: `INCRBY`/`DECRBY`, or a **Lua script**/`MULTI`+`WATCH`
  for read-modify-write. Never `GET` then `SET` a balance across two round-trips.
- Credit deduction is `DECRBY` (fast path) backed by the Postgres ledger + outbox; Redis is
  the accelerator, Postgres is the source of truth. On divergence, Postgres wins
  (hourly reconcile).
- Use `SET key val NX EX <ttl>` for locks/idempotency guards, and always set a TTL so a
  crashed holder cannot deadlock. Release with a compare-and-delete Lua script (check the
  token you own before `DEL`).

### Keys, Memory & Eviction

- **Every key has a TTL** unless it is intentionally durable state. Unbounded key growth is
  the most common Redis outage.
- Set `maxmemory` and choose an eviction policy deliberately:
  `allkeys-lru`/`volatile-lru` for caches; **`noeviction`** for instances holding durable
  billing/session data (evicting a balance is data loss — fail the write instead).
- Never store durable and evictable data on the same instance with an `allkeys-*` policy.
- Namespace keys (`session:{hash}`, `credit:{ws}`, `ratelimit:{ws}:{route}`); avoid `KEYS` in
  production — use `SCAN` with a cursor.

### Sessions

- Session records are opaque server-side state: `session:{sha256(session_id)}` with user/ws/
  role/clearance/csrf and a TTL. The cookie carries only the random reference, never claims.
- Deleting the Redis key = instant revocation (logout/demotion). Re-read clearance every
  request; don't cache authorization decisions past the session lookup.

---

## NATS JetStream

Async event bus. Uses **durable pull consumers** with **per-subject queue groups** (not core
NATS) so events survive restarts and scale horizontally. DLQs exist for `ingestion.dlq` and
`notify.email.dlq`.

### Streams & Consumers

- Every subject that must not lose messages is backed by a **JetStream stream** with an
  explicit retention policy (`limits`/`work-queue`/`interest`), `max_age`, `max_bytes`, and
  `storage=file` for durability.
- Use **durable pull consumers** with a queue/deliver group so N worker pods share the load and
  exactly one processes each message. Set:
  - `max_ack_pending` — bounds in-flight, unacked messages (backpressure). Without it a slow
    consumer silently accumulates redelivery pressure.
  - `ack_wait` — redelivery timeout; must exceed the realistic processing time (including
    long-horizon agent runs) or messages redeliver mid-flight.
  - `max_deliver` — cap redeliveries, then route to a DLQ subject. Infinite redelivery of a
    poison message is an outage.
- Long-running consumers send **`InProgress` (AckProgress)** heartbeats (~10s) to extend
  `ack_wait` while work is legitimately in flight; a janitor re-queues genuinely stuck runs.

### Delivery Semantics

- JetStream is **at-least-least-once** — consumers **must be idempotent**. Deduplicate with the
  message `Nats-Msg-Id` (publisher-set) and/or a data-layer unique key. The queue group is not
  the correctness guarantee; the idempotent write is.
- Ack **after** successful processing, not before. Use `AckExplicit`. On a handled failure,
  `Nak` with a backoff delay; on an unrecoverable error, `Term` and publish to the DLQ.
- Scheduled/background work is single-owner: an external k8s CronJob publishes a tick subject
  (`billing.reconcile.tick`, `agent.janitor.tick`, `usage.matview.refresh`) → one queue-group
  worker claims it via a data-layer atomic guard. No in-process `time.Ticker` in request tiers.

### Operations

- Monitor consumer **pending/ack-pending/redelivered** counts and DLQ depth; alert on growth.
- Provision stream `max_bytes`/`max_age` so disk cannot fill; a full JetStream store rejects
  publishes and stalls producers.
- DLQ messages are inspectable and replayable; every DLQ has a documented drain/replay runbook.

---

## Qdrant

Vector store for hybrid (dense + sparse) retrieval. Uses **payload-isolated shared
collections** with a documented re-shard/replication trigger.

### Isolation & Data Model

- Enforce workspace isolation with a **mandatory payload filter** (`workspace_id`) on **every**
  query — never rely on the caller to add it. Wrap Qdrant access in a repository that injects
  the tenant filter, the same discipline as Postgres RLS.
- Create a **payload index** on `workspace_id` (and other filtered fields) so filtered search
  stays fast; unindexed payload filters degrade to full scans.
- Pin the distance metric and vector dimensions to the embedding model; a mismatch silently
  corrupts recall. Park embeddings that don't match to a DLQ rather than mixing models in a
  collection.

### Reliability & Performance

- Upserts are idempotent by point ID (deterministic from `chunk_id`) so re-ingestion replaces
  rather than duplicates.
- Set HNSW parameters deliberately (`m`, `ef_construct`, search-time `ef`/`hnsw_ef`) and tune
  for the recall/latency target; document the chosen values.
- Consider **scalar/product quantization** + `on_disk` payload/vectors when memory-bound; measure
  the recall impact before enabling.
- Run with **replication** (`replication_factor ≥ 2`) and enough shards for the collection in
  prod; a single-replica collection has no HA. The re-shard/replication trigger is documented —
  act before saturation, not after.
- Use snapshots for backup; batch upserts (`wait=false` for throughput, `wait=true` when the
  caller needs read-after-write) and cap batch size.
- Secure the API with an API key + TLS; never expose Qdrant directly to the public internet.

---

## MinIO / S3

Object storage for uploads. Uses **direct-to-storage presigned uploads** to keep payloads off
the app servers.

### Access & Security

- App uses a **scoped IAM/access key** limited to the specific bucket(s) and actions it needs —
  never root/admin credentials.
- **Block all public access** by default. Buckets are private; objects are served via
  short-lived presigned GET URLs or an authenticated proxy, never public ACLs.
- Enable **server-side encryption** (SSE-S3 / SSE-KMS) and **TLS** for all requests.
- Presigned upload URLs are **short-lived** (minutes) and **constrained**: pin
  `Content-Type`, enforce a max `Content-Length`, and scope the key prefix to the workspace
  (`{workspace_id}/...`). Validate the object server-side after upload (size, MIME, virus scan)
  before marking it usable — a presigned PUT trusts the client until you verify.

### Data Management

- Enable **versioning** on buckets holding user data to protect against overwrite/delete, plus
  **lifecycle rules** to expire incomplete multipart uploads and transition/expire old versions.
- Use deterministic, tenant-prefixed keys; never put untrusted user input directly in a key
  path without sanitization (path traversal / key injection).
- For large files use multipart upload; set a lifecycle rule to abort orphaned multipart
  uploads (they cost storage silently).
- Replicate across zones/regions for DR per the RPO target; test restore/read from replica.

---

## Casdoor (OIDC / Identity)

Identity provider for browser auth. Browser flow is **OIDC Authorization Code + PKCE (S256)**;
the BFF verifies `id_token` via JWKS.

### Protocol & Verification

- Browser login uses **Authorization Code + PKCE (S256)** — never the implicit flow, never a
  client secret in the SPA.
- Always send and validate the **`state`** parameter (CSRF) and the PKCE `code_verifier`/
  `code_challenge`; reject callbacks with a missing or mismatched `state`.
- The BFF verifies every `id_token` against Casdoor's **JWKS**: check signature with the
  expected algorithm (`RS256`/`ES256`, never `none`), and validate `iss`, `aud`, `exp`, `iat`,
  and `nonce`. Cache JWKS with rotation handling; refetch on unknown `kid`.
- Use exact, allowlisted `redirect_uri` values registered in Casdoor — no wildcards, no
  open-redirect via the return URL.

### Sessions After Login

- After verifying the token, mint an **opaque server-side session** in Redis (see Redis §)
  rather than storing the JWT in the browser. This gives instant revocation and no stale
  claims. Tokens are never placed in `localStorage`.
- Local agents authenticate with a **scoped device PAT** (user + workspace, 90-day, revocable)
  issued via `POST /devices/authorize`; the `workspace_id` derives from the PAT, never the
  request body.

### Operations

- Casdoor's own admin credentials, DB, and signing keys are secrets managed outside the repo;
  rotate signing keys periodically and handle `kid` rollover gracefully on the verifier side.
- Run Casdoor behind TLS; restrict its admin console to internal networks / SSO.

---

## Caddy (Edge / Reverse Proxy)

Edge reverse proxy (in front of the BFF, ahead of CloudFront). Handles TLS termination,
routing, and security headers.

### TLS & HTTP

- Rely on **automatic HTTPS** (ACME) with a resolvable domain; for internal/dev use Caddy's
  internal CA rather than disabling TLS. Never run public traffic over plaintext.
- Redirect HTTP→HTTPS and enable **HTTP/2** (and HTTP/3 where supported).
- Keep the `Caddyfile` in version control; pin the Caddy version and any external modules built
  into the binary (`xcaddy`).

### Security Headers & Hardening

- Set security headers at the edge (defense in depth with app-level headers):
  `Strict-Transport-Security` (with `includeSubDomains; preload`),
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` /
  `Content-Security-Policy: frame-ancestors 'none'`, and
  `Referrer-Policy: strict-origin-when-cross-origin`. Remove the `Server` header.
- Terminate TLS at Caddy and forward the real client IP and scheme
  (`X-Forwarded-For`, `X-Forwarded-Proto`) so the app can enforce `Secure` cookies and log
  correctly. Trust proxy headers only from the known upstream, not arbitrary clients.

### Proxying & Reliability

- Configure explicit **timeouts** (`dial_timeout`, read/write, `flush_interval: -1` for SSE) so
  the proxy doesn't hold sockets forever.
- **SSE/long-lived streams:** disable response buffering (`flush_interval -1`) and set generous
  read/idle timeouts on those routes so query/ingest/notification streams aren't cut off.
- Add health-check upstreams (`health_uri`, `health_interval`) so Caddy stops routing to an
  unhealthy BFF replica.
- Apply **rate limiting** at the edge for auth and public endpoints (defense in depth with the
  app's own limiter).
- Reload configuration gracefully (`caddy reload`) — zero-downtime; validate with
  `caddy validate` in CI before deploy.

---

## Backing-Service Checklist

Before merging changes that touch a backing service, verify:

### All Services
- [ ] No credentials in code/config; all via env/secret manager
- [ ] TLS enabled for the connection in staging/prod
- [ ] Dedicated, least-privilege account (not root/admin)
- [ ] Image/version pinned; explicit resource limits set
- [ ] Health/readiness probe reflects this dependency
- [ ] Graceful shutdown drains/closes cleanly

### PostgreSQL
- [ ] Connections fronted by PgBouncer; app pool bounded with lifetimes + statement timeout
- [ ] RLS `FORCE`d on tenant tables; app role has no `BYPASSRLS`; context via `SET LOCAL`
- [ ] Migration is expand/contract, uses `CONCURRENTLY` off-transaction, has `lock_timeout`
- [ ] Idempotency keys have UNIQUE constraints; RLS/FK columns indexed
- [ ] PITR/backups tested; replication lag monitored

### Redis
- [ ] Durable data uses AOF; HA via Sentinel/Cluster
- [ ] Counter mutations atomic (INCR/DECR/Lua); locks use `SET NX EX` + owner-checked delete
- [ ] Every key has a TTL or is intentionally durable; `maxmemory` + correct eviction policy
- [ ] `noeviction` on instances holding billing/session state

### NATS JetStream
- [ ] Stream retention/`max_age`/`max_bytes` set; `storage=file`
- [ ] Durable pull consumer with queue group; `max_ack_pending`, `ack_wait`, `max_deliver` set
- [ ] Consumers idempotent (msg-id / data-layer key); ack after success; DLQ on `max_deliver`
- [ ] Long runs heartbeat `InProgress`; ticks are single-owner via data-layer guard

### Qdrant
- [ ] Mandatory `workspace_id` payload filter injected by the repository layer
- [ ] Payload index on filtered fields; metric/dims pinned to the embedding model
- [ ] Idempotent upsert by deterministic point ID; replication + snapshots for prod

### MinIO / S3
- [ ] Scoped access key; public access blocked; SSE + TLS on
- [ ] Presigned URLs short-lived, content-type/size constrained, tenant-prefixed; verified server-side
- [ ] Versioning + lifecycle rules (expire incomplete multipart / old versions)

### Casdoor
- [ ] Auth Code + PKCE (S256); `state` + `nonce` validated; exact redirect_uri allowlist
- [ ] `id_token` verified via JWKS (alg/iss/aud/exp); opaque Redis session, no JWT in browser
- [ ] Device PATs scoped + revocable; workspace_id from PAT, not body

### Caddy
- [ ] Automatic HTTPS; HTTP→HTTPS redirect; Caddyfile validated in CI
- [ ] Security headers set at edge; `Server` header stripped; forwarded IP/proto trusted only from upstream
- [ ] SSE routes unbuffered with long timeouts; upstream health checks; graceful reload
