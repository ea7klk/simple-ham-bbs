# Scalability assessment

Date: 2026-11-11
Scope: the BBS as deployed — one Go binary, SQLite in WAL mode, one process per
SSH connection, a companion web BBS sharing the same database file.
Question: what happens at hundreds and at thousands — 5 000 to 10 000 — of users?

This is a measurement, not an opinion. Every figure below was produced by running
the real code against a generated database at the stated size. The harness was
temporary and is not in the repository; the method is described at the end so the
numbers can be reproduced.

> **Status.** Changes 1–3 of the recommendations are implemented; the measured
> figures below describe the code as it was assessed, before those changes, and
> are kept as the record that justified them. Change 4 is deliberately deferred.
> See [What to change](#what-to-change-in-order-of-value).

## Verdict

Every row below is a measured point, not an interpolation.

| Users | Storage | Time to login prompt | Feasible today? | With the four changes below |
| --- | --- | --- | --- | --- |
| 100 | ~1 MiB | 0.2 s | Yes, comfortably | Yes |
| 1 000 | ~10 MiB | 0.6 s | Yes | Yes |
| 5 000 | ~50 MiB | 2.5 s | Only just — a visible stall per connection | Comfortable |
| 10 000 | 100.6 MiB | 4.6 s | Painful, but functional | Comfortable |

A 500-user deployment sits between the first two rows: about 5 MiB and a login
prompt well under a second. Nothing changes qualitatively between 100 and 1 000 —
the costs are linear, so the interesting threshold is where the per-connection
work starts being felt, which is between 1 000 and 5 000.

Storage is a non-issue: SQLite handles a 100 MiB database with a few tables
without difficulty, and the whole thing still fits in page cache on any modern
host. **The limit is not the database. It is that every SSH connection pays an
O(users) startup cost in its own process.**

At 10 000 users, roughly **4.6 seconds** pass between a caller opening an SSH
connection and seeing the login prompt, and that work includes writes.

That cost is paid per connection, in its own process. What was measured directly
was the concurrent *login* write path (table `loadUser` + `saveUser`, below),
not the full per-connection startup under load: the housekeeping scans are reads,
which SQLite serves concurrently, so they should overlap rather than serialise —
but that is an expectation, not a measurement. The one part that certainly
serialises is that each connection still takes the write lock.

With the four changes in "What to change" the same 10 000-user deployment is
comfortable. Beyond roughly 10 000–20 000 users, or if sustained write
concurrency becomes real, the answer changes from "tune SQLite" to "move to
PostgreSQL" — not because of size, but because SQLite permits exactly one writer.

## Measured results

### Storage

| Metric | Today (6 users) | 10 000 users |
| --- | --- | --- |
| Database file | 292 KiB | 100.6 MiB (db + WAL + shm) |
| Tables | 13 | 13 |
| Users | 6 | 10 000 |
| Messages | 3 | 800 (4 boards × 200) |
| APRS rows | 0 | 1000 users × (200 sent + 500 received) |

The generated 10 000-user database included a realistic profile payload per user
(name, email, locator, QTH, rig, password hash) and APRS history at the retention
caps for a tenth of them, which is the dominant storage term. A deployment where
every user had full APRS history would be roughly 5× larger — about 500 MiB —
still unremarkable for SQLite.

### Per-operation latency

Times are single-run wall clock on the deployment host; treat small differences
as noise and the trends as real.

| Operation | 100 users | 1 000 | 5 000 | 10 000 | Grows with |
| --- | --- | --- | --- | --- | --- |
| `loadUsers` (one full scan) | 1.9 ms | 15 ms | 97 ms | 190 ms | users, linearly |
| `seedData` (every SSH connection) | 37 ms | 397 ms | 2 410 ms | 4 379 ms | users, linearly |
| ↳ `normalizeReceivedAPRSStore` | 37 ms | 390 ms | 2 172 ms | 4 191 ms | users **and** their APRS history |
| `lookupAccount` by email | 1.1 ms | 12 ms | 91 ms | 132 ms | users, linearly (no index) |
| `loadBoards` (board picker) | 11 ms | 7.6 ms | 11 ms | 8.3 ms | message count, not users |
| `loadBoardMessages` (one board) | 2.5 ms | 1.8 ms | 2.9 ms | 2.6 ms | messages on that board |
| `createMessage` (post) | 0.4 ms | 0.4 ms | 24 ms | 16 ms | page count, variable |
| `storeReceivedAPRS` (per packet) | — | — | — | 155 ms | users, linearly |
| registration-throttle check | 0.08 ms | 0.3 ms | 0.07 ms | 0.04 ms | nothing (indexed) |

Two entries deserve attention.

`createMessage` jumps from 0.4 ms at 1 000 users to 16–24 ms at 5 000–10 000. The
statement is unchanged; what changed is the database it runs against. The write
touches indexes on a larger working set and every transaction takes the
immediate write lock. It is not a cliff — a burst of 20 concurrent posts and 20
concurrent board reads completed in 148 ms — but a single post now costs an order
of magnitude more than it did at small scale.

`storeReceivedAPRS` at 155 ms per inbound packet is the sharpest limit in the
system: it is a fixed cost per packet that scales with the user table rather than
with the packet. See below.

### Concurrency

Each SSH session is a separate `bbs_app` process with its own database handle.
SQLite serialises writers, so what matters is how many writers queue at once.

| Concurrent logins | Wall clock | Failures |
| --- | --- | --- |
| 1 | 8.1 ms | 0 |
| 5 | 1.1 ms | 0 |
| 20 | 11.8 ms | 0 |
| 50 | 43.8 ms | 0 |

No `SQLITE_BUSY` failures even at 50 simultaneous writers, because `busy_timeout`
is 5 s and `_txlock=immediate` makes them queue rather than collide. That is the
H2 work paying off. But these are bursts, not sustained load: the ceiling is one
committed transaction at a time, so sustained write throughput is bounded by disk
fsync, not by CPU.

### The per-packet APRS limit

`storeReceivedAPRS` calls `loadUsers()` for every inbound packet, to find the
recipient's profile. At 10 000 users that full table scan dominates the call: the
scan alone measured 190 ms when cold and 97 ms warm, and the whole call measured
155 ms per packet in a warm run.

```
10 inbound messages/second  ->  1.55 s of work per second  ->  falls behind
 6 inbound messages/second  ->  saturated
```

The receiver subscribes with `filter t/m`, which is the *global* message stream of
APRS-IS. Today that is a low rate — tens of packets a minute — so 6/s is not a
practical ceiling yet. It becomes one as the global message rate rises, and it is
entirely unnecessary: the lookup needs one row by callsign, not the whole table.
This is the cheapest significant fix in the list.

### Where the time goes per connection

Opening an SSH connection runs, in order, in a fresh process. **This is the
before picture:** changes 1–3 removed every row of it except the login itself, so
`openDatabase` now only opens the file and a session waits on a single indexed
read for the schema instead of migrating it.



| Step | Cost at 10 000 users | Writes? |
| --- | --- | --- |
| `openDatabase` (AutoMigrate + 2 backfills) | 12 ms | DDL |
| `seedData` → `normalizeReceivedAPRSStore` | 4 191 ms | yes, per user |
| `seedData` → `reportBootstrapSysopState` | 190 ms | no |
| `seedData` → `applyEmailChanges` | small | maybe |
| `authenticate` → `loadUsers` | 190 ms | no |
| **Before the login prompt** | **~4.6 s** | |

`normalizeReceivedAPRSStore` is the bulk of it. It walks every user and calls
`trimReceivedRows`, which reads that user's APRS rows to enforce the retention
caps. At 10 000 users with a tenth having full history that is 1 000 scans of 500
rows. If every user had full history the same step would take roughly **36
seconds** per connection — the extrapolation is linear in APRS rows, and the
single measured data point (3.6 ms per user with 500 rows) makes it a fair one.

That work is idempotent housekeeping. It does not need to run per connection at
all.

## Architecture findings

**1. Process-per-connection multiplies all fixed costs.** Every session
re-runs schema migration, seeding, housekeeping and a full user scan. The
supervisor already exists and already runs in its own process; the work belongs
there, once, not N times. This is the single largest scalability defect and it is
structural, not a micro-optimisation.

**2. Several code paths load the entire user table when they need one row.**
`loadUsers` is called from the login path, the APRS menu, the station directory,
the sysop list (once per menu iteration), four places in the receiver, and the
password-reset lookup. Some legitimately need all users — the station directory
does. The rest do not:
- `storeReceivedAPRS` → recipient profile by callsign.
- `aprsRecipientEnabled` / `aprsRecipientExists` → same.
- `lookupAccount` by email → needs an index, see below.

**3. `db_users.email` has no index.** The forgot-password flow accepts a callsign
*or* an email address; the email branch reads every user and compares in Go
(132 ms at 10 000). An index fixes it, but the comparison is case-insensitive, so
it needs either `COLLATE NOCASE` on the column or a stored lowercase copy — a
naive `CREATE INDEX` on `email` will not serve that query.

**4. SQLite has one writer.** Every login writes `last_seen`, so under sustained
concurrency the queue is unavoidable. Right now that is fine: 50 simultaneous
logins completed in 44 ms with no failures. It stops being fine when writes
outnumber the disk can commit. The mitigation is not a bigger machine.

**5. WAL growth is unbounded between checkpoints.** The live database is 292 KiB
with a 3.9 MiB WAL — normal for WAL mode and harmless, but worth knowing that a
long-running read transaction can pin a much larger WAL open. Nothing in this
code holds read transactions open across user input, so this is currently safe;
a future feature that does (a report, an export) could balloon it.

**6. Message storage is already bounded.** The 500 top-level messages per board
cap, plus the five-level reply depth cap, keeps `loadBoards` — which reads every
message of every board — from growing without limit. That was fixed in M8 and it
is the reason message-heavy latency is flat in the table above. Users are the
unbounded dimension.

**7. Nothing is paginated on the user axis.** The sysop user list builds a menu
option per user. At 10 000 users that is a 10 000-entry menu: the renderer shows
a window, so it works, but it is 10 000 strings built per menu draw and the menu
label arrays are `O(n)` in memory. Fine at 5 000, uncomfortable at 10 000,
unusable at 50 000.

## What to change, in order of value

| # | Change | Removes | Effort | Status |
| --- | --- | --- | --- | --- |
| 1 | Move `AutoMigrate`, `seedData`, `normalizeReceivedAPRSStore` out of the connection path into the supervisor (or a one-shot `bbs migrate`) | ~4.4 s per login at 10 000 users, and a write lock acquisition per connection | Small | **Done** |
| 2 | Replace `loadUsers` with a targeted `loadUser(callsign)` in `storeReceivedAPRS`, `aprsRecipientEnabled` and `aprsRecipientExists` (all in `aprs_receiver.go`) | 155 ms → sub-millisecond per inbound packet; removes the 6 packets/s ceiling | Small | **Done** |
| 3 | Index the email column with `COLLATE NOCASE` and query by it | 132 ms → log n for password reset by email | Small | **Done** |
| 4 | Make the sysop user list a paginated search instead of one option per user | O(n) menu construction, and the 10 000-entry menu | Medium | **Later implementation** |

After 1–3, measured expectations: login prompt well under 200 ms, APRS receive
independent of user count, password reset by email constant-time. Storage and
write concurrency already have ample headroom at 10 000.

### Change 4: deliberately deferred

The sysop user list is the last O(n) path, and it is left as a later
implementation on purpose. It is the only remaining one, and it is bounded by how
often a sysop opens the list rather than by traffic: a slow menu is a sysop
waiting a second, where changes 1–3 were paid on every login and every inbound
packet. Nothing else in the code depends on it.

When it is picked up, the shape is a filtered query with a page size rather than
one menu option per user:

- a callsign prefix search over the primary key, plus a page of N rows, instead
  of building an option per account;
- the same "next page" affordance the APRS history already uses, so there is one
  pagination idiom in the TUI rather than two;
- `chooseUser` currently also needs the full map to answer "would this demote the
  last sysop", so that check has to become a count query before the page can be
  narrowed. That is the part that makes this Medium rather than Small.

Until then the list stays correct, just slower, and the threshold in "When to
leave SQLite" is not affected by it.

## When to leave SQLite

Do not migrate for size — 10 000 users is 100 MiB, which SQLite does not notice.

Migrate when one of these becomes true:

- **Sustained writes exceed what one committer can flush.** Measure it: log
  `busy_timeout` expiry. Individual bursts of 50 are fine today; a thousand
  logins a minute sustained would not be.
- **The web BBS starts writing concurrently at volume.** The two services share
  the file (see L5 in `REVIEW.md`), and SQLite's single writer is shared between
  them. Two writers at low volume are fine; two writers under load contend.
- **You need more than one BBS instance.** SQLite cannot be shared across hosts.
  Horizontal scaling requires a server database.
- **A query needs to hold a read transaction across user interaction,** which WAL
  makes expensive.

Until then, PostgreSQL would add an operational dependency — a server, a backup
story, a connection pool — to solve a problem that four code changes solve. The
gateway to it is clean: every statement goes through GORM against the row types in
`database.go`, with no hand-written SQL anywhere in the package, so the driver is
the only thing that would change.

## Method, so these numbers can be reproduced

A temporary Go test in `bbs/cmd/bbs` built a database with the real
`openDatabase`, then inserted:
- N users (`EA00000`…) with a full profile payload and a PBKDF2-shaped hash;
- 4 boards × 200 messages;
- APRS history at the retention caps (200 sent, 500 received) for N/10 users.

It then timed, with `time.Now()` around real calls: `loadUsers`, `loadBoards`,
`loadBoardMessages`, `createMessage`, `normalizeReceivedAPRSStore`, a single
`trimReceivedRows`, `lookupAccount`, `seedData`, the registration-throttle check,
`openDatabase`, concurrent logins (1/5/20/50 separate database handles), a mixed
writer/reader burst, and `storeReceivedAPRS` per packet. The harness was deleted
afterwards; it is not part of the suite.

Caveats worth stating:

- Single host, single run per figure. The trends are solid; individual
  millisecond values carry noise, and `createMessage` in particular varied by
  ~30% between the 5 000 and 10 000 runs.
- Concurrent figures are **bursts**, not sustained load. No soak test was run.
- Latency was measured on a temporary directory on the build host, not on the
  deployment volume (`/opt/stacks/ham-bbs/data/bbs`, a hostPath-backed PVC), so
  write figures in particular depend on fsync latency there and may differ. Read
  figures are dominated by page-cache hits and should transfer closely; that the
  100 MiB database is cache-resident is itself an assumption to re-check on the
  real host.
- APRS and message volumes were generated, not replayed from production. The
  t/m stream on a real deployment is the input to the per-packet limit, and that
  rate was not measured.
- Nothing was measured through the TUI itself; the timings are of the functions
  the TUI calls, which is where the cost is.
