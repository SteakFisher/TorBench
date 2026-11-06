# Decisions

TorBench runs the same brokerage workload against Torment (Postgres + TigerBeetle) and Formance ledger v2.4.17 (Postgres), one at a time, and checks every run for correctness.

## Workload
- **Accounts:** `world`; 256 sets of `bank:N`, `broker:clearing:N` and `broker:fees:N`; `user:N:cash` and `user:N:shares` per user. Every account is credit-normal on Torment (liability, revenue, equity), so a balance is credits minus debits, the same as Formance's input minus output.
- **Assets:** `USD/2` and tickers `TK00/6`…, so shares can be fractional. Torment registers them as currencies. Formance needs no registration.
- **Writes:** deposit 10%, withdraw 5%, buy 43%, sell 35%, overdraft buy 5%, dividend to 50 users 2%. Deposits, withdrawals and dividends move cash between a `bank:N` account and users; only the seed touches `world`. A buy or sell is one atomic transaction of three postings (cash, shares, fee). Sells of shares the user lacks are rejected, as on a real broker.
- **Reads (10%):** one user's cash balance, and one user's statement (the latest 20 transfers across `user:N:*`).
- **Contention:** `-shards` picks how many bank, clearing and fee sets writes spread over (1 means every write shares the same three), and `-zipf` concentrates activity on a few hot users. The matrix runs three levels: (1, 1.1), (16, 1.1) and (256, uniform).
- **No shared account at 256 shards.** The first AWS run sent deposits, withdrawals and dividends through `world`, so 17% of writes shared one account at every level. Formance locks each touched account's balance row, so those writes queued for seconds (deposit p50 627 ms, withdraw p50 2.6 s at 16 workers) and starved the rest, while Torment was unaffected. That is a real cost of routing money through one `@world` account on Formance; those results are kept in `results/aws-world-hot/`.
- **Not measured:** totals across all users (`user:*:cash`). Torment caps pattern balances at 1000 wallets by design, so the two systems can't answer the same query.

## Same guarantees on both
| Guarantee | Torment | Formance |
|---|---|---|
| Idempotency | posting ids (transaction id + i) | `Idempotency-Key` header, or `ik` per bulk element; the value is the transaction id |
| Atomic multi-posting trade | one chain | one transaction with several postings |
| No overdraft except `world` | balance-limit flags | the default balance check (no `force`) |

Ids are 128-bit: the process start time in milliseconds in the high 64 bits, a counter in the low 64, so they always increase, which TigerBeetle needs for throughput.

## Configuration choices
- **Same Postgres for both:** `postgres:17`, `max_connections=200`, `shared_buffers=1GB`.
- **Formance:** the standalone `serve` and `worker` images, Postgres pool of 40 and `BULK_PARALLEL=10` (its example compose). Two feature sets, each in its own ledger: `default` (Formance's defaults) and `minimal` (moves history, log hashing and metadata history off).
- **Formance `BULK_MAX_SIZE=10000`:** the default caps a bulk at 100 elements. Torment takes up to 8189 per request.
- **Formance bulk runs with `parallel=false`:** with `parallel=true`, results come back in completion order, and an error result does not say which element it belongs to. So which transactions failed can't be told.
- **Formance balances use `/aggregate/balances`:** account volumes (`expand=volumes`) need the `MOVES_HISTORY` feature, which `minimal` turns off.
- **TigerBeetle in Docker on a Mac needs `--development`**, because Direct IO is unavailable. That flag also shrinks the request to 32 KiB (254 transfers), so `--experimental --limit-request=1MiB` restores the production 8189. The grid cache is set to 1 GiB (`TB_CACHE_GRID`), since development mode defaults to 32 MiB. On Linux with NVMe, set `TB_DEV_FLAG=` (empty) to drop `--development`. On AWS the matrix gives TigerBeetle an 8 GiB grid cache: it uses Direct IO, so unlike Postgres it gets no OS page cache.
- **Torment's container needs `seccomp=unconfined`:** the TigerBeetle client uses io_uring. It also connects by IP, because the client does not resolve host names.

## Measuring
- **Closed loop** (`-concurrency`): workers send requests back to back. Gives throughput.
- **Open loop** (`-rate`): requests start on a fixed schedule, and latency counts from the scheduled start, so queueing delay is included. The matrix runs it at 25/50/75/90% of the best requests/s any closed loop reached for that system and level.
- **Batched** (`-batch 100`): 100 independent writes per request: Torment chains, a Formance bulk.
- **Timeout:** 30 s per request. A write that times out is "unknown".

## Correctness check (every run)
1. Snapshot every account's balances before the run.
2. Record each write the ledger accepted in a book of expected changes.
3. Resubmit unknown writes alone with the same ids. Idempotency turns each into accepted or rejected; any still unknown fail the check.
4. Snapshot again. Each balance must have moved exactly by the book, every asset must sum to zero, no account but `world` may be negative, and no overdraft buy may have been accepted.

A failed check is recorded in the result file, and the report flags it.
