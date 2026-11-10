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
- Fixed H3 and then H2 in this tree, each with regression tests. The probes that
  proved the bugs were converted into permanent tests where they belonged (see
  `sanitize_test.go` and `concurrency_test.go`); the H2 tests were also checked
  to fail when the old write pattern is temporarily restored.

## Summary

- **Trust model**: still unverified. A callsign is not a credential and cannot be validated, so anyone can claim an unregistered one; the permanent sysop grant from `BBS_SYSOPS` is fixed (H1).
- **Multi-user concurrency**: fixed. Every mutation writes only the rows it names, inside an immediate transaction, so a concurrent session cannot overwrite another's data.
- **Terminal injection**: fixed. User text is now sanitised on input, on write, and on read, so stored escape sequences can no longer reach another user's terminal.
- **Defaults**: one publicly known SSH password away from a session on port 2222.
- **Dependency hygiene**: one reachable advisory; dependencies otherwise current.
- **Architecture**: workable at small scale. The persistence model was the source of the worst findings and is now row-level; the per-connection process model still forces every session to pay global startup cost.

The two structural issues — H2 (whole-table rewrite from stale snapshots) and
the per-connection process model — accounted for most of the exploitable impact.
H1, H2 and H3 are fixed or mitigated in this tree; their sections record the
mechanism, the evidence and what was deliberately left open. Callsign ownership
verification is the one finding that cannot be closed by code, and it is
documented as follow-up work.

---

## High

### H1. Anyone can claim any callsign, and the first claimant of a `BBS_SYSOPS` callsign becomes a sysop — PARTIALLY FIXED

**Files (as found):** `bbs/cmd/bbs/auth.go:35-44`, `bbs/cmd/bbs/storage.go:89-100`,
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

**Fix (implemented 2026-11-10): the persistent grant is gone; a real ownership
step is still missing.**

`BBS_SYSOPS` is now a bootstrap mechanism rather than a standing grant:

- `isSysop(profile)` (`storage.go`) answers from the stored row alone. The
  configured map is no longer consulted on the request path, so a callsign
  listed in `BBS_SYSOPS` has no authority unless an account actually holds
  `IsSysop` in the database.
- `applyConfiguredSysops(users)` promotes configured accounts only while
  no enabled account holds the role. It checks the caller's snapshot and then
  confirms against the database, so a stale snapshot from before another
  session bootstrapped cannot grant the role again; an unreadable count counts
  as "a sysop exists".
- `isBootstrapSysop(callsign)` gates the registration path with a database
  count of active sysops and **fails closed**: if the count cannot be read, the
  role is not granted.
- The `cannot_demote_configured` guard in `toggleSysop` is removed (with its
  translations), because a bootstrapped sysop is now an ordinary sysop. The
  last-active-sysop guard still prevents locking the BBS out.

Impact: the "register a configured callsign months later and receive the sysop
menu" path is closed. An attacker registering `EA1ABC` on a BBS that already has
a sysop now gets a plain account, and `README.md` and `.env.example` document
the bootstrap-window rule and the demotion behaviour.

**Verification.** `bbs/cmd/bbs/sysop_bootstrap_test.go` covers the bootstrap
grant, the ignore-once-a-sysop-exists rule, disabled-sysop handling,
`hasActiveSysop`, registration through `authenticate()` in both directions, a
bootstrapped sysop being demotable, the last-sysop guard, a configured callsign
with no account, fail-closed behaviour when the database is closed, and a stale
snapshot that must not re-bootstrap over a stored sysop. The
registration test was checked for sensitivity: restoring the old
"always grant from the configured list" behaviour makes it fail with
`registration handed sysop to a configured callsign while a sysop existed`.

**What is NOT fixed, and cannot be fixed here: there is no reliable way to
assess the validity of a callsign.**

A callsign is not a credential. Amateur callsigns are published in public
databases, they cannot be proven by typing them, and there is no service this
BBS can call that would tell it "this caller is the licensee of `EA1ABC`". Any
check available at login time — format, an allow-list, a lookup against a
public database — only confirms that the string exists, not who is typing it.
Sending a verification token would need a channel to the licensee that this
deployment does not have (no verified email, no postal address, no signed
packet path). The practical consequence is unchanged:

1. **Any callsign without an account can still be claimed by the first caller
   who types it.** That includes, during the bootstrap window, the callsign
   listed in `BBS_SYSOPS` — mitigated by registering the sysop immediately after
   first start, not eliminated.
2. **Transmission as another station.** `sendAPRSMessage` derives the APRS-IS
   passcode from the logged-in callsign (`aprsPasscode(from)`, `aprs.go:611-624`)
   and transmits under it, and `sendLoginAPRSBeacon` beacons that station's
   position at every login. APRS passcodes are a public function of the
   callsign, so this is not a bug in the derivation — it is the protocol, and it
   is why APRS-IS treats the passcode as anti-noise, not authentication.
3. **Account takeover of a returning user** remains impossible while the
   account has a password, which is the only real credential in the system.

Until ownership can be verified, the honest framing is that a BBS account is an
**unverified pseudonym**, and the deployment must be sized accordingly
(rate limits, guest mode off, APRS disabled for unverified users).

**Left as follow-up work.** None of these are implemented, and each is a design
decision rather than a patch:

- A registration approval queue: new accounts are created disabled and a sysop
  enables them, using whatever out-of-band contact the operator already has.
- Out-of-band binding tokens (a signed packet over the air, a postcard code, a
  club-membership list) that unlock a callsign after a one-time code is entered.
- Limiting new self-registrations per source address and per hour, so claiming
  many callsigns is expensive.
- Making `Enable APRS` opt-in by a sysop rather than self-service, since APRS is
  the only place where an unverified callsign causes damage outside this
  service.

### H2. Every write rewrites the whole table from a stale snapshot; concurrent sessions destroy each other's data — FIXED

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

**Why it mattered:** silent data loss across users, including account deletion
and lost sysop flags. `sqlDB.SetMaxOpenConns(1)` (`database.go:98`) only
serialises within one process, so it does not help here. WAL plus
`busy_timeout` prevents corruption but not lost updates. Every caller in
`boards.go`, `bulletins.go`, `sysop.go`, and `aprs.go` discarded the returned
error (`_ = a.saveBoards(...)`), so a `SQLITE_BUSY` at commit was also silent.

**Fix (implemented 2026-11-10).** Persistence is now row-level. `bbs/cmd/bbs/mutations.go`
is the only place that writes:

- **One row per operation.** `createMessage` / `createMessageRow` insert a
  single message or reply at `MAX(position)+1` among its siblings;
  `updateMessageRow` updates one message's subject, body, and edited stamp;
  `deleteMessageSubtree` deletes one thread's rows. `createBoard`,
  `renameBoardRow`, `deleteBoardRow`, `createBulletin`, `updateBulletinRow`,
  `deleteBulletinRow`, `saveUser`, and `deleteUser` follow the same shape. No
  statement deletes rows it was not asked to delete, so two sessions posting to
  the same board touch disjoint rows and cannot erase each other.
- **Compound edits are one transaction.** A board rename changes the board row
  and moves every message to the new `board_id` in a single transaction, so a
  failure cannot orphan a board's messages. Deleting a message removes its
  replies in the same transaction. `deleteUser` removes the account and its
  APRS history together.
- **`_txlock=immediate` on the DSN** (`database.go`). Every transaction now
  takes the write lock at `BEGIN` instead of upgrading at its first write.
  Without it, two processes can both begin a deferred read transaction, then
  both try to upgrade — one gets `SQLITE_BUSY` and, under the old code, that
  error was discarded. With it, writers queue on `_busy_timeout=5000` and
  proceed one at a time. `SetMaxOpenConns(1)` still bounds each process; the
  immediate lock is what bounds the processes.
- **Seeding is transactional.** `seedDefaultData` runs its count check and its
  inserts inside one immediate transaction, so two simultaneous first logins no
  longer both insert the default bulletins and board.
- **The bulk-replace primitive is gone from the binary.** `replaceAllUsers`,
  `replaceAllBoards`, and `replaceAllBulletins` now live in
  `bbs/cmd/bbs/fixtures_test.go`, so they are compiled only into the test
  binary. This is the mechanism half of the fix: a future caller cannot
  reintroduce the pattern on a request path even by accident.
- **Missed rows no longer materialise as orphans.** `createMessageRow`
  materialises the board row when the UI is showing the virtual `General`
  board, so a post cannot be written to a `board_id` that no board row names.
- **Errors reach the user.** Every rewritten path propagates the persistence
  error to `showMutationError`, which logs it and displays it, instead of
  reporting success for a write that did not happen (see M7).

**Verification.** New `bbs/cmd/bbs/concurrency_test.go`: a two-handle
`TestConcurrentPostsDoNotLoseEachOthersMessages` that reads both snapshots
before either write; `TestConcurrentWritesFromTwoSessionsAllLand` (20 posts split across two
handles, all 20 must land); `TestStaleSessionCannotDeleteAnotherSessionsAccount`
(the exact user-table probe from this review, now asserting survival);
`TestConcurrentProfileSavesDoNotOverwriteEachOther`; `TestLoginDoesNotRewriteOtherAccounts`;
plus rename/delete/subtree-trim correctness tests and
`TestDatabaseOpensWithImmediateTransactions`.

These tests were checked for sensitivity, not just for passing: temporarily
restoring the old `DELETE FROM db_messages` behaviour inside `createMessageRow`
makes the first two fail exactly as this review described (`a concurrent post
was lost: [from-bob]`, `total messages = 1, want 20`); restoring the fix makes
them pass. `gofmt -l` clean, `go vet ./...` clean, `go test ./cmd/bbs` passes
with 70.5% statement coverage (CI floor 60%), and `go test -race ./cmd/bbs`
passes.

**Residual risk.** SQLite still serialises writers, so a busy BBS serialises
writes and waits up to the 5 s busy timeout before failing. Message positions
are computed as `MAX(position)+1` inside the immediate transaction, which is
safe against concurrent inserts but leaves gaps after deletes; ordering only
ever relies on relative position, so gaps are harmless. `AutoMigrate` still
runs on every connection (see the architecture section).

### H3. Stored terminal-escape injection into other users' terminals — FIXED

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

**Fix (implemented 2026-11-10).** The rule now lives in one place and is
enforced at three boundaries plus startup:

- **One rule: `bbs/cmd/bbs/sanitize.go`.** `sanitizeTerminalText(text, multiline)`
  normalises CRLF/CR to LF, replaces invalid UTF-8 with U+FFFD, drops ESC, C0/C1
  controls and DEL (via `unicode.IsControl`), turns tabs into spaces, and caps
  blank-line runs at two. It also drops the printable-but-invisible characters
  that can spoof a terminal or its surrounding renderer: bidi overrides and
  isolates (U+202A–U+202E, U+2066–U+2069), directional marks (U+200E/U+200F,
  U+061C), zero-width space (U+200B), word joiner (U+2060), soft hyphen
  (U+00AD), BOM (U+FEFF), and the Unicode line/paragraph separators
  (U+2028/U+2029). ZWJ and ZWNJ are deliberately kept because they are required
  for correct shaping in scripts this BBS serves. `sanitizeSingleLineText`
  collapses whitespace for one-line fields; `sanitizeMultilineText` keeps
  paragraph breaks; `sanitizeFieldText(kind, text)` picks the rule for a form
  field kind and leaves passwords and choice values byte-exact. The function is
  idempotent, so overlapping boundaries cannot corrupt content.
- **On input.** `formModel.validate()` (`ui.go`) passes every interactive field
  through `sanitizeFieldText` after its normaliser and before the required and
  validator checks. This is the mechanism guarantee: a future screen cannot
  skip it, and a field containing only escapes fails its `required` check
  instead of being stored blank.
- **On write.** Each persistence path cleans its own values even when a form is
  bypassed: `postMessageToBoard` and `replyToMessage` (`boards.go`),
  `editBoardMessage`, `addBoard`, `renameBoard` (`sysop.go`), `addBulletin`,
  `editBulletinAt` (`bulletins.go`), and `applyProfileValues` (`auth.go`, which
  also serves the sysop user editor). Required fields that sanitise to empty are
  rejected with a visible "required" screen, and the confirmation screens and
  log lines now use the cleaned values rather than the raw form input.
- **On read.** `loadBoardMessages`/`loadBoards`, `loadBulletins`, and
  `loadUsers` sanitise as they build in-memory rows. This is what repairs data
  written before this fix — and data written by any future path that forgets
  the write guards — without a migration: bad text simply cannot reach a
  renderer. It is also why render-time sanitising was not the right layer:
  `showInfo`/`showInfoActions` receive strings that already carry the
  application's own Lip Gloss styling, so cleaning them there would strip the
  app's formatting along with the attack.
- **At startup.** `newApp` cleans `BBS_NAME`, `BBS_SYSOP`, `BBS_LOCATION`, and
  `BBS_WELCOME_TOPIC`, which reach the banner and header through the same
  renderers as user text.

`singleLineAPRSDetail` now delegates to `sanitizeSingleLineText`, so the APRS
screens that already behaved correctly and the rest of the UI finally share one
implementation instead of two.

**Verification.** New `bbs/cmd/bbs/sanitize_test.go` covers: a 19-case unit
table (CSI clear, OSC 52 clipboard, bare ESC, C1 CSI, C1 control, DEL, bidi
override, isolate, zero-width space, BOM, tab, newline in a one-line field,
CRLF, lone CR, blank-line run, invalid UTF-8, accented Latin, CJK); idempotence;
the form boundary (a payload mixing C0 controls with a bidi override and a
zero-width space, because the Bubbles widgets strip the former but keep the
latter — verified against the vendored `runeutil` sanitizer; plus a check that
the password field is left byte-exact); a post → store → load →
render end-to-end check that the old `messageMenuLabel` and `wrapText` paths no
longer carry escapes; replies and bulletins; board names and profile fields;
a legacy-row test that inserts hostile text straight into the database and
asserts every loader cleans it; and a rejection test for escape-only input.
`gofmt -l` clean, `go vet ./...` clean, `go test ./cmd/bbs` passes with 71.5%
statement coverage (CI floor is 60%). One existing assertion in
`content_test.go` was relaxed from "description is exactly 120 bytes" to "at
most 120, at least 100" because reading now trims the trailing space that the
byte clip can leave behind — the clipping behaviour itself is unchanged.

**Residual risk.** `translations.json` is operator-controlled and is not
sanitised; it is a trusted file, but it is still shell/screen-visible text. The
sanitiser removes control and format characters, not visual spoofing: homoglyphs
and legitimate right-to-left text can still reorder glyphs inside a fixed-width
panel, and no fixed-width TUI can fully prevent that. Cleaning happens on read,
so a repaired row is only rewritten in the database the next time that record is
saved; until then the stored bytes stay hostile but unreachable.

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

### M3. CI ran fork pull requests on a self-hosted runner with workflow-wide `packages: write` — FIXED

**Files (as found):** `.github/workflows/docker-image.yml`

`pull_request` triggered the `test` job on `runs-on: self-hosted`. Any pull
request from a fork executed arbitrary Go code on that runner, which is a
persistent host, not an ephemeral VM. `permissions` granted `packages: write`
at workflow scope rather than to the `docker` job that needs it. Actions were
pinned to major tags (`actions/checkout@v7`, `docker/build-push-action@v7`)
rather than commit SHAs. The `deploy` job ran `docker compose pull/down/up` in
`/opt/stacks/ham-bbs` on the same host.

**Why it mattered:** a malicious fork PR got code execution on the machine that
held the deploy stack and the registry credentials used by later jobs.

**Fix (implemented 2026-11-10).** Every job now runs on `ubuntu-latest`, so no
pull request reaches a persistent host. `permissions: contents: read` is set at
workflow level and `packages: write` is granted to the `docker` job alone. Each
action is pinned to a commit SHA with the version in a trailing comment. The
`deploy` job is gone — it could never have worked on this host, which has no
Docker daemon — and is replaced by a `pin` job that updates the digest in the
GitOps repository Fleet reconciles (see the Deployment section of `README.md`).
That job only runs when an `INFRA_REPO_TOKEN` secret is configured and otherwise
records the digest for a manual pin, so a fork PR still cannot reach the
deployment repository.

Also fixed in the same workflow: stale branch runs are cancelled
(`cancel-in-progress` for non-release events) so the queue cannot fill with
superseded work, `timeout-minutes` bounds every job, and the duplicate
`tags: "*"` push trigger is removed.

**Residual:** branch protection does not currently require the `pin` job, and
`actions/dependency-review-action` or a scheduled SHA updater is still the right
way to keep the pinned SHAs current. Neither was added here.

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

### M7. Persistence errors are discarded, so success is reported when nothing was written — FIXED IN THE REWRITTEN PATHS

**Files (as found):** `boards.go`, `bulletins.go`, `sysop.go`, `auth.go`,
`aprs.go`

Every `saveBoards`, `saveBulletins`, and `saveUsers` call site ignored the
returned error (`_ = a.saveBoards(*data)`), and the UI paths then told the user
the post succeeded. With H2's contention and no `_txlock=immediate`, a
`SQLITE_BUSY` at commit became a silently lost write that the UI reported as
saved.

**Fix (implemented 2026-11-10).** Every persistence call site that H2 touched
now handles its error: board post and reply, board create/rename/delete,
message edit/delete, bulletin create/edit/delete, user create/update/delete,
sysop toggle, profile change, APRS enable, and the two APRS history deletes.
`showMutationError` (`ui.go`) logs the failure (`action=persistence_error`) and
displays it instead of a success screen. No `_ = a.save…` or `_ = a.delete…`
remains outside tests.

Residual: `showBulletins`, `stationDirectory`, and similar read paths still
ignore their load errors (`bulletins, _ := a.loadBulletins()`), so a failed read
still renders as an empty list rather than an error. That is a read-path
decision, not a lost write.

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

**1. The persistence layer was "read everything, write everything".**
`loadBoards()` read all boards and all messages (recursively), and the caller
mutated that copy; `saveBoards()` then deleted and re-inserted every row. A
single message post was `O(all messages in all boards)` of writes, including
rows another session had just created. That is H2, and it could not be fixed
with a mutex because the writers are separate processes. It is now row-level
CRUD with immediate transactions (see H2). What remains of the design budget:
`loadBoards()` still reads every message of every board into memory for the
board picker, which is `O(total messages)` per screen. That is a scalability
limit, not a correctness bug; a paginated `COUNT` query per board would fix it.
`AutoMigrate` still runs on every startup for every session — a real migration
step is still worth adding.

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
- **SQL injection.** Every query goes through GORM's placeholder binding
  (`Where("... = ?", x)`, `Delete(&row, "callsign = ?", key)`). The raw `Exec`
  calls that used to exist (`DELETE FROM db_messages`, `DELETE FROM db_boards`)
  are gone with the H2 fix, so there is no raw SQL left at all. No user input
  reaches a query string.
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
  site uses it (`aprs.go:366-383`). It now delegates to the shared rule in
  `sanitize.go`, so APRS and the rest of the UI have one implementation (H3).
- **Ack matching scope.** `markSentAPRSAck()` filters on
  `(user_callsign, from, to, message_id)`, so a stray ack cannot mark another
  user's message acknowledged (`aprs.go`).
- **Intra-process database concurrency.** `SetMaxOpenConns(1)` plus WAL plus
  `busy_timeout=5000` bounds each process; `_txlock=immediate` (added with the
  H2 fix) serialises read-then-write transactions across processes.
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

1. ~~**H2** — row-level persistence~~ — **done** (see H2: single-row writes,
   immediate transactions, row-level tests, and a sensitivity check).
2. ~~**H1** — stop auto-granting sysop from `BBS_SYSOPS`~~ — **done**; callsign
   ownership verification is **left open** (see H1, "Left as follow-up work").
3. ~~**H3** — sanitise user text at the storage boundary~~ — **done** (see H3:
   one rule, enforced on input, write, read, and startup, with regression tests).
4. **H4** — refuse to start on the placeholder password; scope
   `PermitEmptyPasswords`.
5. **M5** — dependency update; ~~M7~~ — **done** in the paths H2 rewrote (see
   M7; read paths still ignore load errors).
6. **M1, M2** — rate limits at the two places untrusted input arrives (BBS
   login, APRS inbound).
7. ~~**M3** — CI runner and permission hardening~~ — **done** (see M3);
   **M4** — image pinning is still open.
8. **M6, M8, L1–L11** — hygiene.

## Limits

This review read the code and ran the project's own checks plus three targeted
probes. It did not run the container, the SSH server, fail2ban, or a live
APRS-IS session, so anything that depends on runtime configuration (iptables
behaviour in the shared namespace, guest mode) is reasoned from configuration
and source rather than observed. The `textarea` question that H3 originally
left open — whether the component filters `ESC` on input — no longer changes the
outcome: text is sanitised on write and again on read, so content arrives clean
whether or not the widget filters anything. Bubble Tea's `textinput.SetValue`
was checked in the vendored source and does strip control characters, which is
why the password field needed no special handling.

The H2 work was verified with unit-level concurrency tests against two database
handles on one file, which is the same isolation the separate SSH processes
have, but it was not exercised with two live SSH sessions against a running
container. SQLite's locking behaviour under real multi-process load (including
the 5 s busy timeout under sustained contention) is therefore reasoned from the
`_txlock=immediate` and `busy_timeout` settings rather than observed.
