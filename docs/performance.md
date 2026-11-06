# Where Torment's time goes

Measured on AWS (`i4i.2xlarge`, 8 vCPU, local NVMe), Torment 256 workers, one write per request, about 7,000 requests/s. Postgres, Torment and TigerBeetle share the 8 vCPUs.

## The plateau
| Workers | Requests/s | p50 |
|---|---|---|
| 64 | 7,027 | 3.8 ms |
| 256 | 6,135–7,500 | 25–27 ms |

Little's law: throughput = concurrency ÷ latency. Four times the workers bought four times the latency and no more throughput, so something saturated near 7k requests/s.

## CPU by process (`perf record -a`, 15 s)
| Process | Share of all CPU |
|---|---|
| postgres | 50% |
| torment | 22% |
| idle | 13% |
| tigerbeetle | 10% |

TigerBeetle runs on one core and uses less than all of it. It is not the bottleneck. Batching proves it: 100 writes per request reached about 19,800 applied writes/s through the same TigerBeetle.

## Inside Postgres (`pg_stat_statements` with `track_planning`, 40 s)
| Query | Calls | Plan total | Exec total | Plan/call | Exec/call |
|---|---|---|---|---|---|
| `GetWallets` (every transfer) | 271,765 | 103 s | 21 s | 0.38 ms | 0.08 ms |
| `ListWalletsByPattern` (statement reads) | 15,206 | 8 s | 19 s | 0.50 ms | 1.27 ms |

- **`GetWallets` is planned on every call.** pgx prepares it, but Postgres keeps choosing a custom plan for the array values over the cached generic one. Planning costs five times the execution, and this one query uses about 2.5 of the 8 cores.
- **Forcing the generic plan made it worse:** `plan_cache_mode = force_generic_plan` halved throughput (3,546 requests/s, Postgres at 620% CPU). Without the array values, the generic plan for the `unnest` join is a bad one. Replanning was the cheaper option.
- **Statement reads are expensive per call:** 5% of requests, 1.27 ms and 770 buffer reads each. The pattern `user:N:*` becomes a regex over addresses.

## What would move the ceiling (in Torment, not the benchmark)
- **Cache confirmed wallets in memory** (the deferred feature 11). `GetWallets` then leaves the transfer path, and most of Postgres's CPU goes with it.
- **Or make the lookup cheap to plan:** for example, one lookup per distinct address with a simple `where address = $1` that a generic plan handles well.
- **Coalesce concurrent requests into one TigerBeetle batch and one wallet lookup.** Batching already shows the payoff: about 4× the applied writes on the same hardware.

## After the wallet cache (Torment `c3701be`)
Applied writes/s, best closed loop, bank-sharded workload:

| Contention | Before | With cache |
|---|---|---|
| Hot (1 shard), 256 workers | 5,266 | 7,711 |
| Medium (16 shards), 256 workers | 5,309 | 6,336 |
| Spread (256 shards, uniform), 256 workers | 4,090 | 1,534 |
| Spread, 100 writes per request | 21,609 | 45,198 |

- **`GetWallets` disappeared from Postgres.** In a 45 s spread run with the cache, Postgres used 4% of CPU (it was 50%).
- **The spread case got slower while the machine sat 77% idle.** Statement reads rose to about 500 ms at p50, and every other request waited behind them.
- **Cause: one TigerBeetle client, one request in flight.** TigerBeetle's docs: "TigerBeetle clients can have at most one in-flight request." Queued calls of the same type are merged into one request; different types are not. A statement read makes one history query per wallet (up to about 21 round trips), and Torment shares a single client, so every TigerBeetle call from every HTTP request waits in that one queue. With Postgres no longer slowing requests down first, more of them reach the queue at once. How the mix of request types plays out at the spread level specifically is inferred from these numbers, not measured.
- **Next fix, in Torment:** a small pool of TigerBeetle clients, or one batched request per statement read instead of one per wallet.

## After splitting the TigerBeetle clients (Torment `789887b`)
Writes and reads get separate clients, and a statement read sends its per-wallet queries at once. Results are in `results/aws-split/` (matrix) and `results/aws-fresh-spread/`.

| Best closed loop, applied writes/s | Old | New |
|---|---|---|
| Hot | 5,266 | 7,081 |
| Medium | 5,309 | 6,610 |
| Spread (matrix) | 4,090 | 1,759 |
| Spread, 100 writes per request | 21,609 | 45,074 |

Statement p99 is 2.4–3.3× lower at every level.

- **The matrix's spread number depends on run order.** Spread runs last, after about 16 minutes of hot and medium writes. On a fresh database with spread run first (256 workers), new beats old:

  | Spread, fresh database | Old | New |
  |---|---|---|
  | Cold, wallets created on first use | 2,517 | 3,909 |
  | Warm | 4,280 | 7,891 |
  | Warm, no reads | 6,016 | 13,810 |

- **At spread, reads hold the writes back.** On the database left after the matrix, warm, the 10% reads cut writes from 15,511/s to 3,986/s. With reads on, disk reads rose from 522/s to 4,686/s, and the machine sat 75% idle (Postgres 4%, Torment 12%, TigerBeetle 8%). Every operation, balance reads included, had the same p50 (about 38 ms).
- **Cause (inferred from the numbers above):** TigerBeetle applies requests one at a time. Statement reads for uniformly picked users miss its cache and read from disk, and every request behind them waits. That includes writes sent on the other client. Separate clients remove the queue inside Torment, not the one inside TigerBeetle.
- **What could move it, in Torment:** serve statements from a store that isn't on the write path (a read replica of history, or Postgres), or cap how many history reads are in flight so writes keep getting through.
