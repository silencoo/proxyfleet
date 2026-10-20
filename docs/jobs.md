# Application pools and durable account bindings

A Job is an application: its stable `name` is the application ID, and each
account uses its own session ID within that application. Bindings are keyed by
`(job name, session ID)`, never by passwords. A Profile defines the candidate
nodes. By default, new applications share the primary Endpoint's port and use a
unique application password to authenticate and select their application. The proxy
username is the account ID. Rotating an application password does not change its
bindings, nodes, expiry or revision. Renaming a Job does not migrate its bindings.
Dedicated Endpoints remain an optional advanced configuration.
The YAML `jobs` key and `/api/jobs` routes remain backward compatible.

A Job independently configures where its nodes come from (`selection`) and how
connections use them (`mode`). Its Profile always bounds the candidate set.

| Node selection | Behavior | Target URL required? |
|---|---|---|
| `manual` (default) | Use all eligible nodes in your chosen Profile; no Job target benchmarks or speed ranking | No |
| `auto` | Benchmark the Profile against `target_url` and select up to `size` successful nodes by elapsed time adjusted by recent success rate | Yes |

Either selection can be combined with either connection mode:

- `pooled`: each new upstream connection selects among the eligible nodes.
  Selection balances active connections; ties rotate.
  Pool-wide sticky affinity and `fail_open` do not override this job's selection.
- `pinned`: the shared port accepts an account ID as the proxy username and the
  application's password. The first connection assigns a node; later connections
  keep the assignment, including after service restart. No SDK or API call is needed.
  New accounts spread across selected nodes by binding count. Bindings are permanent
  by default and reserve their node capacity even while paused or expired.
  `max_accounts_per_node: 1` gives one account per node **within this application**;
  `0` (default) allows sharing. Other applications have independent limits.
  Capacity exhaustion rejects the new binding instead of reusing a full node.
  This is a node reservation, not an exclusive-IP reservation.

Both policies work in `pool` or `hybrid` mode. All shared applications and ordinary
pool clients can use the same primary port, with different credentials. Pinned assignments select nodes,
not provider-controlled public IPs. Several nodes may share one exit IP. Existing
HTTPS CONNECT/SOCKS tunnels keep their selected node; rotation is per new upstream
connection, not necessarily per HTTP request. Plain HTTP forwarding opens a new
upstream connection per request and rechecks the same pinned assignment.

## Configure

Merge this into your own configuration (retain your existing nodes/subscriptions
and management credentials). Ports and node names are examples. This setup uses
only the nodes named `node-a`, `node-b`, and `node-c`, with no Job target probes:

```yaml
mode: pool

profiles:
  - name: scrape-candidates
    name_regex: '^(node-a|node-b|node-c)$'

endpoints:
  - name: default
    address: 127.0.0.1
    port: 2323
    username: fleet
    password: replace-ordinary-pool-secret
    # Leave profile unset on the shared primary Endpoint.

jobs:
  - name: catalog
    mode: pooled
    selection: manual       # default when omitted
    profile: scrape-candidates
    ingress: shared
    password: replace-catalog-app-secret
    concurrency: 8
    timeout: 15s

  - name: accounts
    mode: pinned
    selection: manual
    profile: scrape-candidates
    ingress: shared
    password: replace-accounts-app-secret
    concurrency: 4
    timeout: 15s
    session_ttl: "0"          # default: permanent; optional finite lifetime 1m–168h
    max_sessions: 1024
    max_accounts_per_node: 1 # per application; 0 means unlimited
```

In the WebUI, open **System Settings → Application configuration** (administrator only).
Create a Profile to define candidates, then add an application. New applications
use **Shared port · application password** and a generated masked secret; no new
listening port is needed. The editor shows the primary Endpoint and offers password
reveal/regeneration. Regeneration requires confirmation and only takes effect on
**Save configuration**. Update all clients after a rotation; existing tunnels are
not forcibly closed. Under **Access mode**, dedicated Endpoints and legacy direct
node ports remain available. Every Job field is editable: connection and selection modes, Profile/access mode,
client concurrency/timeout, pinned session lifetime/capacity, and automatic target
URL, node limit, refresh interval, batch/worker limits, response size/status/text.
The editor shows the settings relevant to the chosen modes and retains inactive
values when switching modes. **Save configuration** applies Jobs, Profiles and
Endpoints together using the configuration revision. Failed saves and conflicts
retain the draft; **Reload settings** explicitly discards it after confirmation.
Leaving settings with unsaved changes also requires confirmation.

The **Applications** page shows candidates, selected nodes and automatic
benchmark results, and lets you trigger a benchmark for auto Jobs. Its access
helper generates HTTP or SOCKS5 URLs for a Job/session; credentials start masked
and can be revealed or copied. For pinned Jobs, clicking **Get endpoint / create
session** acquires a real assignment. Ordinary scraper clients still need no
management API: they can use their account ID and application password directly.

The account binding table is read-only until you explicitly migrate/update or release a binding.
Permanent records display **Permanent binding**. **Migrate / update binding** lists
eligible nodes with their account count and limit. Choosing the same node updates
the policy/lifetime without changing nodes; choosing another node migrates the account.
Capacity is rechecked atomically on save; failed migrations preserve the old binding.
Lowering a capacity limit never evicts existing accounts: over-limit nodes refuse
new assignments until bindings are explicitly released or moved. The table supports
search, Job/state filters and pagination, including expired or removed-Job records.
Viewing or refreshing this table never creates or renews a session. Stop the scraper
and close its old connections before confirming **Release session**.

YAML and the revision-checked `PUT /api/settings` JSON API remain supported.
Shared access (`ingress: shared`) requires an enabled, **unfiltered** primary Endpoint
with a nonempty ordinary-pool username and password, in `pool` / `hybrid` mode.
The primary is the first enabled unfiltered Endpoint. Anonymous shared ingress is
not allowed: even after the last application is removed, retired app credentials
must not fall through to the ordinary pool. Existing ordinary-pool credentials,
including `base@profile`, still work. App passwords must be **16–255 bytes**, contain
no control characters, and be distinct from every other app and Endpoint password.
Use random secrets (the UI generates 192-bit keys), not application names.

For advanced dedicated access, set `ingress: endpoint` and `endpoint: <name>`.
Each Job needs its own enabled Endpoint with the same Profile; pinned Endpoints
require a username and password and retain `<base>-session-<accountID>` usernames.
Set `ingress: direct` only for legacy pinned `hybrid` / `multi-port` node-port access.
Old configurations with `endpoint` remain dedicated; without ingress/endpoint/password
they retain direct node ports. A password without ingress/endpoint opts into shared
access. Existing applications are not silently converted. Changing the Endpoint
field changes the existing binding policy: stop clients, switch access mode, then
explicitly update each binding on its current node before resuming with the new URL.
Changing only the shared application password does **not** require a binding update.

Manual selection respects existing Profile filters (including `min_quality` if
configured), TCP support, and ordinary transport health/blacklists. General
ProxyFleet health checks remain independent of Job target benchmarks. `size` does
not truncate a manual Profile; narrow the Profile itself to choose fewer nodes.
An empty/unavailable Profile fails closed. Profile membership can change as your
inventory changes; an existing pinned session retains its node or pauses if that
node is no longer eligible. It never switches to another node automatically.

## Optional automatic selection

To enable target optimization for either mode, set these fields on that Job:

```yaml
selection: auto
target_url: https://example.com/catalog
size: 5
refresh_interval: 10m
probe_batch_size: 32
probe_concurrency: 4
max_response_bytes: 2097152
expected_status: 200
# body_contains: "expected page marker"
```

Use a small, representative, authorized GET URL, not a login submission or
state-changing URL. `target_url` alone does not enable automatic selection.
Previously configured benchmark fields may remain saved in manual mode, but the
target URL, size, refresh interval and probe limits do not affect manual routing.
To preserve the earlier target-tested behavior, add `selection: auto` explicitly.
Changing selection changes the session policy; existing IDs return
`policy_changed` and require explicit recovery, never silent reassignment.

For auto Jobs, probe batches start automatically and repeat at `refresh_interval`. Candidates
with the oldest measurements are tested first, so bounded batches eventually
cover the Profile. A narrow candidate Profile gives faster initial coverage.
No measured usable nodes means unavailable; there is no broad-pool fallback.
At most `size` nodes are active, but fewer are accepted when fewer qualify.
Healthy measured spares replace failed members; routing health is rechecked
at selection and ranking is cached for at most 500 ms.

Probes require the exact `expected_status`, read the complete response up to
`max_response_bytes`, and optionally match `body_contains`. They do not follow
redirects or send account cookies. Failures affect this Job's target measurements,
not other Jobs' target scores or global node blacklists. Raw transport latency
does not count as successful application performance. Measurements are in memory
and restart cold. Stale results expire after two batch sweeps plus slack (at least
three refresh intervals, capped at 48 hours). All Jobs share a 16-probe concurrency
ceiling; each Job also respects its smaller configured worker count.

## Use ordinary proxy clients

Use the configured URL directly; management credentials are unnecessary:

```text
Catalog:   http://pool:replace-catalog-app-secret@127.0.0.1:2323
Account A: http://account-a:replace-accounts-app-secret@127.0.0.1:2323
Account B: http://account-b:replace-accounts-app-secret@127.0.0.1:2323
Ordinary:  http://fleet:replace-ordinary-pool-secret@127.0.0.1:2323
```

Shared application ports and dedicated pinned ports accept HTTP forwarding,
HTTPS CONNECT, and SOCKS5 CONNECT with username/password authentication. For SOCKS
clients use `socks5h://` (or your client's remote-DNS equivalent) with the same
credentials and port. **SOCKS4 and UDP are unsupported on these ports**, including
ordinary clients on an application-enabled primary port. Keep a separate ordinary
Endpoint if you need the normal mixed listener's other protocols.

For shared pooled apps, the username is not a binding; `pool` is a convenient value.
For pinned apps it is the stable account ID. Another application can use the same
account ID without sharing the binding. Session IDs are case-sensitive and consist
of 1–128 ASCII letters, digits, `_` or `-`; IDs are scoped to their Job. Encode
special characters in URL credentials, or use your client's separate auth fields.

For example, with an existing Python `requests` installation:

```python
import requests

account = requests.Session()  # one client/cookie jar per logical account session
account.trust_env = False
proxy = "http://account-a:replace-accounts-app-secret@127.0.0.1:2323"
account.proxies.update(http=proxy, https=proxy)
response = account.get("https://example.com/", timeout=15)
response.raise_for_status()
```

The proxy validates every new upstream connection: a missing/invalid session ID
or wrong/retired application password fails authentication without ordinary-pool fallback, and an unavailable/expired assignment
fails forwarding. Pool-wide sticky settings and retries never move that session
to a different node. Auto Jobs may reject connections after a cold start until
target probes finish; retry with backoff. Manual Jobs do not wait for these probes.
Keep concurrency and request deadlines in the scraper;
the Job's `concurrency` and `timeout` do not impose distributed traffic quotas.

## Management API

All Job routes require administrator authentication, matching `/api/access`
because acquisition returns proxy credentials. Log in with
`POST /api/auth {"password":"..."}` and send its token as
`Authorization: Bearer ...`. Do not send this token to target websites.

| Method and route | Body | Result |
|---|---|---|
| `GET /api/jobs` | — | Selection mode, candidate counts and selected nodes; target measurements for auto Jobs only |
| `GET /api/jobs/sessions` | — | Read-only persisted bindings, including removed Jobs; filter with `job`, `state`, `q`, `page` and `page_size` (1–100, default 20) |
| `POST /api/jobs/catalog/refresh` | — | Auto only: refresh one bounded batch; overlapping requests share the current status |
| `POST /api/jobs/catalog/acquire` | `{}` | Pooled proxy URL, concurrency and timeout |
| `POST /api/jobs/accounts/acquire` | `{"session":"account-a"}` | Stable node assignment and state |
| `GET /api/jobs/accounts/nodes` | — | Eligible migration targets with per-application `accounts`, `limit`, `available` |
| `POST /api/jobs/accounts/migrate` | `{"session":"account-a","node":"node-…","expected_revision":"…"}` | Atomically update an existing binding; same-node updates are allowed |
| `POST /api/jobs/accounts/release` | `{"session":"account-a"}` | Explicitly end the assignment |
| `POST /api/jobs/accounts/feedback` | See below | Auto pinned only: target-specific application observation for an assigned node |

Manual refresh has a five-second minimum interval. A refresh response may still
show `refreshing: true` when another call is already working. Acquisition returns
503 when no eligible nodes are ready for a new assignment, or 409 when eligible
nodes have no capacity. A paused or expired binding still reserves its slot. Unknown Jobs return
404; conflicting operations, including refresh/feedback on manual Jobs, return
409. Manual status returns `measured: 0`, `refreshing: false`, `size: 0` (no size
cap), and `selected: [{"node":"..."}]` without invented target measurements.

Ready access example (credentials, if configured, are URL encoded; the zero
`expires_at` timestamp is omitted below for clarity, and `permanent` is authoritative):

```json
{
  "job": "accounts", "mode": "pinned", "selection": "manual", "state": "ready",
  "session": "account-a", "node": "node-<stable-key>",
  "proxy_url": "http://account-a:replace-accounts-app-secret@127.0.0.1:2323",
  "concurrency": 4, "timeout": "15s", "timeout_ms": 15000,
  "expected_status": 200, "permanent": true, "revision": "opaque-binding-revision"
}
```

For auto pinned Jobs, feedback is for representative requests to the configured target, with elapsed
time covering the full response. Only the leased session/node pair is accepted:

```json
{
  "session": "account-a", "node": "node-<stable-key>",
  "success": true, "duration_ms": 182.5, "status_code": 200
}
```

Set `success: false` for invalid content, throttling or other unsuccessful target
responses. HTTPS response interpretation belongs to the scraper. Pooled HTTP
tunnels do not expose per-request node identity to the client, so pooled Jobs
use existing transport health and, with `selection: auto`, active target benchmarks;
application feedback is supported for auto pinned assignments only. No application requests are
automatically replayed. Respect `Retry-After` and target concurrency/rate limits.

## Session lifecycle and recovery

Keep cookies, the HTTP client/connection pool, and the session ID together. With a
pinned shared or dedicated Endpoint, the proxy acquires and validates the assignment internally.
Optionally use `/acquire` to inspect its node, expiry, and state; use returned URLs
only when `state == "ready"`. Other states intentionally omit `proxy_url`:

- `paused`: the assigned node was removed, is unhealthy, or (auto only) failed target checks.
  A later acquisition can resume the same node when it recovers.
- `expired`: an explicitly finite lease lifetime ended; acquisition does not extend it.
  New bindings default to `session_ttl: "0"` and never expire.
- `policy_changed`: Job settings or candidate Profile changed since assignment.

Stop requests and close the old HTTP client's connections before recovery. Prefer **Migrate / update binding** to keep the account ID. Select the current node
to adopt the current lifetime/policy, or a different eligible node to migrate. The
request must include the latest `revision` returned by the binding list or `/acquire`.
A stale revision returns 409; refresh before trying again. Stop clients first: existing
tunnels are not forcibly closed or moved. Failed writes, exhausted capacity and
unavailable targets preserve the original binding. A successful migration changes
its revision and persists atomically. With a fixed Endpoint the proxy URL stays the same.

Alternatively, end the old logical session with `/release`, then reconnect and restore/recreate
the application's login state explicitly. Alternatively use a new session ID with
a fresh application session, and clean up the old assignment later. Even expired
IDs never silently rebind. `/release` ends the binding, so old clients must stop
using that ID before another client reuses it.
There is no per-job global blacklist on a pinned failure and no automatic node
switch for an existing session.

Existing installations: explicit `session_ttl: 30m` or other finite values retain
their meaning. Set `session_ttl: "0"` for new permanent bindings, then use a
same-node **Migrate / update binding** operation to convert each old binding.
Reading the table, upgrading or restarting never silently renews or reassigns old records.
Changing the per-node account limit alone does not invalidate existing policies.

Assignments are atomically persisted in `job-sessions.json` next to `config.yaml`,
with restrictive permissions. Preserve it with the rest of the data directory.
Corrupt state fails startup rather than resetting identities. Finish sessions
explicitly: expired records count toward the bounded store (4096 total), as
silently forgetting them would allow accidental reassignment. `/release` also
cleans up assignments for Jobs removed from configuration.
Startup and runtime cutovers temporarily reject creation of new pinned bindings
until the runtime commits or rolls back. Existing assignments are preserved;
a rejected configuration cannot leave sessions bound to its candidate nodes.

Pinned Endpoints gate new upstream connections on every request to acquire a node.
Existing tunnels stay on their node and are not forcibly closed at expiry/release.
Legacy dedicated node ports (`ingress: direct`) gate acquisition only;
clients using those ports must revalidate through the API themselves and must not
reuse cached URLs indefinitely after node removal or port-map changes.
Remote clients need reachable listener addresses, authentication and an appropriate
`external_ip` for wildcard listeners. A loopback URL is local to the client host.

## Python integration

This optional helper is useful for API status, feedback, and explicit cleanup;
ordinary proxy clients can use the URLs above without it.
Copy [proxyfleet_jobs.py](../examples/python/proxyfleet_jobs.py) into the scraper
and install `examples/python/requirements.txt`. It keeps a separate aiohttp
session/cookie jar per logical session, revalidates pinned assignments before
each request, and shares each Job's semaphore across clients in the same Fleet.
The configured request timeout covers the complete target request. Concurrency
limits are client-side, per process, not a distributed quota or a proxy tunnel
limit. Divide the desired total across processes or add a shared job scheduler.

```python
import asyncio
import os
from proxyfleet_jobs import Fleet

async def main():
    async with Fleet("http://127.0.0.1:9091", os.environ["PROXYFLEET_PASSWORD"]) as fleet:
        async with await fleet.job("catalog") as catalog:
            responses = await asyncio.gather(*(
                catalog.request("GET", f"https://example.com/catalog?page={page}")
                for page in range(10)
            ))

        async with await fleet.job("accounts", session="account-a") as account:
            # Login and subsequent requests share this client's cookies and node.
            response = await account.request("GET", "https://example.com/")
            # report=True optionally reports target responses for auto Jobs.
            # Finish only when the logical account session is actually complete:
            await account.finish()

asyncio.run(main())
```

Closing the Python client alone preserves the server lease. It does not persist
cookies across Python process restarts; applications own cookie/login storage.
Both `close()` and `finish()` reject new or queued requests and wait for active
requests (including assignment checks and feedback) to finish. `finish()` then
releases the binding once, even if called concurrently.
`JobUnavailable` requires explicit recovery; the helper never switches nodes,
recreates a session, or retries an application request on its own.
Optional feedback failures are available as `client.last_feedback_error`; they
do not hide an already-completed response or cause an application request replay.


## Optional idle binding reclamation

Pinned applications using **shared** or **endpoint** ingress can configure
`session_idle_timeout: 24h` (1 minute–720 hours). `0` or omission disables cleanup
and preserves the existing durable-binding behavior. Direct node-port ingress
and pooled applications cannot enable it: direct traffic does not carry enough
application/session identity to track safely.

This is independent of `session_ttl`: TTL controls validity; idle retention
controls when an unused record is removed and its application/node capacity is
returned. Paused and expired bindings are also eligible when idle cleanup is
explicitly enabled. Changing retention does not invalidate node-selection policy
or migrate existing bindings. Removing an application does not delete its pins.

- A validated proxy connection attempt updates activity. Management reads and
  repeated API acquire calls do not keep a binding alive.
- HTTP, HTTPS CONNECT and SOCKS5 CONNECT hold a revision-scoped activity reference
  from before the upstream dial until the tunnel closes. Failed dials release it.
- Any open tunnel protects the binding, even if no bytes are currently flowing.
  The full idle interval starts again after the last tunnel closes. Merely closing
  a browser does not immediately release its usable exit.
- One maintenance loop uses committed configuration and runs every 30 seconds.
  Deletion is atomic and rolled back on persistence failure; runtime replacement
  fences cleanup. Activity is batch-persisted rather than written per request.
- Process restart grants at least a full idle interval to every loaded binding,
  because interrupted tunnels and the last unflushed activity cannot be recovered.
  Frequent process restarts can therefore delay reclamation.
- The sessions API and Applications table expose `last_used_at`,
  `active_connections`, and `reclaim_at`. No reclaim time is shown for a currently
  active or cleanup-disabled binding. Reclamation occurs on the next sweep after
  the displayed time, not at an exact wall-clock deadline.

Clients remain ordinary HTTP/SOCKS5 clients. Keep the same session ID while an
exit is useful; use a **new, application-chosen ID** when replacing that session.
Do not generate a fresh ID for every TCP connection. An unused old ID expires
without a custom release call. Reusing it after reclamation may select another
node. Cleanup does **not** promise that a new ID selects a different exit, nor
that a site's HTTP 403 is automatically treated as a bad proxy.

Long-lived account identities should normally leave idle cleanup disabled.
Temporary scraper sessions can opt into a retention period longer than their
normal resource-saving pauses (for example 24h). Size `max_sessions` for the
number of replacements retained during that interval; cleanup is not permission
to exceed the configured capacity before the deadline.
