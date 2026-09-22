# Scraping jobs: pooled and pinned

A Job independently configures where its nodes come from (`selection`) and how
connections use them (`mode`). Its Profile always bounds the candidate set.

| Node selection | Behavior | Target URL required? |
|---|---|---|
| `manual` (default) | Use all eligible nodes in your chosen Profile; no Job target benchmarks or speed ranking | No |
| `auto` | Benchmark the Profile against `target_url` and select up to `size` successful nodes by elapsed time adjusted by recent success rate | Yes |

Either selection can be combined with either connection mode:

- `pooled`: one existing Profile-bound Endpoint selects among the eligible nodes.
  Selection balances active connections; ties rotate.
  Pool-wide sticky affinity and `fail_open` do not override this job's selection.
- `pinned`: one fixed Endpoint accepts a session ID in the proxy username,
  `<username>-session-<id>`. The first connection assigns a node; later connections
  keep the assignment, including after service restart. No SDK or API call is needed.
  New sessions spread across the selected nodes by active lease count. Multiple
  sessions can share a node; this is not an exclusive-IP reservation.

Both policies work in `pool` or `hybrid` mode, with one fixed Endpoint per Job.
All sessions of a pinned Job share its port. Pinned assignments select nodes,
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
  - name: catalog
    address: 127.0.0.1
    port: 2324
    profile: scrape-candidates
  - name: accounts
    address: 127.0.0.1
    port: 2325
    profile: scrape-candidates
    username: scraper
    password: change-me

jobs:
  - name: catalog
    mode: pooled
    selection: manual       # default when omitted
    profile: scrape-candidates
    endpoint: catalog
    concurrency: 8
    timeout: 15s

  - name: accounts
    mode: pinned
    selection: manual
    profile: scrape-candidates
    endpoint: accounts
    concurrency: 4
    timeout: 15s
    session_ttl: 30m
    max_sessions: 1024
```

In the WebUI, open **System Settings → Job configuration** (administrator only).
Create a Profile to define candidates, create a matching Endpoint, then add a Job.
Every Job field is editable: connection and selection modes, Profile/Endpoint,
client concurrency/timeout, pinned session lifetime/capacity, and automatic target
URL, node limit, refresh interval, batch/worker limits, response size/status/text.
The editor shows the settings relevant to the chosen modes and retains inactive
values when switching modes. **Save configuration** applies Jobs, Profiles and
Endpoints together using the configuration revision. Failed saves and conflicts
retain the draft; **Reload settings** explicitly discards it after confirmation.
Leaving settings with unsaved changes also requires confirmation.

The **Scraping jobs** page shows candidates, selected nodes and automatic
benchmark results, and lets you trigger a benchmark for auto Jobs. Its access
helper generates HTTP or SOCKS5 URLs for a Job/session; credentials start masked
and can be revealed or copied. For pinned Jobs, clicking **Get endpoint / create
session** acquires a real assignment. Ordinary scraper clients still need no
management API: they can construct the session username directly.

The session table is read-only until you explicitly release a binding. It supports
search, Job/state filters and pagination, including expired or removed-Job records.
Viewing or refreshing this table never creates or renews a session. Stop the scraper
and close its old connections before confirming **Release session**.

YAML and the revision-checked `PUT /api/settings` JSON API remain supported.
Each Job with an `endpoint` requires its own enabled Endpoint with the same Profile.
Pinned Endpoints require a username and nonempty password. With Jobs configured,
an incompatible mode/Profile/Endpoint change is rejected. Legacy pinned Jobs
without `endpoint` retain dedicated node ports and require `hybrid` or `multi-port`.

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
Catalog:   http://127.0.0.1:2324
Account A: http://scraper-session-account-a:change-me@127.0.0.1:2325
Account B: http://scraper-session-account-b:change-me@127.0.0.1:2325
```

The pinned port accepts HTTP forwarding, HTTPS CONNECT, and SOCKS5 CONNECT with
username/password authentication. For SOCKS clients use `socks5h://` (or your
client's remote-DNS equivalent) with the same credentials and port. SOCKS4 and UDP
are unsupported on pinned Endpoints. Session IDs are case-sensitive and consist
of 1–128 ASCII letters, digits, `_` or `-`; IDs are scoped to their Job. Encode
special characters in URL credentials, or use your client's separate auth fields.

For example, with an existing Python `requests` installation:

```python
import requests

account = requests.Session()  # one client/cookie jar per logical account session
account.trust_env = False
proxy = "http://scraper-session-account-a:change-me@127.0.0.1:2325"
account.proxies.update(http=proxy, https=proxy)
response = account.get("https://example.com/", timeout=15)
response.raise_for_status()
```

The proxy validates every new upstream connection: a missing/invalid session ID
or wrong password fails authentication, and an unavailable/expired assignment
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
| `POST /api/jobs/accounts/release` | `{"session":"account-a"}` | Explicitly end the assignment |
| `POST /api/jobs/accounts/feedback` | See below | Auto pinned only: target-specific application observation for an assigned node |

Manual refresh has a five-second minimum interval. A refresh response may still
show `refreshing: true` when another call is already working. Acquisition returns
503 when no eligible nodes are ready for a new assignment. Unknown Jobs return
404; conflicting operations, including refresh/feedback on manual Jobs, return
409. Manual status returns `measured: 0`, `refreshing: false`, `size: 0` (no size
cap), and `selected: [{"node":"..."}]` without invented target measurements.

Ready access example (credentials, if configured, are URL encoded):

```json
{
  "job": "accounts", "mode": "pinned", "selection": "manual", "state": "ready",
  "session": "account-a", "node": "node-<stable-key>",
  "proxy_url": "http://scraper-session-account-a:change-me@127.0.0.1:2325",
  "concurrency": 4, "timeout": "15s", "timeout_ms": 15000,
  "expected_status": 200, "expires_at": "2026-09-19T18:00:00Z"
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
pinned Endpoint, the proxy acquires and validates the assignment internally.
Optionally use `/acquire` to inspect its node, expiry, and state; use returned URLs
only when `state == "ready"`. Other states intentionally omit `proxy_url`:

- `paused`: the assigned node was removed, is unhealthy, or (auto only) failed target checks.
  A later acquisition can resume the same node when it recovers.
- `expired`: the fixed lease lifetime ended; acquisition does not extend it.
- `policy_changed`: Job settings or candidate Profile changed since assignment.

Stop requests and close the old HTTP client's connections before recovery. End
the old logical session with `/release`, then reconnect and restore/recreate
the application's login state explicitly. Alternatively use a new session ID with
a fresh application session, and clean up the old assignment later. Even expired
IDs never silently rebind. `/release` ends the binding, so old clients must stop
using that ID before another client reuses it.
There is no per-job global blacklist on a pinned failure and no automatic node
switch for an existing session.

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
Legacy dedicated node ports (Jobs without `endpoint`) gate acquisition only;
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
