# Security, Concurrency, and Architecture Review

Date: 2026-11-10
Scope: the whole repository at `main` (`a5b114d`) — the Go BBS
(`bbs/cmd/bbs/`, ~4.5k lines plus ~2.8k of tests), container/SSH/WireGuard infrastructure, CI
workflow, and the helper script `aprs-test.py`.
Reviewer: automated review, not a formal audit. This document finds defects; it
does not prove their absence.

## What was done

- Read every Go source file, `Dockerfile`, `docker-entrypoint.sh`,
  `bbs_session.sh`, `sshd_config`, both compose files, the fail2ban jails, the
  workflow, and `aprs-test.py`.
- Ran `go vet ./...` (clean), `go test ./cmd/bbs` (pass, 4.4 s), and
  `govulncheck ./...` (one reachable advisory, see M5).
- Ran three throwaway probes against the real code, then deleted them:
  escape-sequence passthrough through `wrapText`/`panelStyle`, a two-process
  lost-update race on `saveBoards`, and a stale-snapshot race on `saveUsers`.
  All three reproduced the behaviour described below.

## Summary

- **Trust model**: broken at the root. A callsign is an identity here, and the BBS lets anyone claim any callsign.
- **Multi-user concurrency**: unsafe. Every mutation rewrites whole tables from a snapshot read earlier in a different process.
- **Terminal injection**: real. User text reaches other users' terminals with ANSI escapes intact.
- **Defaults**: one publicly known SSH password away from a session on port 2222.
- **Dependency hygiene**: one reachable advisory; dependencies otherwise current.
- **Architecture**: workable at small scale, but the persistence model and the per-connection process model are the source of most findings.

The two structural issues — H2 (whole-table rewrite from stale snapshots) and
the per-connection process model — account for most of the exploitable impact.
Fixing H2 and H1 changes the risk picture more than any of the smaller items.

---

## High

### H1. Anyone can claim any callsign, and the first claimant of a `BBS_SYSOPS` callsign becomes a sysop

**Files:** `bbs/cmd/bbs/auth.go:35-44`, `bbs/cmd/bbs/storage.go:89-100`,
`bbs/cmd/bbs/aprs.go:611-624`, `bbs/cmd/bbs/app.go:20-34`, `bbs/cmd/bbs/app.go:49-56`

**Reachable path:** SSH login → `authenticate()` → callsign not in `users` →
`register()` immediately creates the account with no proof of ownership → the
new account is flagged `IsSysop` if the callsign appears in `BBS_SYSOPS`
(`register()` sets `profile.IsSysop = profile.IsSysop || a.cfg.sysops[callsign]`,
and `applyConfiguredSysops()` re-applies it on every login).

**Why it matters:** callsigns are legal identities in amateur radio. Three
distinct impacts follow from one root cause:

1. **Privilege escalation.** `BBS_SYSOPS=EA1ABC` only grants sysop to the first
   person who registers `EA1ABC`. If that is not the operator — and nothing
   prevents it — the attacker gets the sysop menu: user deletion, board
   deletion, message editing, bulletin publishing. `README.md` documents this
   bootstrap path without warning that it must be claimed before the port is
   exposed.
2. **Transmission as another station.** `sendAPRSMessage` derives the APRS-IS
   passcode from the logged-in callsign (`aprsPasscode(from)`, `aprs.go:620`)
   and transmits under it. APRS passcodes are a public function of the
   callsign, so claiming `EA7KLK` is enough to gate packets to APRS-IS as
   `EA7KLK-0`, and `sendLoginAPRSBeacon` beacons that station's position at
   every login.
3. **Account takeover of a returning user.** An existing account is protected
   by its password, so this is not a takeover of *established* accounts — but
   every callsign that has not logged in yet is claimable.

**Fix:** introduce a real ownership step before an account becomes
identity-bearing. Minimum viable: require a sysop-approved registration queue,
or issue binding tokens out of band (a signed packet, a postcard, a
`/etc/bbs/allowed_callsigns` list). At a minimum, do not auto-grant sysop from
`BBS_SYSOPS`; require the configured callsign to complete an explicit
bootstrap (for example, a generated one-time token printed to container logs on
first start) before it gets the role. Document the bootstrap race in
`README.md` in the strongest terms and require an explicit
`BBS_ALLOW_SELF_REGISTRATION=true` for the current behaviour.

### H2. Every write rewrites the whole table from a stale snapshot; concurrent sessions destroy each other's data

**Files:** `bbs/cmd/bbs/boards.go:35-63`, `bbs/cmd/bbs/bulletins.go:19-37`,
`bbs/cmd/bbs/storage.go:46-86`, `bbs/cmd/bbs/app.go:66-97`

**Reachable path:** each SSH session runs its own `bbs_app` process
(`sshd_config:27`, `bbs_session.sh:11`). Session A calls `loadBoards()` at the
top of `showMessages()`. Session B posts a message; `saveBoards()` runs
`DELETE FROM db_messages; DELETE FROM db_boards` and re-inserts its own
snapshot inside a transaction. Session A posts; its `saveBoards()` deletes the
same tables and writes *its* snapshot, which never contained B's message.

**Proof:** a probe opened two `app` instances on the same SQLite file, gave
both the same board snapshot, appended one message each, and saved in order.
Final board contents: `[from-bob]`. Alice's post was gone, with no error
surfaced to either user.

`saveUsers()` has the same shape and a worse blast radius: it loads all users,
writes the caller's map, then deletes every row not in that map
(`storage.go:77-83`). A probe showed a stale save deleting a user registered
concurrently by another process. Because `authenticate()` calls `saveUsers()`
for `LastSeen`, `applyConfiguredSysops()`, and every profile change, **every
login rewrites the entire user table**. Two simultaneous logins plus one
registration can delete the registration; two simultaneous registrations can
delete one another.

**Why it matters:** silent data loss across users, including account deletion
and lost sysop flags. `sqlDB.SetMaxOpenConns(1)` (`database.go:98`) only
serialises within one process, so it does not help here. WAL plus
`busy_timeout` prevents corruption but not lost updates. Every caller in
`boards.go`, `bulletins.go`, `sysop.go`, and `aprs.go` discards the returned
error (`_ = a.saveBoards(...)`), so a `SQLITE_BUSY` at commit is also silent.

**Fix:** stop rewriting tables. Give each mutation a narrow SQL statement —
`INSERT` a message, `UPDATE` one user row, `DELETE` one board — so concurrent
writers touch disjoint rows. Where a full rewrite is genuinely needed, guard it
with a monotonically increasing `version` column and fail the write if the
snapshot is stale, rather than clobbering. `saveUsers` should update only the
keys it actually changed, never delete by omission. Do this before anything
else in this list; it is the difference between a BBS that loses posts and one
that does not.

### H3. Stored terminal-escape injection into other users' terminals

**Files:** `bbs/cmd/bbs/ui.go:93-99`, `ui.go:865-902`, `boards.go:164-197`,
`boards.go:237-243`, `bulletins.go:42-60`, `screens.go:5-28`, and the
wrapping helpers at `ui.go:176-228`

**Reachable path:** user A posts a board message whose subject is
`hi\x1b[2J\x1b]52;c;ZXZpbA==\x07` (or whose body is a fake login prompt). The
text is stored as-is. When user B opens the board, `messageMenuLabel()` puts the
subject in a menu option and `renderMenuOption()` hands it to
`panelStyle.Render()`; the body goes through `wrapText()` into
`showInfoActions()`. Neither path strips control characters.

**Proof:** a probe pushed a payload containing CSI (`ESC[2J`) and OSC 52
(`ESC]52;...BEL`) through `wrapText()` and `panelStyle.Render()`; both escapes
survived intact to the rendered string.

**Why it matters:** the victim's terminal is under attacker control for the
duration of the render. Realistic abuses: clear/scroll the screen, reposition
the cursor to fake the BBS's own "Enter your BBS password" prompt (credential
phishing inside a service that already handles passwords), OSC 52 clipboard
writes in terminals that allow them, OSC 8 hyperlink spoofing, and title-bar
rewrites. No special position is needed — any registered user can post, and
full name, QTH, and rig fields reach the same renderers via `profileRows()` and
the station directory. Bulletin titles and bodies are sysop-controlled, so they
are lower risk but the same sink.

Note that the APRS paths already get this right: `singleLineAPRSDetail()`
(`aprs.go:366-383`) strips control characters before storage and display, and
every APRS render site uses it. That is the pattern to generalise.

**Fix:** sanitise at the storage boundary, not the render site — strip or
escape C0/C1 controls and `ESC` on write for every user-supplied field
(subject, body, full name, QTH, rig, board name, description), so no future
render path can reintroduce the hole. Also apply a render-time sanitiser in
`showInfo`/`showInfoActions`/`runMenu` as defence in depth. Keep the existing
`singleLineAPRSDetail` behaviour as the reference implementation.

### H4. Publicly known default SSH password, published on all interfaces

**Files:** `.env.example:5`, `bbs/docker-entrypoint.sh:4`,
`compose.yaml:32`, `compose.dockge.yaml:39`, `bbs/sshd_config:10-11`

**Reachable path:** `docker compose up -d` without editing `.env` (or after
copying it and leaving the placeholder) sets the transport user's password to
`change-this-password`. The WireGuard container publishes `2222:2222/tcp` on
`0.0.0.0` (`compose.yaml:13`), and `sshd_config` allows password auth with
`AllowUsers bbs`. `PermitEmptyPasswords yes` is also unconditionally on, which
is only needed when `BBS_GUEST_MODE=true` deletes the password
(`docker-entrypoint.sh:20-26`).

**Why it matters:** the default path yields a reachable SSH account with a
password published in this repository. `ForceCommand` keeps it from being a
shell, but it is a free interactive session into an application that will then
register any callsign the caller types — which chains directly into H1. In
guest mode the same port gives an unauthenticated session; `MaxStartups`
defaults (10:30:100) then bound how much of the service an attacker can
occupy.

**Fix:** refuse to start when `BBS_USER_PASSWORD` is unset or equals the
placeholder and guest mode is off — fail loudly instead of defaulting. Move
`PermitEmptyPasswords yes` inside a `Match` block that only applies when guest
mode is enabled, or generate a random password at first start and print it once
to the container log. Consider binding `2222` to the WireGuard interface only
rather than `0.0.0.0`.

---

## Medium

### M1. No rate limiting on BBS authentication; fail2ban only watches sshd

**Files:** `bbs/cmd/bbs/auth.go:18,75-80`, `bbs/fail2ban/jail.local:9-14`

`authenticate()` counts failures per callsign in memory, for the lifetime of one
session. A new SSH connection resets the counter, and nothing links attempts
across connections. fail2ban reads `/var/log/auth.log`, which receives only
sshd events — the BBS's own "wrong password" events go to `bbs.log` and are
never banned. With guest mode on there is no SSH password to guess at all, so
fail2ban has nothing to see. Each attempt costs a 200k-iteration PBKDF2
(`config.go:12`), so a single connection can also burn CPU cheaply.

**Fix:** persist attempt counters keyed by source address (the BBS can read
`SSH_CONNECTION`) with a lockout window, and add a fail2ban filter for
`/var/lib/bbs/bbs.log`. Prefer `argon2id` over PBKDF2-SHA256, and keep the
iteration count in the stored hash so it can be raised later.

### M2. Inbound APRS traffic can force outbound connections without limit

**Files:** `bbs/cmd/bbs/aprs_receiver.go:126-148`, `bbs/cmd/bbs/aprs.go:710-760`

For every received APRS message that carries a message ID and is addressed to a
user with `EnableAPRS=true`, the receiver calls `sendAPRSAck()`, which dials a
**new** TCP connection to APRS-IS, logs in, writes one packet, and closes. There
is no rate limit, no backoff, and no shared connection. APRS-IS is an open
network and the receiver subscribes with `filter t/m`, so any station on the
planet can generate that traffic. One inbound packet becomes one outbound
dial with a 10 s connect timeout and 30 s deadline; a burst becomes a dial
storm that also serialises the receive loop (the ack is sent synchronously
before the packet is stored).

**Fix:** keep one long-lived APRS-IS session for sending, or queue acks through a
bounded worker with a token bucket and drop-with-log on overflow. Do not let
untrusted inbound traffic allocate outbound sockets one-for-one.

### M3. CI runs fork pull requests on a self-hosted runner with workflow-wide `packages: write`

**Files:** `.github/workflows/docker-image.yml:16-18,27,81,131-141`

`pull_request` triggers the `test` job (`:9-10`) on `runs-on: self-hosted`
(`:27`). Any pull request from a fork executes arbitrary Go code on that
runner, which is a persistent host, not an ephemeral VM. `permissions` grants
`packages: write` at workflow scope rather than to the `docker` job that needs
it. Actions are pinned to major tags (`actions/checkout@v7`,
`docker/build-push-action@v7`) rather than commit SHAs. The `deploy` job runs
`docker compose pull/down/up` in `/opt/stacks/ham-bbs` on the same host.

**Why it matters:** a malicious fork PR gets code execution on the machine that
holds the deploy stack and the registry credentials used by later jobs. Even
without secrets exposed to `pull_request` from forks, runner compromise is
enough to reach the Docker socket and the deploy directory.

**Fix:** run PR builds on `ubuntu-latest` and reserve `self-hosted` for
`release` events only; scope `packages: write` to the `docker` job; pin actions
to SHAs with a dependency updater; require approval for fork workflows
(`Environment` with required reviewers) and keep the deploy job in a separate
workflow gated by environment protection.

### M4. Unpinned `:latest` images with broad capabilities

**Files:** `compose.yaml:3,5-7,16`, `compose.dockge.yaml:30-31`

`lscr.io/linuxserver/wireguard:latest` runs with `NET_ADMIN` and `SYS_MODULE`
and has `/lib/modules` mounted read-only. `SYS_MODULE` plus a module tree lets
the container load kernel modules; combined with a floating tag, an upstream
compromise or regression lands directly in the network namespace that carries
all BBS traffic. `compose.dockge.yaml` pins the BBS to `:latest` with
`pull_policy: always`, so a failed build pushes straight to production on the
next restart.

**Fix:** pin by digest or immutable version tag, drop `SYS_MODULE` unless
something demonstrably needs it (LinuxServer's image uses it for `iptables`
modules in some paths — verify, do not assume), and promote images by digest
rather than by `latest`.

### M5. Reachable dependency advisory in `golang.org/x/text`

**File:** `bbs/go.mod:31` (indirect, `v0.21.0`)

`govulncheck` reports **GO-2026-5970** — infinite loop on invalid input in
`golang.org/x/text` — with a symbol trace through `gorm`'s initialisation
(`cmd/bbs/aprs.go:13 → bbs.init → gorm.init → norm.Form.Properties`). Fixed in
`v0.39.0`. The same scan found 23 advisories in required modules that this code
does not appear to call.

**Fix:** `go get golang.org/x/text@latest && go mod tidy`. While there, run
`go mod tidy` — `go.mod` marks directly imported modules (`gorm.io/gorm`,
`gorm.io/driver/sqlite`) as `// indirect`, which means the file has not been
tidied since the imports changed.

### M6. Log rotation can drop records and duplicate them

**Files:** `bbs/cmd/bbs/aprs_supervisor.go:113-158`, `:193-219`

`rotateLog()` copies the active file and then calls `Truncate(0)`. Anything
appended between the copy and the truncate is lost — and appends to these files
come from other processes (the receiver, every BBS session). There is no lock
and no `rename`-based handoff. In the other direction, `tailLogFile()` resets
its offset to zero when it observes a shrink, so post-rotation it re-emits
whatever is left instead of resuming cleanly.

**Fix:** rename the active file and create a new one (or use `logrotate`/`s6`/
Docker's own log driver, which already handles this). If the hand-rolled
rotator stays, hold an exclusive `flock` across the copy-and-truncate window
and have appenders take a shared lock.

### M7. Persistence errors are discarded, so success is reported when nothing was written

**Files:** `bbs/cmd/bbs/boards.go:179,213`, `bbs/cmd/bbs/bulletins.go:99,119,129`,
`bbs/cmd/bbs/sysop.go:149,203,266,284,303,326,379`,
`bbs/cmd/bbs/auth.go:16,44,60,72,123`, `bbs/cmd/bbs/aprs.go:62`

Every `saveBoards`, `saveBulletins`, and `saveUsers` call site ignores the
returned error (`_ = a.saveBoards(*data)`), and the UI paths then tell the user
the post succeeded. With H2's contention and no `_txlock=immediate`, a `SQLITE_BUSY` at
commit becomes a silently lost write that the UI reports as saved.

**Fix:** propagate the error, show a failure screen, and log the underlying
cause. This is cheap and turns silent data loss into visible, actionable
failure.

### M8. Unbounded reply depth

**Files:** `boards.go:176-179`, `boards.go:237-251`, `boards.go:91-105`

The 500-message cap applies only to top-level messages
(`len(data.Boards[boardIdx].Messages) > 500`). Replies are appended without
limit, and `flattenMessages`, `saveMessageRows`, `totalMessages`, and
`deleteMessageAtPath` all recurse over that structure. A user who replies
repeatedly produces a thread whose depth is bounded only by patience; rendering
it into a 22-line panel costs `O(depth²)` label construction, and the recursion
costs stack.

**Fix:** cap thread depth (3–5 levels is normal for a BBS) at write time, and
count replies toward the same 500-message budget.

---

## Low / notes

- **L1. Predictable APRS message IDs.** `aprsMessageIDs()` (`aprs.go:901-925`)
  seeds from `time.Now().UnixMilli()` plus the callsign characters, not from a
  CSPRNG, and `markSentAPRSAck()` accepts any ack matching
  `(user, from, to, id)`. A third party who guesses the next ID can pre-ack a
  message. Use `crypto/rand` for the 5-character ID; the collision cost is
  negligible and acks become unforgeable-by-guessing.
- **L2. Unmetered registration cost.** Creating an account runs
  `hashPassword()` at 200k PBKDF2 iterations (`storage.go:119-124`). Any caller
  can create unlimited accounts, each costing deliberate CPU. Rate-limit
  registration per source address.
- **L3. User enumeration.** Unknown callsigns go to a registration form, known
  ones to a password prompt (`auth.go:35-44`). The station directory already
  lists every active user to any logged-in caller, so this is informational
  rather than secret-leaking.
- **L4. `aprs-test.py` is a stray development script.** It contains a real
  callsign and its derived APRS-IS passcode (`:6-7`) and, if run, sends live
  packets. APRS passcodes are a public function of the callsign, so this is not
  a leaked credential — but the file belongs in a scratch directory, not the
  repository root next to production code, and it should not be runnable by
  accident.
- **L5. World-readable data files.** The SQLite database, `bbs.log`, and
  `aprs.log` are created `0644` (`logging.go:12`, `aprs_supervisor.go:39`).
  Inside a single-purpose container this is tolerable, but the database holds
  every user's email and password hash, and `aprs.log` holds full message text
  including packets from third parties. `0600`/`0640` is strictly better.
- **L6. `/etc/bbs/bbs.env` is `0644` and sourced by bash**
  (`docker-entrypoint.sh:10-18`, `bbs_session.sh:4-9`). It deliberately excludes
  `BBS_USER_PASSWORD`, and `printf '%q'` quoting is correct, so this is safe as
  written — but it is a shell-parsed file with an implicit "operator input is
  trusted" assumption. Note it, do not celebrate it.
- **L7. `readJSON` swallows malformed JSON** (`storage.go:147-164`): a corrupt
  `translations.json` silently yields an empty translation map and a UI full of
  raw keys. Log the parse error instead of falling back silently.
- **L8. `parseSysops` does not validate** (`app.go:49-56`): a typo in
  `BBS_SYSOPS` is accepted, normalised, and then simply never matches — meaning
  an operator can believe they have a sysop when they do not. Validate against
  `callsignRE` at startup and fail loudly.
- **L9. Watchdog cannot recover a wedged child.** `watchAPRSReceiver()`
  (`aprs_supervisor.go:52-77`) starts the receiver and blocks in `Wait()` with
  no timeout or kill path. The receiver's own hourly restart usually saves it,
  but a receiver stuck in a dial or a filesystem call leaves the watchdog
  waiting forever. Add a context with a deadline and `cmd.Process.Kill()`.
- **L10. Triple restart layering.** The entrypoint loops the supervisor
  (`docker-entrypoint.sh:79-87`), the supervisor loops the receiver, and the
  receiver loops its own reconnect with `restartAt`. Three independent retry
  loops with different delays make failure behaviour hard to reason about and
  hard to observe. Collapse to one supervisor with backoff.
- **L11. TUI errors become a silent logout.** `runMenu()`
  (`ui.go:672-683`) returns `"q"` when Bubble Tea fails to start, so a broken
  terminal looks identical to a clean quit. Surface the error.

---

## Architecture review

The design is coherent for its size — one binary, a forced command, SQLite, and
a terminal UI — but three structural choices create most of the risk above.

**1. The persistence layer is "read everything, write everything".**
`loadBoards()` reads all boards and all messages (recursively), and
`saveBoards()` deletes and re-inserts all of them. A single message post is
`O(all messages in all boards)` of writes and a delete-and-recreate of every
row, including rows another session may have just created. This is why H2
exists and why it cannot be fixed with a mutex (the writers are separate
processes). Replace with row-level CRUD: `INSERT` a message, `UPDATE` a
subject, `DELETE` a subtree. Keep a `version` column if you need optimistic
concurrency. Add a real migration story instead of `AutoMigrate` on every
startup.

**2. Every SSH connection pays the global startup cost, including a DDL pass.**
`main()` runs `newApp()` → `AutoMigrate` → `seedData()` →
`normalizeReceivedAPRSStore()` before the session starts (`main.go:8-25`,
`app.go:14-45,115-126`, `aprs_receiver.go:361-373`).
`normalizeReceivedAPRSStore` walks **every user** and rewrites that user's
received-APRS rows on each login, and `AutoMigrate` issues schema DDL on each
login. Under concurrent logins those DDL statements contend for the write lock,
which — combined with M7 — turns a busy moment into failed logins with no
diagnostic. Initialisation belongs in a one-shot `bbs migrate` step (or a
separate init container), not on the connection path.

**3. `app` conflates five responsibilities.** `app` owns configuration,
storage, UI hooks, APRS networking, and process supervision
(`config.go:84-99`, `aprs_supervisor.go`, `ui.go:672+`). Test hooks are
unexported fields on the same struct, so tests exercise the UI through the
production object rather than through a boundary. Splitting `store`
(database), `ui` (rendering and input), `aprs` (transport), and `supervise`
(process lifecycle) would make the concurrency work in H2 testable without
Bubble Tea, and would let the receiver and supervisor stop sharing a type that
also carries terminal styling.

**Smaller structural notes.**

- `now()` returns a formatted string (`utils.go:10-12`); timestamps are stored
  and compared as text. It works for this format, but it forecloses timezone
  handling, duration arithmetic, and any database-side date query. `time.Time`
  with an explicit UTC column is cheap to adopt now and expensive later.
- Board IDs are derived from the display name (`utils.go:103-113`), so a rename
  changes the ID, which is why `saveBoards` has to rewrite messages. Give boards
  stable generated IDs and let names be mutable.
- Mandatory email (`profileFields` required, `auth.go:128-160`) with no verification
  means the BBS collects personal data it never validates or uses. Consider
  making it optional, or state clearly why it is required.
- The APRS sender opens a fresh TCP session per message and per ack
  (`aprs.go:659,724,779`). A single long-lived sender would cut latency, remove
  M2's amplification path, and make rate limiting natural.
- The `BBS_FALLBACK_GATEWAY` route injection (`docker-entrypoint.sh:60-68`)
  hard-codes `eth0` and a Docker-default gateway. It is a reasonable development
  crutch, but it quietly changes routing in the namespace that also carries the
  WireGuard tunnel. Make it opt-in (`BBS_ALLOW_FALLBACK_ROUTE=true`) so
  production cannot take that path by accident.

---

## Checked and clean

These were traced and found sound; listing them so the scope of the "clean"
claim is honest.

- **Password storage.** PBKDF2-SHA256, 200,000 iterations, 16-byte random salt
  per hash, algorithm and iteration count stored with the hash, constant-time
  comparison via `subtle.ConstantTimeCompare` (`storage.go:119-143`). Format is
  upgradeable even though no rehash path exists yet.
- **SQL injection.** All queries go through GORM's placeholder binding
  (`Where("... = ?", x)`); the only raw `Exec` calls are constant strings
  (`DELETE FROM db_messages`, `boards.go:37,40`). No user input reaches a
  query string.
- **Command execution.** The only `exec.Command` is
  `exec.Command(os.Executable(), "aprs-receiver")` — no shell, no user input
  (`aprs_supervisor.go:58`). No template engine, no `eval`, no dynamic
  imports.
- **Secrets in the repository and its history.** `hamnet/wg_confs/` and `.env`
  are ignored; `git log --all -- .env` is empty; `wg0.conf.example` carries a
  placeholder private key and a public key, which is correct. The real host
  address and peer public key in `README.md` are published deliberately.
- **SSH server hardening.** `PermitRootLogin no`, `AllowUsers bbs`,
  `AllowTcpForwarding no`, `PermitTunnel no`, `X11Forwarding no`,
  `GatewayPorts no`, `PermitUserEnvironment no`, `ForceCommand` for the only
  user, `ClientAliveInterval/CountMax` set. The `ForceCommand` overrides
  client-supplied commands and `authorized_keys` command options.
- **Host keys.** Generated once into a mounted directory, `0600`/`0644`, so
  they survive container restarts (`docker-entrypoint.sh:36-58`).
- **Inbound APRS text handling.** `singleLineAPRSDetail()` strips C0/C1 controls
  and normalises whitespace before storage and display, and every APRS render
  site uses it (`aprs.go:366-383`). This is the correct pattern — it is the
  rest of the UI that lacks it (H3).
- **Ack matching scope.** `markSentAPRSAck()` filters on
  `(user_callsign, from, to, message_id)`, so a stray ack cannot mark another
  user's message acknowledged (`aprs.go`).
- **Intra-process database concurrency.** `SetMaxOpenConns(1)` plus WAL plus
  `busy_timeout=5000` (`database.go:90,98`) makes a single process safe; the
  remaining races are inter-process (H2).
- **Concurrent stdout writes.** `supervisorOutputMu` serialises the four log
  tailers and rotation messages (`aprs_supervisor.go:18,193-219`).
- **Input validation.** Callsigns, Maidenhead locators, emails, APRS SSIDs and
  board IDs are all validated by anchored regular expressions with length caps
  (`config.go:32-37`, `ui.go:405-444`). Field limits (80/100/120/4000) bound
  every stored string.
- **Build and test gates.** `go vet ./...` clean; `go test ./cmd/bbs` passes;
  CI enforces a 60% coverage floor (`.github/workflows/docker-image.yml:61-68`).

---

## Suggested order of work

1. **H2** — row-level persistence. Everything else that touches data is
   fragile until this lands.
2. **H1** — stop auto-granting sysop from `BBS_SYSOPS`, and decide how a
   callsign is proven.
3. **H3** — sanitise user text at the storage boundary (one function, applied
   everywhere, mirroring `singleLineAPRSDetail`).
4. **H4** — refuse to start on the placeholder password; scope
   `PermitEmptyPasswords`.
5. **M5, M7** — dependency update and error propagation: small, mechanical,
   immediately useful.
6. **M1, M2** — rate limits at the two places untrusted input arrives (BBS
   login, APRS inbound).
7. **M3, M4** — CI and image pinning.
8. **M6, M8, L1–L11** — hygiene.

## Limits

This review read the code and ran the project's own checks plus three targeted
probes. It did not run the container, the SSH server, fail2ban, or a live
APRS-IS session, so anything that depends on runtime configuration (iptables
behaviour in the shared namespace, guest mode, actual Bubble Tea input
filtering for the textarea) is reasoned from configuration and source rather
than observed. The `textarea` path in H3 in particular deserves a live check:
if the component filters `ESC` on input, the body-based injection narrows to
content imported or edited by other means, though the subject and profile-field
paths do not go through that component at all.
