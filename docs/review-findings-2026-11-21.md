# Review findings and implementation guide (2026-11-21)

**Reviewed at**: `9dd0edd` (main). **Scope**: the whole Go codebase, read for correctness, concurrency, multi-replica safety, security, scalability and Go idiom. It deliberately skips what the review of 2026-11-13 ([review-findings-2026-11.md](review-findings-2026-11.md), R1 to R18) and `docs/architecture.md` "Where the code differs from the docs" already cover. The table [Already tracked elsewhere](#already-tracked-elsewhere-do-not-duplicate) lists those items.

**Method**: the code was read. `go build ./...`, `go vet ./...` and `go test ./...` pass. Q1 was **reproduced**: a test that runs the real `subscribeRunEvents`, the forwarding loop in `kernelRunStarter.StartRun` and `runsrest.Handler.PublishEvent` over an in-memory Redis saw one published event 10,605 times in 500 ms. Every other item is marked *read*: it was not reproduced. Integration tests, lint and the eval gate were not run for this review.

This document is written for whoever implements the fixes, including a model with no memory of the review. Each work item says what is wrong, where, what to change, which test to write first and when it is done. The open tasks are registered in `specs/001-agent-platform/tasks.md` (T285 to T305); this file holds the detail.

## How to use this document

1. Pick **one** work item. One item is one pull request. Do them in the order of the table unless an item says it depends on another.
2. Read the files named under **Where** before changing anything. Items name functions and types, because line numbers drift. If a function no longer exists or no longer does what the item says, stop and re-read the code: the item may already be fixed, or the design may have moved. Do not force the change.
3. Write the failing test under **Tests** first and watch it fail. That proves the finding still holds. If it passes before your change, say so in the pull request and close the task as not reproducible.
4. Make the change, then run the checks under **Ground rules**.
5. In the same pull request, tick the item's task in `specs/001-agent-platform/tasks.md` (`- [ ]` to `- [X]`) and make its line name every file you touched. A ticked task that names a missing file fails `tests/contract/specs_test.go`.
6. An item marked **decision** has a question for the owner. Implement only the part the item says is safe without one, and ask about the rest in the pull request.

## Ground rules

The ground rules of [review-findings-2026-11.md](review-findings-2026-11.md#ground-rules-they-are-enforced-by-tests-and-ci) apply unchanged: the pre-done check (`go build ./... && go vet ./... && golangci-lint run --build-tags=integration ./... && go test ./...`), the 1,000-line PR limit, the spec gate, the 500-line file cap, the import boundaries, the lint bans, the `goleak` rule, nil-valid hooks, `.env.example` for every new `NEXUS_*` variable, `make eval` for anything the model sees, and `make generate` plus the `bruno/` file for any REST change. Three more apply here:

- A Redis test uses `github.com/testcontainers/testcontainers-go/modules/redis` (already in `go.mod`) behind the `integration` build tag, like `internal/queue/redis/*_integration_test.go`. Do not add a new Redis fake to `go.mod` for one test.
- A change to a migration follows `.claude/rules/` for `migrations/`: a new numbered file, never an edit to an applied one, and a new table goes into `tableOwners` in `tests/contract/ownership_test.go` in the same change.
- A multi-replica fix needs a test with **two** instances of the component (two `Scheduler`s, two `Gate`s, two anchor passes) over one store. A single-instance test does not prove it.

## Work items

| Id | Task | Title | Severity | Depends on |
|---|---|---|---|---|
| Q1 | T285 | REST runs re-publish their own bus events forever | **blocker** (reproduced) | none |
| Q2 | T286 | Per-run event subscriptions never close for a paused run | **blocker** (leak per message) | Q1 |
| Q3 | T287 | The Telegram bot token leaks into `delivery_failed` events | **blocker** (security) | none |
| Q4 | T288 | Chat messages are dropped while a turn runs, or after a failed dispatch | high (data loss); **decision** for part (a) | Q2 |
| Q5 | T289 | The outbox never retries, so chat replies are at-most-once | high (data loss) | Q2 |
| Q6 | T290 | Cron schedules fire once per replica | high (multi-replica) | none |
| Q7 | T291 | Audit anchoring reports false breaks and forks across replicas | high (false tamper alerts); **decision** on design | none |
| Q8 | T292 | A session's cost ceiling is enforced against a stale per-process total | high (cost) | none |
| Q9 | T293 | Tool calls after a suspended call in the same turn are never executed | high (correctness) | none |
| Q10 | T294 | MCP client: unbounded responses and no egress-safe dialer | high (security) | none |
| Q11 | T295 | MCP client does not follow the Streamable HTTP transport | high (interoperability) | Q10 |
| Q12 | T296 | SSE stream: leaked errors, no keepalive, undetected gaps, slow shutdown | medium | Q1 |
| Q13 | T297 | Queue transitions are not atomic, and retries are unbounded | medium (reliability) | none |
| Q14 | T298 | HTTP server: no panic recovery, access log, request id or per-route deadline | medium (operability) | none |
| Q15 | T299 | Webhooks: unauthenticated traffic costs a database transaction; three copies of the rate limiter | medium (DoS) | none |
| Q16 | T300 | Extract the triplicated chat-webhook dispatch | medium (refactor) | Q2, Q4, Q5 |
| Q17 | T301 | Envelope AAD does not bind the event; no KEK id | medium (security); **decision** | none |
| Q18 | T302 | Redis scripts: no EVALSHA, no hash tags, keys that never expire | low | none |
| Q19 | T303 | Background passes: one admin pool per tick, every replica runs every pass | low | Q6, Q7 |
| Q20 | T304 | Missing indexes for the session list and the orphan scan | low | none |
| Q21 | T305 | Small idiom and robustness fixes | nit | none |

---

### Q1 — REST runs re-publish their own bus events forever (T285)

**Where**: `cmd/nexusd/runner.go` (`kernelRunStarter.StartRun`, the forwarding goroutine after `Enqueue`), `cmd/nexusd/eventbus.go` (`subscribeRunEvents`), `internal/runs/rest/run_create.go` (`CreateRun`, `publishUntilDone`), `internal/runs/rest/broker.go` (`PublishEvent`).

**What is wrong**: `StartRun` subscribes to the session's Redis channel and forwards every event published there into the channel it returns (`launched.Events`). REST's `CreateRun` drains that channel with `publishUntilDone`, which calls `PublishEvent`, and `PublishEvent` publishes to the same Redis channel (`h.Bus` is always set; see `cmd/nexusd/compose.go`). The subscription receives the re-publish and forwards it again, and so on. Every event of a REST run circulates until the run's terminal event closes the subscription. A run that suspends (approval, delegation) or pauses (`conversational: true`) never closes it, so the loop runs forever. Each lap also:

- reaches every process through the `PSUBSCRIBE` in `startEventBus` (the SSE broker drops the duplicates by `seq`, so the UI looks normal);
- re-runs `deliverApprovalNotification` for each lap of an `approval_requested` event: a `Ledger.Open` transaction each time;
- runs `emitTerminalSpan` for each lap of a terminal event that is still circulating.

The comment on `publishUntilDone` says it handles "a synchronous Kernel.Seed's handful of events". That was true before the queue: the forwarding loop added later made it carry bus events too.

**Fix**: REST must publish the seed events (no worker publishes those) and must not re-publish events that came off the bus. Pick one:

1. *Preferred*: the seed events and the bus events reach the caller through different paths. `StartRun` returns a channel that holds only the seed events and closes after them. The per-run subscription moves to the surfaces that need it (Telegram, Zalo, email; see Q2) as a separate port, opened before `Launch` is called. That needs a session id before `Launch`, so let `runs.LaunchRequest` carry an optional pre-minted `SessionID`.
2. *Minimal*: add `Relayed bool` to `runs.Event`, set it in the forwarding goroutine, and have `publishUntilDone` skip relayed events. This leaves Q2's leak in place.

Whichever you choose, rewrite the doc comments on `publishUntilDone` and `StartRun` to say exactly which events each one carries.

**Tests**: `cmd/nexusd/eventbus_integration_test.go` (new, `integration` tag, testcontainers Redis). Build a `runsrest.Handler` with `Bus = redisEventBus{client}`, call `subscribeRunEvents`, start the forwarding loop as `StartRun` does, drain it with `PublishEvent` as `publishUntilDone` does, publish one non-terminal event, and count messages on a separate subscription to `runsrest.EventsChannel(session)` for 500 ms. Before the fix the count is in the thousands; after it, exactly 1. Add a second case: the seed events are still published exactly once.

**Done when**: one published event is seen once on the bus, a REST run's seed events still reach an SSE client, and `go test -race -tags=integration ./cmd/nexusd -run EventBus` passes.

### Q2 — Per-run event subscriptions never close for a paused run (T286)

**Where**: `cmd/nexusd/eventbus.go` (`subscribeRunEvents`, `resumeConversationEvents`), `cmd/nexusd/runner.go` (the forwarding goroutine), `internal/surfaces/{telegram,zalo,email}/webhook.go` (`drainAndNotify`).

**What is wrong**: each of these goroutines stops only on `EventTerminal` or an error. Several events end a turn without either:

- `awaiting_input`, which every Telegram, Zalo and email session reaches after each reply, because they launch with `Conversational: true`;
- `approval_requested` with the session suspended;
- the delegation suspension written by `suspendForDelegation` in `kernel/terminal.go`.

go-redis gives each subscription its own connection. So every chat message leaves behind:

- a Redis connection;
- the `subscribeRunEvents` goroutine;
- the forwarding goroutine (or the `resumeConversationEvents` one);
- a `drainAndNotify` goroutine.

These stay until the idle sweep ends the session (30 minutes) or, for a pending approval, possibly forever. Each new message in the same conversation opens another subscription while the older ones are still live, so the later turns' events are delivered N times (the outbox's unique key stops the duplicate sends, but each costs a transaction). There are two more problems:

- Every `out <- ...` send in these goroutines is unguarded. A reader that stops reading blocks the goroutine for good, and calling `cancel` (which closes the subscription) does not free a goroutine already blocked on a send.
- `subscribeRunEvents` logs a failed `Receive` and carries on, so a caller waits on a subscription that never existed.

**Fix**:

- Close these channels at the end of a *turn*, not of the run: on `EventTerminal`, `EventAwaitingInput`, and the suspension events. Put the predicate in one place, for example an `EndsTurn(eventlog.EventType, ...)` helper in `internal/runs`, used by all three loops. The status projection is not on the wire, so for `approval_requested` decide on the event type alone.
- Every send selects on `ctx.Done()`.
- `subscribeRunEvents` returns an error when `Receive` fails, and `StartRun` / `resumeConversationEvents` return it.
- Bound a subscription's lifetime: a context with a deadline, set from a new `NEXUS_*` setting with a default of the idle-conversation window. When it fires, the goroutine logs and stops.

**Tests**: the integration test file from Q1. Start a conversational run whose events end in `awaiting_input` and assert that the returned channel closes and the subscription is closed. You can check `PUBSUB NUMSUB` on the channel, or `client.PoolStats()`. Add a unit test of the predicate covering every event type, so a new suspension event cannot be forgotten. `goleak` already fails a test that leaves a goroutine.

**Done when**: after a conversational turn pauses, the process holds no subscription and no goroutine for that session.

### Q3 — The Telegram bot token leaks into `delivery_failed` events (T287)

**Where**: `internal/surfaces/telegram/sender.go` (`Sender.Send`), `internal/surfaces/outbox.go` (`Outbox.Deliver`, `reason = sendErr.Error()`), `internal/surfaces/postgres/ledger.go` (`Record`).

**What is wrong**: the request URL is `https://api.telegram.org/bot<token>/sendMessage`. When `client.Do` fails (a timeout, DNS, a reset), the error is a `*url.Error`, and its message includes the full URL, token included. `Send` wraps it with `%w`. `Deliver` stores `sendErr.Error()` as the reason of a `delivery_failed` event, and that event's payload is decrypted and streamed to the session's audience by `GET /v1/runs/{id}/events`. The same string reaches the logs. Zalo sends its token in a header and is not affected; check the email sender the same way.

**Fix**:

- In `Send`, when `client.Do` fails, return an error that does not carry the URL. Use `errors.AsType[*url.Error](err)` and wrap only its `Err`, with the operation named and the URL written as `https://api.telegram.org/bot<redacted>/sendMessage`.
- Read and discard the body with `io.LimitReader` on non-2xx responses, and include Telegram's `description` field (bounded) in the error: it says why (`message is too long`, `chat not found`).
- In `Outbox.Deliver`, store a classified reason (`timeout`, `status 400`, ...) rather than an arbitrary error string, so no other sender can leak this way.

**Tests**: `internal/surfaces/telegram/sender_test.go`. Use an `http.Client` whose `Transport` returns an error, and assert that the error from `Send` does not contain the token. Add an outbox test asserting the reason recorded for a failing sender is the classified one.

**Done when**: no code path puts a bot token into an error string. `grep -rn 'bot%s' internal/` shows the one URL constructor and it is never formatted into an error.

### Q4 — Chat messages are dropped while a turn runs, or after a failed dispatch (T288)

**Where**: `internal/surfaces/{telegram,zalo,email}/webhook.go` (`dispatch`), `internal/surfaces/lock.go`, `internal/surfaces/postgres/inbound_sql.go` (`claimInboundDelivery`).

**What is wrong**:

- **(a)** `dispatch` holds the conversation's `session_key` lock until `drainAndNotify` returns, which is the end of the turn. A second message from the same chat while the agent is still answering waits about 1.2 s (`AcquireSessionLock`), then gets `200 OK` and is dropped with a warning. That is the ordinary case of a user sending two messages in a row. Separately, the lock is the queue's `SessionLock`, whose 30-second TTL nothing renews here. So "held for the turn" is really "held for at most 30 seconds", and a long turn lets the next message through. The behaviour therefore depends on how long the model takes.
- **(b)** `dispatch` claims the delivery id before anything else. If a later step fails (`FindByKey`, `Launch`, `ResumeConversation`), the handler answers `500`, the provider retries with the same id, and the retry is acknowledged as a duplicate. The message is lost. `claimInboundDelivery`'s comment accepts this for a *crash*, but here it happens on any error, with the process up.

**Fix**:

- *(b), safe now*: on any error after a successful claim, release it: delete the `inbound_deliveries` row in its own transaction (a new `Unclaim` on `InboundDedup`), then return the error so the provider retries. Better still: when the session work is a single transaction (`ClaimAwaitingInput`, session creation), take the claim inside that transaction.
- *(a)*, **decision**: what should happen to a message that arrives while a turn runs? The recommendation is to never drop it. Two options:
  1. Append it as a `user_message` the running turn picks up (the REST steer path already does this for `awaiting_input`).
  2. Store it in a per-session inbound buffer that is drained when the session reaches `awaiting_input`.

  Either way, release the lock as soon as the decide-then-act step has committed, not at the end of the turn, and do not use a TTL lock as a turn guard.

**Tests**: `internal/surfaces/telegram/webhook_test.go`. A `Launcher` that fails once: assert the retry of the same `update_id` dispatches. For (a), once decided: two distinct `update_id`s for one chat while the first turn is still running, and assert both messages reach the session.

**Done when**: no delivery the provider sent is acknowledged without its text reaching a session, or a log at `error` level saying why.

### Q5 — The outbox never retries, so chat replies are at-most-once (T289)

**Where**: `internal/surfaces/outbox.go`, `internal/surfaces/postgres/ledger.go`, `internal/surfaces/postgres/delivery_sql.go`, the `drainAndNotify` functions, `cmd/nexusd/background.go`.

**What is wrong**: the comments call the outbox "durable, at-least-once", but:

- `Deliver` makes one attempt, and a `failed` (retryable) row is never picked up again: nothing lists `pending` or `failed` deliveries.
- The attempt is triggered by a Redis Pub/Sub message (at-most-once) in an untracked goroutine of the process that took the webhook. A missed message, a Redis reconnect or a deploy loses the reply with no record that it was ever owed.
- A reply over Telegram's 4,096-character limit fails with `400` every time.

**Fix**:

- Make delivery driven by durable state. When the worker appends an event worth delivering, open the delivery row in the same transaction or right after. Then add a periodic pass in `cmd/nexusd/background.go` (per tenant, through `tenancy.ForEach`) that lists `pending` and `failed` deliveries whose next attempt is due and retries them with backoff, up to `maxDeliveryAttempts`. The schema needs `next_attempt_at`; add it in a migration.
- The webhook process then needs no subscription to send replies, which also removes most of Q2's surface.
- Split Telegram text into chunks of 4,096 characters or fewer.

**Tests**: an integration test in `tests/integration/surfaces_dedup_test.go` or a new file: a sender that fails twice and then succeeds; run the retry pass three times; assert one `delivered` row and exactly one successful send. Add a unit test of the chunking.

**Done when**: killing the process between a reply's event and its send still delivers the reply after restart.

### Q6 — Cron schedules fire once per replica (T290)

**Where**: `internal/surfaces/cron/scheduler.go` (`runTenant`, `fire`), `internal/surfaces/cron/postgres/` (`Due`, `Fire`).

**What is wrong**: every `nexusd` process runs the scheduler. `Due` reads the due rows with no lock, and `Fire` advances the schedule with `UPDATE cron_schedules SET last_run_at = $1, next_run_at = $2 WHERE schedule_id = $3`, which succeeds for every caller. Two replicas polling within the same window both create a session and both start a run. `Fire`'s comment ("the tick is exactly-once") holds for one process only.

**Fix**: make advancing the schedule a compare-and-set, done first in `Fire`'s transaction:

```sql
UPDATE cron_schedules SET last_run_at = $1, next_run_at = $2
WHERE schedule_id = $3 AND enabled AND (next_run_at IS NULL OR next_run_at <= now())
```

If no row was updated, return a sentinel (`cron.ErrNotDue`) without creating the session, and have `fire` treat it as "another replica took it" (debug log, no error). Alternatively, `SELECT ... FOR UPDATE SKIP LOCKED` in `Due` inside the same transaction as `Fire`.

**Tests**: an integration test with two `Scheduler`s over one store and one due schedule, calling `RunOnce` concurrently: exactly one session is created.

**Done when**: N replicas fire a due schedule exactly once.

### Q7 — Audit anchoring reports false breaks and forks across replicas (T291)

**Where**: `internal/audit/postgres/anchor.go` (`Chain.Anchor`), `internal/audit/postgres/verify.go`, `internal/audit/postgres/scoped.go` (`AnchorAndVerify`), `cmd/nexusd/background.go` (`startAnchorLoop`), `migrations/0006_audit.sql`.

**What is wrong**:

1. **Out-of-order commits.** `receipt_seq` is a `bigserial`. Sequence values are handed out at insert time and committed in any order. If receipt 100 is still uncommitted when the anchor pass reads `MAX(receipt_seq) = 101`, the anchor covers `(prev, 101]` without receipt 100. When 100 commits, every later `Verify` recomputes the range with it and reports `hash_mismatch` for an anchor nobody tampered with. Concurrent appends in one tenant (two sessions running at once) are enough to cause it.
2. **Concurrent anchoring.** Every replica runs `startAnchorLoop` for every tenant every 5 minutes with no lock. Two passes read the same last anchor and both insert one from the same `from_receipt_seq`. The anchor chain then forks, and `verifyAnchors` (which chains by `to_receipt_seq` order) reports a break.
3. **Full replay.** `Verify` replays every receipt and anchor the tenant has ever written, in one transaction, every 5 minutes, on every replica. The cost grows without bound with history.
4. **Key rotation.** The verifier trusts only the signer's *current* key, so rotating the signer key reports every older receipt as `unverifiable_signer_key`.

**Fix**:

- *(2), safe now*: take `pg_advisory_xact_lock(hashtext('audit_anchor:' || $tenant))` at the start of `Anchor`, and add `UNIQUE (tenant_id, from_receipt_seq)` on `audit_anchors` in a new migration.
- *(1)*, **decision**:
  - *Recommended design*: anchor the per-session heads instead of a global range. Per-session order is commit order, because `store.Append` holds the session's advisory lock. An anchor records, for each session with new receipts since the last anchor, `(session_id, seq, hash)` of its head, in a child table owned by `internal/audit`. Its hash covers those rows sorted by session id, plus the previous anchor's hash. A late commit only extends a chain past its recorded head and never changes an anchored prefix.
  - *Interim mitigation*: anchor only up to a horizon past which no transaction can still be in flight. This is only sound if the database bounds transaction duration (`idle_in_transaction_session_timeout` and `transaction_timeout`). Do not use `created_at` as the horizon on its own: it is the transaction's *start* time.
- *(3)*: make the scheduled pass incremental. Verify receipts and anchors after the last anchor this pass verified, and leave the full replay to `nexusd verify-chain`.
- *(4)*: verify against a keyring (`key_id` to public key) loaded from a file of retired public keys plus signerd's current one.

**Tests**: an integration test with two goroutines appending to different sessions of one tenant, the second committing first, and an anchor pass between the commits; then `Verify` reports no break. A second test with two concurrent `Anchor` calls for one tenant: exactly one anchor row, and `Verify` is OK.

**Done when**: concurrent sessions and multiple replicas produce no false `hash_mismatch`, and a scheduled pass costs time proportional to new receipts only.

### Q8 — A session's cost ceiling is enforced against a stale per-process total (T292)

**Where**: `internal/cost/gate_helpers.go` (`sessionBudgetFor`), `internal/cost/reserve.go`, `internal/cost/reconcile.go`, `cmd/nexusd/background.go` (`queueRunner.Run`).

**What is wrong**: the gate sums a session's spend from the ledger the first time *this process* sees the session, then keeps a running total in `Gate.sessionState` for up to an hour. Any worker in any process can lease a session's next job. If turn 1 runs on A, turn 2 on B and turn 3 on A, then A's total is missing B's spend at turn 3 and the session can pass its ceiling by that amount. T258 fixed the cold start, not the hand-off.

**Fix**: a session's in-memory state is valid only while this process holds the session's lock. Add `Gate.ForgetSession(sessionID)` and call it when a job ends (in `queueRunner.Run`, or through a `runctl.SettledObserver`), so the next reservation on any process sums from the ledger again. Summing once per job is one indexed query. Keeping per-session spend in Redis like the tenant counter also works, but costs a round trip per reservation.

**Tests**: `internal/cost/gate_durable_test.go`. Two `Gate`s over one `inmem` ledger. Reserve and reconcile on A, then on B, then on A, with a ceiling the three together exceed: the third reservation is refused.

**Done when**: a session's ceiling holds however its turns are spread across processes.

### Q9 — Tool calls after a suspended call in the same turn are never executed (T293)

**Where**: `kernel/turns.go` (the `ClassificationToolCalls` loop), `kernel/loop.go` (`Resume`, `ResumeDelegation`), `kernel/hygiene.go`.

**What is wrong**: a turn whose assistant message holds several `tool_use` blocks runs them in order. When call *i* returns `AwaitingApproval` or `AwaitingDelegation`, the run suspends. Calls *i+1..n* already have durable `tool_use` events, but they have not run. `Resume` executes only the approved call and then enters `runTurns`, whose `Hygiene` pass backfills each remaining call with a synthetic `interrupted_before_execution` error. The model asked for calls that were never attempted and is told they failed. Claude makes parallel tool calls routinely, and under `supervised` autonomy any call that needs approval triggers this. `Hygiene` also sorts the synthetic results by UUID string, not by the calls' `seq`.

**Fix**:

- After `Resume` (and `ResumeDelegation`) settles the resolved call, find the unanswered `tool_use` events of the same assistant turn (those after it in `st.History`, in `seq` order). Run them through the normal `Tools.Execute` path, with the same handling the loop in `runTurns` applies (taint transitions, permission denial, a further suspension, stuck detection). Only then enter `runTurns`.
- Factor that per-call handling out of `runTurns` into one function both paths call, so they cannot drift.
- Sort `Hygiene`'s synthetic results by the `tool_use` event's `Seq`.
- This changes what the model sees: run `make eval`.

**Tests**: `kernel/resume_test.go`. A scripted provider whose first turn requests two tools, the first needing approval. After `Resume` with a grant, assert that the executor ran the second tool, that its result is a real one, and that the next prompt pairs both (`provider.ValidateTranscript`). Add the same case for a second call that itself needs approval: the run suspends again on it.

**Done when**: no tool call the model requested gets a synthetic result while the run is healthy.

### Q10 — MCP client: unbounded responses and no egress-safe dialer (T294)

**Where**: `internal/surfaces/mcp/client.go` (`Client.do`), `internal/surfaces/mcp/resolver.go`, `internal/surfaces/mcp/port.go`, `cmd/nexusd/pipeline.go` (where `mcp.Resolver` is built with no `Client`).

**What is wrong**:

- `do` decodes the response with `json.NewDecoder(resp.Body)` and no `io.LimitReader`. A misbehaving MCP server can make the process allocate until it is killed, against the rule in `CLAUDE.md`. It also ignores the HTTP status.
- `Resolver.Client` is nil in `cmd/nexusd`, so every call uses `&http.Client{Timeout: 15s}`. That is not the egress client T275 introduced for `web_fetch`: redirects are not re-checked against the allowlist, and an allowlisted hostname that resolves to a private or link-local address (cloud metadata) is dialled. The allowlist is checked only on `base_url`'s hostname, in `listTools`.

**Fix**:

- Bound the body (for example 4 MiB, through a named constant) and refuse a non-2xx status.
- Build the MCP `http.Client` with the egress-safe dialer and redirect policy from `internal/tools/builtin/egress.go`. If sharing would cross a feature boundary, put the dialer in a package both may import; check `allowedFeatureImports` and `boundaries_test.go` first. Wire it in `cmd/nexusd/pipeline.go`.
- `hostAllowed` exists twice (`internal/tools/builtin/web_fetch.go`, `internal/surfaces/mcp/resolver.go`); keep one.

**Tests**: `internal/surfaces/mcp/resolver_test.go`. An `httptest.Server` that streams more than the bound: the call fails. A server that redirects to `127.0.0.1`: refused. A base URL on the allowlist that resolves to a private address: refused unless private networks are allowed.

**Done when**: MCP egress follows the same rules as `web_fetch`.

### Q11 — MCP client does not follow the Streamable HTTP transport (T295)

**Where**: `internal/surfaces/mcp/client.go`, `internal/surfaces/mcp/resolver.go`, `internal/surfaces/mcp/port.go`.

**What is wrong**: the client says it speaks protocol version `2025-06-18` over Streamable HTTP. Check each point below against the specification for that version before changing code. Read, the client:

- sends `Accept: application/json` only, where the transport requires the client to list both `application/json` and `text/event-stream`;
- cannot read a `text/event-stream` response, which a server may return for any request;
- ignores the `Mcp-Session-Id` a server may assign on `initialize`, and must then receive on every later request;
- never sends the `notifications/initialized` notification after `initialize`;
- never sends the `MCP-Protocol-Version` header on requests after initialization;
- re-runs `initialize` for every listing (`port.go` and `resolver.go` build a fresh `Client` each time).

A server that enforces any of these rejects every call.

**Fix**: implement the handshake once per (server, credential): `initialize`, keep the session id, send `notifications/initialized`, and send both headers on every later request. Parse SSE responses by reading `data:` lines until the JSON-RPC response with the matching id arrives. Re-initialize on `404` with a session id, as the transport specifies. Keep request ids unique per client instead of the constant `1`.

**Tests**: an `httptest.Server` that behaves as a strict server: it assigns a session id, rejects requests without it or without the protocol header, and answers `tools/call` as an SSE stream. Listing and calling a tool both succeed.

**Done when**: the client works against a strict Streamable HTTP server.

### Q12 — SSE stream: leaked errors, no keepalive, undetected gaps, slow shutdown (T296)

**Where**: `internal/runs/rest/run_events.go` (`StreamRunEvents`), `internal/runs/rest/broker.go`, `cmd/nexusd/http.go` (`runHTTPServer`), `cmd/nexusd/eventbus.go` (`startEventBus`).

**What is wrong**:

- Error frames carry `err.Error()` from `Reader.Events`, from decryption and from the bus. That breaks the rule that a response never carries an infrastructure error.
- There is no keepalive. A stream that waits through a long tool call or an approval is cut by any proxy's idle timeout, which is typically 60 s.
- Frames have no `id:`, and `Last-Event-ID` is ignored, so every reconnect replays and decrypts the whole history.
- The broker drops events for a slow subscriber, and Redis Pub/Sub drops messages across a reconnect. The live loop never checks for a `seq` gap, so a dropped terminal event leaves the stream open forever.
- `http.Server.Shutdown` does not cancel `r.Context()`, so every open stream holds a deploy for the full 30 s and the drain logs "graceful shutdown failed".

**Fix**:

- Send a fixed error message and log the cause, as `restkit.ServerError` does.
- Write a `: keepalive` comment every 15 to 25 seconds.
- Write `id: <seq>` on each durable frame. Honour `Last-Event-ID` by replaying only later events; add `Reader.EventsAfter(seq)` and its SQL.
- Track `lastSeq` in the live loop too. On a gap, re-read from the store after `lastSeq` before continuing.
- Give `runHTTPServer` a context that `RegisterOnShutdown` cancels, pass it to the handler through the kit, and have `StreamRunEvents` return when it is done.
- Update `api/openapi.yaml` and `bruno/` if the stream's documented frames change.

**Tests**: `internal/runs/rest/stream_test.go`. A gap in live events triggers a re-read. `Last-Event-ID: 5` replays only events after 5. An error frame does not contain the underlying error text. The keepalive interval is a field, so a test can set it short.

**Done when**: a stream survives a proxy idle timeout, recovers from a dropped event, and ends promptly on shutdown.

### Q13 — Queue transitions are not atomic, and retries are unbounded (T297)

**Where**: `internal/queue/redis/streams.go` (`Fail`, `promoteDelayed`, `claim`), `internal/queue/worker.go` (`pollOnce`).

**What is wrong**:

- `Fail` retires the entry (ack, delete, delete the claim hash) and *then* re-adds it or dead-letters it, in separate round trips. `promoteDelayed` removes from the delay set and *then* adds to the stream. A crash or a Redis error between the two loses the job. The orphan sweep only recovers sessions marked `running`, and only every 5 minutes.
- No attempt limit dead-letters a job. A job that keeps failing with a different message each time (so the breaker, which counts identical failures, never trips) retries forever.
- Lock contention re-queues through `Fail`, so it counts as an attempt and inflates the backoff.
- Each worker builds its own `AdmissionController` (`withDefaults`), but a worker runs one job at a time, so the controller never limits anything.

**Fix**:

- Make each transition one Lua script (go-redis `Script`): retire-and-requeue, retire-and-delay, retire-and-dead-letter, promote.
- Add `MaxAttempts` to `WorkerConfig` (default 10) and dead-letter past it.
- Re-queue a lock-contended job without incrementing `attempts`.
- Share one admission controller per process, sized by a setting, or delete it.

**Tests**: `internal/queue/redis/streams_integration_test.go`. Inject a failure between the two halves (a client hook that fails the second command) and assert the job is still somewhere. Add a job that fails `MaxAttempts` times with distinct messages: it is dead-lettered. In `internal/queue/worker_test.go`: contention does not raise `Attempts`.

**Done when**: no single failure loses a job, and every job eventually completes or is dead-lettered.

### Q14 — HTTP server: no panic recovery, access log, request id or per-route deadline (T298)

**Where**: `internal/surfaces/rest/server.go` (`engine`), `internal/surfaces/rest/authn.go` (`authenticate`), `cmd/nexusd/http.go`.

**What is wrong**:

- `gin.New()` installs no middleware. A panicking handler is recovered only by `net/http`, which writes the stack to stderr outside zerolog and drops the connection without a response.
- There is no access log, request id or per-route latency and status metric, so a 5xx can be counted but not traced to a route.
- `WriteTimeout` is 0 for every route to suit SSE, so a slow-reading client can hold any response open indefinitely.
- `authenticate` matches the scheme `Bearer ` case-sensitively, but RFC 7235 says the scheme is case-insensitive.

**Fix**:

- Add three kit middlewares:
  - recovery: log the panic with zerolog and answer `500` through `Error`;
  - request id: take `X-Request-Id` if it is well formed, otherwise generate one; echo it back and put it on the logger in the request context;
  - access log: method, route template, status, duration, tenant id. No body, no query string: telemetry is content-free.
- Expose per-route counters and a latency histogram on `/metrics`.
- Set a server `WriteTimeout` (for example 60 s), and have `StreamRunEvents` clear its own deadline with `http.NewResponseController(w).SetWriteDeadline(time.Time{})`.
- Compare the scheme with `strings.EqualFold`.

**Tests**: `internal/surfaces/rest/server_test.go`. A route that panics answers `500` and the server keeps serving. The response carries `X-Request-Id`. `bearer <token>` authenticates.

**Done when**: every request is logged once with its route, status and id, and no panic escapes the kit.

### Q15 — Webhooks: unauthenticated traffic costs a database transaction; three copies of the rate limiter (T299)

**Where**: `internal/surfaces/{telegram,zalo,email}/webhook.go` (`handleWebhook`), `internal/surfaces/{telegram,zalo,email}/ratelimit.go`.

**What is wrong**:

- The tenant's webhook secret is loaded (a tenant-scoped transaction plus an unseal) before anything else, for any `tenant_id` in the path, authenticated or not. A flood of requests with random tenant ids is a flood of database transactions.
- The rate limiter runs only after authentication. It exists in three identical copies.
- Its map never evicts a key, so it grows without bound once it is keyed per sender (spec 003 T004 asks for that).
- It is per process, so N replicas allow N times the limit.
- `Allow` resets `lastFill` to `now` on refill, which drops the partial interval, so the effective rate is lower than configured.

**Fix**:

- Cache each tenant's secret briefly (for example 60 s, per process, with a negative-result cache) and add a per-client-IP limiter before the lookup.
- Replace the three limiters with one shared one built on `golang.org/x/time/rate`, with idle eviction.
- Leave per-sender keys to spec 003 T004, but make sure the shared limiter supports them.

**Tests**: a handler test that 1,000 requests with random tenant ids reach the channel port a bounded number of times. Limiter tests for eviction and for the refill rate.

**Done when**: unauthenticated traffic cannot drive database load, and one limiter implementation remains.

### Q16 — Extract the triplicated chat-webhook dispatch (T300)

**Where**: `internal/surfaces/{telegram,zalo,email}/webhook.go`.

**What is wrong**: `dispatch`, `startRun`, `resumeRun` and `drainAndNotify` are the same algorithm in three files (claim, lock, find the session, resume or start, drain, notify). Each of Q2, Q4 and Q5 would otherwise be fixed three times.

**Fix**: after Q2, Q4 and Q5, move the algorithm into `internal/surfaces` as one type (for example `surfaces.Conversation`). It takes the surface id, the `runs` ports, the dedup, the lock and a per-surface `Notifier`. Each surface keeps only its own parsing, authentication and `Sender`. This is a move plus a parameterisation, so the existing tests of all three surfaces must pass unchanged. If it is large but mechanical, label it `large-pr`.

**Tests**: the existing `webhook_test.go` files of the three surfaces, unchanged, plus a unit test of the shared type with fakes.

**Done when**: the decide-then-act sequence exists once.

### Q17 — Envelope AAD does not bind the event; no KEK id (T301)

**Where**: `internal/crypto/envelope.go` (`Seal`, `Open`), `internal/eventlog/sealed/`, `migrations/0004_encryption_keys.sql`, `internal/crypto/postgres/keystore.go`.

**What is wrong**:

- An event payload's AAD is `tenant_id|session_id`. Anyone who can write the `events` table can swap the ciphertext of two events in one session, and both still decrypt. Reads never compare the plaintext with `payload_digest`, so nothing notices until a full audit verify, and that verify checks digests, not ciphertexts.
- `encryption_keys` records no KEK id, so the KEK cannot be rotated: every wrapped DEK must be re-wrapped at once while every process restarts with the new KEK.
- `Unwrap`'s not-found error is a plain string, not a sentinel.

**Fix**:

- *Safe now*: on every open (`sealed` and the readers), compare `crypto.Digest(plaintext)` with the event's `payload_digest` and fail on a mismatch with a sentinel.
- **Decision**: also bind `event_id` into the AAD for new events, under a new `schema_version`. The audit chain's inputs do not change.
- Add `kek_id` to `encryption_keys` in a new migration. Load a keyring of KEKs by id: wrap with the current one, unwrap with the one recorded.
- Add `crypto.ErrKeyNotFound`.

**Tests**: `internal/crypto/envelope_test.go` and an integration test: swap two events' payloads through the owner connection, and the read fails with the digest sentinel. A DEK wrapped under an old KEK still unwraps after rotation.

**Done when**: a swapped payload is refused at read time, and the KEK can be rotated without downtime.

### Q18 — Redis scripts: no EVALSHA, no hash tags, keys that never expire (T302)

**Where**: `internal/cost/redis/counters.go`, `internal/queue/redis/lock.go`.

**What is wrong**:

- Every script call sends the full Lua source with `EVAL`.
- The cost script's two keys (`...:<budget>:epoch` and `...:<budget>:spend:<n>`) hash to different slots, so it fails with `CROSSSLOT` on Redis Cluster (for example a managed cluster-mode cache).
- Spend keys of past epochs are never expired.

**Fix**:

- Use `goredis.NewScript(...)` and `Script.Run` (EVALSHA with a fallback).
- Put the budget id in a hash tag (`nexus:cost:budget:{<id>}:epoch`). That changes the keys, so arm both forms during one release or document a re-arm.
- Expire a spend key some time after its epoch is replaced.

**Tests**: the existing cost adapter integration tests (`tests/integration/cost_adapters_test.go`) pass. Add a test that the two keys of one budget share a slot (`goredis.ClusterSlot`, or compare the hash-tag substrings).

**Done when**: the scripts run on a cluster and no key grows without bound.

### Q19 — Background passes: one admin pool per tick, every replica runs every pass (T303)

**Where**: `internal/tenancy/postgres/directory.go` (`ListTenantIDs`), `cmd/nexusd/background.go`, `cmd/nexusd/surfaces_wiring.go` (`adminTenantLister`).

**What is wrong**:

- `Directory.ListTenantIDs` opens and closes a new `pgxpool.Pool` on every call. Four loops call it, the cron scheduler every 30 s. That is connection churn against the direct Postgres port, while `startQueueWorkers` already holds an admin pool.
- Every replica runs every tenant sweep (anchor, team backstop, idle sweep). After Q6 and Q7 that is safe, but it is wasted work.
- `startQueueWorkers` logs and continues when it cannot build its admin pool. The process then serves `POST /v1/runs` but never drives a run.

**Fix**:

- Open one admin pool in `serve` and pass it to the directory and to the passes that need it. This overlaps spec 004 T033, which removes the admin pool entirely, so do whichever comes first.
- Run singleton passes under `pg_try_advisory_lock` so one replica runs each pass at a time.
- Fail startup if the worker pool cannot start.

**Tests**: a unit test that the directory reuses the pool it is given. An integration test with two concurrent pass runners: only one runs the body.

**Done when**: background passes open no pools per tick, and a misconfigured worker pool stops the process.

### Q20 — Missing indexes for the session list and the orphan scan (T304)

**Where**: `migrations/0002_sessions.sql`, `internal/store/session.go` (`ListSessionsForUser`), `internal/runctl/postgres/orphans.go` (`ListRunning`).

**What is wrong**:

- `ListSessionsForUser` filters on `user_id` and `parent_session_id IS NULL` and orders by `created_at DESC`, and no index covers it.
- `ListRunning` scans for `status = 'running'` across all tenants every 5 minutes, with no index.
- `GetSessionByKey`'s index `(tenant_id, session_key)` lacks `created_at` for its `ORDER BY ... LIMIT 1`.

**Fix**: a new migration with:

- `(tenant_id, user_id, created_at DESC) WHERE parent_session_id IS NULL`;
- a partial index on `(status) WHERE status = 'running'`, or on `updated_at` with the same predicate;
- a replacement for the session-key index that includes `created_at DESC`.

`tests/integration/session_migration_test.go` pins columns and foreign keys, not indexes. Check it still passes.

**Tests**: an integration test asserting with `EXPLAIN` that each query uses an index scan on a table seeded beyond the planner's seq-scan threshold. If that is flaky, assert that the indexes exist.

**Done when**: the three queries are index-backed.

### Q21 — Small idiom and robustness fixes (T305)

One pull request; each line is independent.

- `internal/store/status.go` `UpdateSessionStatus`: check `RowsAffected` and return `ErrSessionNotFound` when it is 0. Today a status write under the wrong tenant scope succeeds silently.
- `internal/store/append.go`: `pg_advisory_xact_lock(hashtext(session_id))` is a 32-bit hash shared by every tenant, so unrelated sessions can serialise on a collision. Use the two-argument form with two halves of the UUID, or lock the session row (`SELECT ... FOR UPDATE`).
- `internal/eventlog/postgres/log.go`: `AppendBatch` repeats `appendIn`'s body; call `appendIn`.
- `kernel/terminal.go` `terminateFromStreamError`: when `ctx.Err() != nil` the run is being stopped, not failing. Return without writing a terminal event, so recovery resumes it rather than recording `error`.
- `cmd/signerd/main.go`: the accept loop retries a failing `Accept` with no delay, which spins on `EMFILE`. Back off as `net/http` does (5 ms doubling to 1 s).
- `internal/hooks/httphook/handler.go`: bound the response decode with `io.LimitReader`.
- `internal/obs/langfuse/exporter.go`: cap `pending` (drop and count past a bound) so a Langfuse outage cannot grow memory between flushes.
- `internal/queue/worker.go`: the breaker-open reason says "until an operator intervenes", but the job retries in 5 minutes. Make the message match the behaviour.

**Tests**: one unit or integration test per line where the behaviour changes (the first, fourth, fifth, sixth and seventh).

**Done when**: each line is fixed or explicitly declined in the pull request.

---

## Already tracked elsewhere (do not duplicate)

| Finding | Tracked as |
|---|---|
| A suspension's approval row is written outside the suspension's transaction | spec 001 T261 |
| A crash between `Reserve` and `Reconcile` leaves the counter charged | spec 001 T262 |
| The pipeline's per-session state is never evicted | spec 001 T263 |
| Delegated children and team members run on detached goroutines | spec 001 T264 |
| No two-process test harness | spec 001 T265 (Q6, Q7, Q8 each add a narrow one) |
| Webhook replay window and per-sender limits | spec 003 T003, T004 (Q15 prepares the limiter) |
| Audit anchors live in the same database as the chain | spec 003 T006 (Q7 fixes correctness, not placement) |
| The tool calls of one turn run one after another | spec 004 T032 (Q9 is about calls never run at all) |
| `nexusd` holds the admin database credential | spec 004 T033 (overlaps Q19) |
| A new connection to `signerd` per receipt | spec 004 T034 |
| `nexusctl approvals grant` treats `202` as failure | spec 006 T010 |
| A permission denial ends the run; default worker count; rehydration cost; duplicate `events` index; unsalted `payload_digest`; the `nexus_app` password | [review-findings-2026-11.md](review-findings-2026-11.md#needs-an-owners-decision-before-anyone-implements-it) |

## Needs an owner's decision before anyone implements it

- **Q4 (a)**: what happens to a chat message that arrives while a turn runs. The recommendation is to deliver it to the session, never drop it.
- **Q7 (1)**: anchor per-session heads (recommended), or anchor a range under a database-enforced transaction-duration horizon.
- **Q17**: bind `event_id` into new events' AAD under a new `schema_version`.
- **Q9** changes what the model sees after an approval. It is a correctness fix, but it must clear `make eval`.

## What was checked and what was not

| Claim | Basis |
|---|---|
| Build, vet and unit tests pass at `9dd0edd` | run |
| Q1: an event of a REST run circulates on the bus | reproduced. A throwaway test (since deleted) ran `subscribeRunEvents`, `StartRun`'s forwarding loop and `PublishEvent` over miniredis and saw one event 10,605 times in 500 ms |
| Q2 to Q21 | the code was read; none was reproduced |
| Q11's transport rules | from the MCP specification for `2025-06-18` as the reviewer recalls it; verify each against the published text first |
| Integration tests, lint, the eval gate | not run for this review |
