# Clustering

This document covers `pkg/cluster`, `pkg/transport`, and `pkg/crdt`. It
explains how tinyagents nodes form a cluster, how envelopes travel between
nodes, how the consistent-hash registry assigns actor paths to nodes, and how
CRDT-backed state converges across the cluster.

All packages described here are **experimental** in v0.1. The interfaces are
stable; the wiring between them (cluster-aware message routing) is post-v0.1
work. See the v0.1 limits section at the end of this document.

See also:
[architecture.md](architecture.md) |
[actor-model.md](actor-model.md) |
[agents-and-teams.md](agents-and-teams.md) |
[llm-providers.md](llm-providers.md)

---

## Why Clustering?

Three workloads motivate multi-node tinyagents deployments:

**Horizontal scale.** A single process handles hundreds of agents, but
provisioning diverse hardware — nodes with GPUs for local models, nodes with
high network bandwidth for hosted providers — requires spreading agents across
machines. The cluster layer makes membership and topology observable to the
application without dictating how work is distributed.

**Shared token budget.** A `*agent.Budget` shared by agents on different nodes
needs a cluster-wide counter. `pkg/crdt.PNCounter` provides this without
requiring a coordination round-trip on every token charge.

**Global task registry.** When agents on any node can own a given task key,
`registry.ClusterRegistry` determines which node is authoritative for a path
and routes accordingly.

---

## Cluster Interface

```go
type Cluster interface {
    Join(ctx context.Context, seeds ...string) error
    Leave(ctx context.Context) error
    Members() []Member
    LocalNode() Member
    Subscribe(h EventHandler) (unsubscribe func())
    Close() error
}
```

`Join` contacts seed addresses and announces this node. Implementations that
do not care about addresses (like `cluster/local`) ignore them. `Join` is
idempotent.

`Members()` returns a snapshot of the current membership view including the
local node. The slice is owned by the caller.

`Subscribe` registers a callback for membership events (`MemberJoined`,
`MemberLeft`, `MemberUpdated`). Events are delivered sequentially per
subscriber via a buffered channel. Multiple subscribers are allowed. The
returned function unsubscribes.

```go
unsub := cl.Subscribe(func(ev cluster.Event) {
    switch ev.Kind {
    case cluster.MemberJoined:
        fmt.Printf("node joined: %s at %s\n", ev.Member.ID, ev.Member.Address)
    case cluster.MemberLeft:
        fmt.Printf("node left: %s\n", ev.Member.ID)
    }
})
defer unsub()
```

### Two implementations

**`cluster.New(id, ...Option)`** — the local single-process implementation.
Its only member is the node itself. Use this for tests, single-node deployments,
and unit-testing code that depends on `Cluster` without spinning up real sockets.

**`cluster/memberlist.New(id, bind, ...Option)`** — UDP-based SWIM failure
detection and eventually-consistent membership gossip. Use this for real
multi-node deployments.

tinyagents is not a consensus system. Membership is eventually consistent:
a node that partitions from the cluster will be declared suspect and eventually
dead according to the SWIM protocol timeouts, but there is no quorum gate on
reads or writes.

### Bringing up a 3-node cluster in one process

The `examples/cluster-gossip` example shows this end-to-end. The essential
structure:

```go
import "github.com/skyforce77/tinyagents/pkg/cluster/memberlist"

// Alpha bootstraps alone.
alpha, err := memberlist.New("alpha", "127.0.0.1:7946")
if err != nil { log.Fatal(err) }
alpha.Join(ctx) // no seeds = single-node bootstrap

// Beta joins alpha.
beta, err := memberlist.New("beta", "127.0.0.1:7947")
if err != nil { log.Fatal(err) }
beta.Join(ctx, "127.0.0.1:7946")

// Gamma joins alpha as well.
gamma, err := memberlist.New("gamma", "127.0.0.1:7948")
if err != nil { log.Fatal(err) }
gamma.Join(ctx, "127.0.0.1:7946")

// After gossip converges, each node sees three members.
time.Sleep(500 * time.Millisecond)
fmt.Println(len(alpha.Members())) // 3

// Orderly departure.
gamma.Leave(ctx)
beta.Leave(ctx)
alpha.Leave(ctx)
```

The `WithWANProfile()` option switches to larger gossip timeouts suited for
multi-datacenter deployments. `WithMeta(map[string]string{...})` gossips
arbitrary key/value capability advertisements alongside heartbeats.

---

## Transport Layer

```go
type Transport interface {
    Listen(addr string, h Handler) error
    LocalAddr() string
    Dial(ctx context.Context, nodeID, addr string) (Conn, error)
    Close() error
}

type Conn interface {
    Send(env *proto.Envelope) error
    NodeID() string
    Close() error
    Done() <-chan struct{}
}
```

`pkg/transport/tcp` is the v0.1 implementation. Create one per node:

```go
import "github.com/skyforce77/tinyagents/pkg/transport/tcp"

t := tcp.New("node-1",
    tcp.WithHeartbeatInterval(5*time.Second),
    tcp.WithHeartbeatMiss(3),
    tcp.WithDialTimeout(5*time.Second),
)

// Start accepting inbound connections.
err := t.Listen("0.0.0.0:8000", func(c transport.Conn, env *proto.Envelope) {
    // handle inbound envelopes
})

// Dial a peer.
conn, err := t.Dial(ctx, "node-2", "10.0.0.2:8000")
conn.Send(&proto.Envelope{Kind: proto.KindMessage, To: proto.PID{...}})
```

**Framing.** Every frame is prefixed with a 4-byte big-endian length. This
makes it safe to stream multiple messages over a single TCP connection and
avoids the "read until delimiter" problem.

**Heartbeat.** A `wire.Heartbeater` runs in the background of every connection.
It emits `proto.KindHeartbeat` frames at `HeartbeatInterval` and declares the
peer dead if no frame of any kind is seen for `HeartbeatMiss` consecutive
intervals. On timeout, `Conn.Done()` closes, which drives reconnection in
the caller.

**Codec.** The default codec is `wire.GobCodec`, which uses `encoding/gob` —
stdlib-only with no code generation. The `Codec` interface is pluggable:
any implementation that marshals and unmarshals `*proto.Envelope` can be
substituted via `tcp.WithCodec(myCodec)`.

```go
type Codec interface {
    Marshal(env *proto.Envelope) ([]byte, error)
    Unmarshal(b []byte, env *proto.Envelope) error
}
```

---

## Cluster-Aware Registry

`registry.ClusterRegistry` wraps any `registry.Registry` with a
consistent-hash ring built from the current cluster membership. It answers the
question "which node owns this actor path?" in O(log N) and rebuilds the ring
on every membership event.

```go
import (
    "github.com/skyforce77/tinyagents/pkg/registry"
    "github.com/skyforce77/tinyagents/pkg/cluster"
)

local := sys // *actor.System satisfies registry.Registry
cr := registry.NewClusterRegistry(local, clusterImpl, 128)
defer cr.Close()

owner := cr.OwnerOf("/user/my-agent") // stable node ID
isHere := cr.IsLocal("/user/my-agent") // true iff owner == LocalNode().ID
```

**Ring construction.** Each cluster member is mapped to `replicas` virtual
nodes (default 128). Each virtual node is hashed with `FNV-64a(nodeID+"#"+i)`.
The ring is sorted by hash. For a given path, the owner is the node whose
virtual node has the smallest hash greater than or equal to `FNV-64a(path)`.

128 replicas gives a good balance between ring size and load distribution for
clusters up to a few hundred nodes.

**Consistency model.** The ring is eventually consistent: between a membership
change and the ring rebuild, `OwnerOf` may return a stale result. This is
acceptable for soft routing (directing new work to the right node) but not for
hard ownership guarantees.

---

## CRDT State

`pkg/crdt` provides four conflict-free replicated data types and a Replicator
that broadcasts deltas between nodes.

### CRDT types

| Type | Operations | Merge rule | When to use |
|---|---|---|---|
| `GCounter` | `Inc(n)`, `Value()` | Per-node max | Monotonically growing counters: request counts, total tokens produced. |
| `PNCounter` | `Inc(n)`, `Dec(n)`, `Value()` | Two GCounters, one per direction | Counters that can go negative: rate-limited capacity, available slots. |
| `LWWRegister[T]` | `Set(v)`, `Get()` | Highest hybrid timestamp wins; equal timestamp tiebreak by NodeID | Last-write-wins configuration registers, feature flags, model assignments. |
| `ORSet[T]` | `Add(v)`, `Remove(v)`, `Contains(v)`, `Elements()` | Union of add-tags minus union of tombstone-tags; add-wins | Distributed membership sets: active session IDs, registered capabilities. |

All types are concurrent-safe (mutex-protected internally). The full godoc is
at `pkg/crdt`.

### Replicator

The `Replicator` ties CRDTs to a broadcast function and handles serialization,
echo suppression, and periodic anti-entropy.

```go
// bcast is any func([]byte) error that fans out to all peers except self.
rep := crdt.New("node-1", bcast,
    crdt.WithSyncInterval(10*time.Second), // periodic anti-entropy
)

gc := crdt.NewGCounter(crdt.NodeID("node-1"))
rep.Register("request-count", gc, "gcounter")
rep.Start(ctx)

// On local mutation, broadcast immediately.
gc.Inc(1)
_ = rep.Replicate("request-count")

// On incoming payload from transport or gossip layer.
rep.Deliver(rawPayload)
```

`RegisterType` extends the type registry for generic types or custom CRDTs:

```go
func init() {
    crdt.RegisterType("orset[string]", func(nodeID string) crdt.CRDT {
        return crdt.NewORSet[string](crdt.NodeID(nodeID))
    })
}
```

The type string must match on sender and receiver.

`WithSyncInterval` enables periodic anti-entropy: every interval the
Replicator re-broadcasts every registered CRDT's snapshot. This heals nodes
that missed payloads during a partition or joined late.

The `examples/crdt-counter` example demonstrates three `PNCounter` replicas
sharing an in-process broadcast bus and converging to the same value.

---

## v0.1 Limits

The following items are on the roadmap but not yet implemented:

- **No cluster-aware message routing.** The TCP transport exists; so does the
  cluster registry that says which node owns a given path. The wiring that
  automatically routes `ref.Tell` and `ref.Ask` to the right node is post-v0.1
  work. Cross-node communication currently requires application-level
  `Transport.Dial` + `Conn.Send`.

- **No persistent CRDTs.** CRDT state is in-memory. A node restart loses all
  local accumulated state. Bootstrapping from a peer's snapshot after restart
  is manual.

- **No encryption on transport.** The TCP transport sends plaintext frames.
  Run it on a trusted private network or wrap `net.Conn` in a TLS dialer at
  the application layer.

- **No cross-DC tuning beyond WAN memberlist profile.** The `WithWANProfile()`
  option switches to larger SWIM timeouts, but there is no automatic
  propagation-delay-aware routing or rack/zone awareness.

---

## Security Considerations

The cluster transport carries actor envelopes between nodes without
authentication or encryption in v0.1. Any process that can reach the bound
address can send arbitrary envelopes.

Recommended mitigations for production deployments:

- Run cluster traffic on a private network segment with no external exposure.
- Use firewall rules or network policies to restrict which addresses can reach
  cluster ports.
- Plan a TLS upgrade path: the `wire.Codec` interface and the TCP dialer
  accept a custom `http.Client`-style extension point, so adding TLS is
  a matter of providing a TLS-wrapped `net.Conn` and a matching codec.

Authentication and authorization for gossip membership and envelope routing
are planned for a future release.
