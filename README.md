# Load balancing strategies (reference: `lbpkg/lb.go`)

This project implements four load-balancing strategies in Go. Below is a simple explanation of how each strategy works, based directly on the code in `lbpkg/lb.go`.

The default backend list is defined in `lbpkg/lb.go` as:
- `http://localhost:8080`
- `http://localhost:8081`
- `http://localhost:8082`

All strategies keep a small amount of state and use mutexes to protect it when accessed concurrently.

## Round Robin

Purpose: distribute requests evenly across servers in a fixed, repeating order.

How it works:
- Keeps `Servers`, `PerviousServer` (last index used), and a mutex.
- On each selection (`Next`):
  - Computes the next index as `(PerviousServer + 1) % len(Servers)`.
  - Updates `PerviousServer` and returns that server.
- The HTTP handler reads the `path` query, forwards the request to the selected server, and returns the response body.
- `RemoveServer` removes a server from the list when it goes down.

Characteristics:
- Very simple and predictable.
- Ignores server capacity or current load.
- Even distribution as long as all servers are healthy and similar.

## Weighted Round Robin

Purpose: favor some servers more than others based on configured weights.

Key state:
- `Servers`, `Weights`, `CurrentIndex`, `CurrentWeight`, `MaxWeight`, and `GcdWeight`.

How it works (`nextServer`):
- Iterates over indices in a cycle.
- When the index wraps to 0, decreases `CurrentWeight` by `GcdWeight`.
  - If `CurrentWeight` drops to 0 or below, reset it to `MaxWeight`.
- Chooses the current server when `Weights[CurrentIndex] >= CurrentWeight`.
- The effect is that servers receive traffic in proportion to their weights.

Handler and maintenance:
- The HTTP handler forwards using `nextServer()` and returns the backend response.
- `RemoveServer` removes a server from the pool if it goes down.

Characteristics:
- Preserves round-robin order but skews selection according to weights.
- Requires weight configuration per server.

## Least Connections

Purpose: send the next request to the server with the fewest active connections.

Key state:
- `Servers`, `ActiveConnections` (one counter per server), and a mutex.

How it works:
- On selection (`Next`):
  - Scans for the index with the minimum value in `ActiveConnections`.
  - Increments that server’s active-connection counter and returns it.
- After the request finishes, `Release(server)` decrements that server’s counter.
- The HTTP handler wraps this pattern: it increments before the outbound call and decrements afterward.

Characteristics:
- Adapts to slow or overloaded servers by sending them fewer new requests.
- Ties are broken by the first minimum found during the scan.

## Consistent Hashing (with cache and health checking)

Purpose: route requests that share a key to the same server, while minimizing remapping when servers are added or removed.

Key parts:
- Hash ring with virtual nodes: `Replicas`, `Keys` (sorted `uint32` hashes), and `Ring` (hash -> server).
- Liveness: `Alive` map tracks whether a server is considered up.
- Lookup cache: `Cache.Table` caches hash-to-server decisions; `InvalidateAffected` clears only entries in affected hash ranges when topology changes.

Hashing:
- Each server replica key is hashed with FNV-1a (e.g., `server#replicaIndex`) to place it on the ring.
- For request routing in the HTTP handler, a stable 5‑tuple hash is computed using Murmur3 over:
  - source IP, destination IP, source port, destination port, and protocol (TCP = 6).
- If the 5‑tuple is not available, the handler falls back to hashing the connection addresses.

Add / Remove:
- `Add(server)`:
  - Computes hashes for each replica, inserts them into the sorted `Keys` and `Ring`.
  - Collects the predecessor→new-node intervals as affected ranges.
  - Marks the server alive, then invalidates cache entries whose keys fall into affected ranges.
- `Remove(server)`:
  - Marks the server not alive.
  - Deletes each replica from `Ring` and `Keys`.
  - For each deletion, records the predecessor→successor interval as an affected range.
  - Invalidates cache entries in those ranges.

Lookup:
- `Get(h uint32)`:
  - Returns a cached mapping if present and the server is alive.
  - Otherwise binary-searches for the first ring key `>= h` (wrapping to 0) and returns its server.

HTTP handler behavior:
- Computes the stable 5‑tuple hash for the incoming connection.
- Picks a server with `Get(h)` and forwards the request to `server/path`.
- On error from the selected server:
  - Logs and removes that server from the ring.
  - Recomputes the destination and retries once.

Characteristics:
- Keeps most keys on the same server when membership changes.
- Cache reduces repeated ring lookups; cache entries are selectively invalidated after adds/removes.

## Health checking

File: `HealthChecker(rr, wrr, lc, ch)`.

What it does:
- Periodically sends `GET /health` to each server in `Servers`.
- On failure (error or non-200):
  - Removes that server from: Consistent Hash ring, Round Robin, Weighted RR, and Least Connections.
  - Marks alive state accordingly and logs the change.
- On recovery (200 after being down):
  - Adds the server back to the Consistent Hash ring and appends it to the other strategies’ server lists.
- Sleeps for 3 minutes between checks.

Notes:
- All strategies share the same `Servers` notion but maintain their own internal state.
- The HTTP handlers accept a `path` query parameter and forward to `server/path`.
