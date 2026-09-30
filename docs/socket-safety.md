# Socket pressure and large node inventories

A node inventory is not a connection limit. These protections address two
specific failure paths; they do not promise that any workload with 1,000 nodes
will fit within the host's networking resources.

## Timed-out Job probes

The shared Job store has 16 probe slots. A slot now remains owned until the
request has finished **and** every admitted HTTP dial callback has returned.
Go's HTTP transport may return a timeout before its dial callback returns.
A transport that ignores cancellation therefore keeps its slot, rather than
allowing repeated timeouts to accumulate unbounded unfinished dials. Callbacks
scheduled after the request retires are rejected before entering the outbound.
Existing late-connection cleanup still closes a socket returned after timeout.

An uncooperative dial can occupy a slot indefinitely. If all 16 are stuck,
further Job probes wait for admission or their parent cancellation. This is
intentional containment, not proof that the underlying transport has recovered.

## Local allocation errors are not failed remote nodes

Typed local socket/file-descriptor/memory/address-allocation failures stop
cross-node retries and leave node health and existing Job measurements unchanged.
On Windows this includes Winsock 10024 (WSAEMFILE), 10048 (WSAEADDRINUSE), and
10055 (WSAENOBUFS). These codes have different causes: an address conflict does
not by itself establish port exhaustion. Untyped error strings received from a
remote proxy are deliberately not treated as local resource evidence.

Pool instances share a five-second local-resource backoff, including pool-based
TCP/UDP traffic, active health probes, and Job probes. Fresh OS allocation errors
renew the backoff; merely rejecting another request does not. Existing healthy
connections are not terminated. Traffic errors from attempted connections are
classified as `local_resource`. Validated probe publication also ignores these
failures rather than turning them into durable blacklist entries.

## Limits of this change

This is not a new global socket quota or new-connections-per-second limiter.
Ordinary client traffic still needs bounded concurrency and a sensible request
rate. Job `concurrency` remains a client-side contract, not a server-side ceiling.
The backoff covers pool paths, not subscription fetches or all independent
GeoIP/preflight activity. Protocol-owned QUIC/UDP sessions, connection reuse,
TCP closing states, other applications, and Windows port reservations all affect
actual resource use. Fixing these two bugs cannot guarantee zero port exhaustion.

No Windows registry, dynamic-port-range, or TCP timeout settings are changed.
