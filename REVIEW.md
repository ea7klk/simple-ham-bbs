# Security, Concurrency, and Architecture Review

Date: 2026-11-10
Scope: the whole repository at `main` (`a5b114d`) — the Go BBS
(`bbs/cmd/bbs/`, ~4.5k lines plus ~2.8k of tests), container/SSH/WireGuard
infrastructure, and the CI workflow.
Reviewer: automated review, not a formal audit. This document finds defects; it
does not prove their absence.

## What was done

- Read every Go source file, `Dockerfile`, `docker-entrypoint.sh`,
  `bbs_session.sh`, `sshd_config`, both compose files, the fail2ban jails, the
  workflow, and the then-present `aprs-test.py` (removed under L4).
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
- **Defaults**: guest mode is on by design and deletes the transport password, so the published placeholder is inert; accepted (H4).
- **Dependency hygiene**: clean. `govulncheck` reports no reachable advisory after the x/text update (M5).
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

1. **Privilege escalation.** `BBS_SYSOPS=EA1ABC` granted sysop to whoever
   registered `EA1ABC`, and kept granting it on every later login. If that was
   not the operator — and nothing prevented it — the attacker got the sysop
   menu: user deletion, board deletion, message editing, bulletin publishing.
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

### H4. Publicly known default SSH password, published on all interfaces — ACCEPTED, SAFE AND AS INTENDED

**Status (annotated 2026-11-10):** accepted risk, by design. The password
placeholder has no effect on the running deployment, and the open access it
implies is the documented purpose of guest mode. Details below; the original
finding is kept for the record because the mechanism still exists for a
deployment that does *not* enable guest mode.

**Files:** `.env.example:5`, `bbs/docker-entrypoint.sh:4`,
`compose.yaml:40`, `compose.dockge.yaml:47`, `bbs/sshd_config:10-11`

**Reachable path (as written):** `docker compose up -d` without editing `.env`
(or after copying it and leaving the placeholder) sets the transport user's
password to `change-this-password`. That path is the local compose run; the
deployed instance is the Fleet Deployment described in M4. The WireGuard
container publishes `2222:2222/tcp` on `0.0.0.0` (`compose.yaml:21`), and
`sshd_config` allows password auth with `AllowUsers bbs`. `PermitEmptyPasswords
yes` is also unconditionally on, which is only needed when `BBS_GUEST_MODE=true`
deletes the password (`docker-entrypoint.sh:20-26`).

**Why this is safe as intended.** The deployment runs in guest mode, and in
guest mode the transport password is deleted rather than set — so the
placeholder value is dead configuration, not a credential:

- `configure_login_password` (`docker-entrypoint.sh:20-26`) branches on
  `BBS_GUEST_MODE`. In guest mode it runs `passwd -d bbs` and never touches
  `BBS_USER_PASSWORD`; the placeholder is therefore never applied to any
  account.
- Verified on the running deployment: `BBS_GUEST_MODE=true`, and `/etc/shadow`
  has an empty password field for `bbs`. `BBS_USER_PASSWORD` is present in the
  `bbs-env` secret with the placeholder value and has no effect, precisely
  because guest mode deletes the password first.
- `PermitEmptyPasswords yes` is what makes that work; it is not an oversight.
  It is required for the passwordless `bbs` account that guest mode creates, and
  is only reachable because `AllowUsers bbs` limits the server to that one
  account. `ForceCommand` then confines the session to the BBS application, so
  even an unauthenticated session never reaches a shell.
- An open BBS is the product: `README.md` documents
  `BBS_GUEST_MODE=true` as the way to let callers connect without a password.
  That is the intended deployment shape, not a misconfiguration.

**What the trade-off buys and costs.** Guest mode means the SSH port is an
unauthenticated entry point to the BBS application. That is deliberate and
bounded: `ForceCommand` (no shell, no forwarding), `MaxStartups` (10:30:100),
and fail2ban on the sshd log. The residual exposure is not SSH but identity —
an unauthenticated caller can create an account under any unregistered callsign,
which is H1. H1's privilege-escalation path is closed: `BBS_SYSOPS` no longer
confers a standing role, so a guest-created account cannot reach the sysop menu
without an existing sysop granting it.

**Residual configuration hazard (not an active finding).** The mechanism the
original finding described is still real for a deployment that turns guest mode
off and leaves the placeholder in place: it would then set a reachable account's
password to a value published in this repository. The defaults remain as they
are by choice — an operator who sets `BBS_GUEST_MODE=false` is expected to set
`BBS_USER_PASSWORD` at the same time, and `README.md` says so in Local development
step 2. If that assumption is ever judged too weak, the cheap hardening is to
refuse to start when guest mode is off and the password is unset or equals the
placeholder, and to scope `PermitEmptyPasswords yes` to a guest-mode `Match`
block.

---

## Medium

### M1. No rate limiting on BBS authentication; fail2ban only watched sshd — FIXED (fail2ban removed)

**Files (as found):** `bbs/cmd/bbs/auth.go`, `bbs/fail2ban/jail.local`

`authenticate()` counted failures per callsign in memory, for the lifetime of one
session. A new SSH connection reset the counter, and nothing linked attempts
across connections. fail2ban read `/var/log/auth.log`, which receives only sshd
events — the BBS's own "wrong password" events go to `bbs.log` and were never
banned. With guest mode on there is no SSH password to guess at all, so
fail2ban had nothing to see. Each attempt costs a 200k-iteration PBKDF2, so a
single connection could also burn CPU cheaply.

**Fix (implemented 2026-11-10). fail2ban is gone, and the BBS throttles its own
logins.**

- Removed from the image and entrypoint: the Alpine package, `bbs/fail2ban/`
  (both files), `/run/fail2ban`, `/var/log/fail2ban.log`, the
  `fail2ban-server` background process, the `BBS_FAIL2BAN_LOG_FILE` config
  field and its tailer. `iptables` was only there for fail2ban's banaction and
  goes with it; `iproute2` stays for the fallback-route helper.
- `dbAuthFailure` (`ratelimit.go`) stores failures keyed by the client address
  from `SSH_CONNECTION`, in SQLite, so the counter is shared by every `bbs_app`
  process instead of dying with the session.
- Five failures within fifteen minutes lock the address for fifteen minutes. A
  successful login clears it. Idle rows are pruned on the same nightly timer as
  log rotation, so the table cannot grow unbounded.
- `authenticate()` checks the lock before rendering the login form, so a locked
  caller cannot keep prompting, and writes `login_lockout` to `bbs.log`.
  Counter failures are logged and do not fail open — the per-session
  three-attempt limit still applies.
- When `SSH_CONNECTION` is absent (a local test, or a session started outside
  sshd) all such callers share one bucket rather than escaping the limit.

**Trade-off, stated plainly.** Removing fail2ban removes sshd-level brute-force
protection. That is safe for this deployment because guest mode is on and there
is no SSH password to guess; the application-level password prompt is what the
new throttle protects. An operator who runs with `BBS_GUEST_MODE=false` should
keep the port inside WireGuard and prefer `authorized_keys` over the transport
password, since nothing now bans a repeat SSH offender.

**Still open:** `argon2id` instead of PBKDF2-SHA256, and per-source limits on
account *registration* (L2), which the same throttle table could serve.

### M2. Inbound APRS traffic can force outbound connections without limit — FIXED

**Files (as found):** `bbs/cmd/bbs/aprs_receiver.go`, `bbs/cmd/bbs/aprs.go`

For every received APRS message that carries a message ID and is addressed to a
user with `EnableAPRS=true`, the receiver called `sendAPRSAck()`, which dialled a
**new** TCP connection to APRS-IS, logged in, wrote one packet, and closed. There
was no rate limit, no backoff, and no shared connection. APRS-IS is an open
network and the receiver subscribes with `filter t/m`, so any station could
generate that traffic. One inbound packet became one outbound dial with a 10 s
connect timeout and 30 s deadline, and because the ack was sent synchronously
before the packet was stored, a burst also stalled the receive loop.

**Fix (implemented 2026-11-10).** Acknowledgements go through `aprsAckQueue`
(`aprs_ack.go`):

- A bounded queue of 64 pending acks; overflow is dropped, counted and logged
  rather than blocking the scan loop.
- One worker drains it, spacing sends at least 10 s apart, which caps outbound
  dials at six per minute no matter the inbound rate.
- Repeat acks for the same `(from, to, message ID)` are suppressed for ten
  minutes.
- The receive loop only enqueues, so packet handling is no longer serialised
  behind network I/O.
- Sent and dropped counters are logged when the receiver disconnects, so an
  operator can see when inbound traffic outran the outbound budget.

**Still open:** a single long-lived APRS-IS session for sending would cut latency
and remove the per-ack login entirely; the bounded queue makes that an
optimisation rather than a requirement.

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

### M4. Broad capabilities and floating tags in the compose files — ACCEPTED

**Files:** `compose.yaml:11,13-15,24,35-36`, `compose.dockge.yaml:11,13-15,29-30,38-39,42-43`

**Status (annotated 2026-11-10):** accepted in full. The capabilities are
necessary for WireGuard to function and were verified against the running
workload, not assumed; the floating tags live only in compose files that the
deployment does not use.

**What was found.** `lscr.io/linuxserver/wireguard:latest` runs with `NET_ADMIN`,
`SYS_MODULE`, and `/lib/modules` mounted read-only, and the BBS container shares
its network namespace. A floating tag plus module-loading capability means an
upstream compromise or regression would land directly in the namespace that
carries all BBS traffic.

**Why the capabilities are required.** The cluster runs a real WireGuard
endpoint (`wg-easy`, namespace `wireguard`), so this is not a theoretical grant:

- `NET_ADMIN` is what creates and configures the tunnel. Verified on the live
  workload: a `wg0` interface with an active peer and a completed handshake, in
  the **host** network namespace (`hostNetwork: true`). Creating the interface,
  assigning addresses, adding routes and installing NAT rules all require it.
  There is no way to run a kernel WireGuard endpoint without `NET_ADMIN`; a
  userspace implementation would still need `/dev/net/tun` and equivalent
  privilege. Accepting it is the price of the product.
- `SYS_MODULE` exists because WireGuard on Linux is a **loadable module**, not
  built in: the node's kernel config says `CONFIG_WIREGUARD=m`, and the module is
  present in `lsmod` with a populated `/sys/module/wireguard`. Loading it needs
  `SYS_MODULE` *and* a readable module tree, which is exactly why the LinuxServer
  image ships the `/lib/modules` bind mount. On a host that has not preloaded the
  module, dropping `SYS_MODULE` would leave the container unable to bring the
  tunnel up.

**Honest scope of the grant.** With `hostNetwork: true`, `NET_ADMIN` is not
namespace-local advice — it is host-level network control: interfaces, routes,
and firewall rules on the node itself. That is inherent to a VPN endpoint and is
accepted here, with two mitigations that do not depend on the capabilities:
the live WireGuard image is **digest-pinned**
(`ghcr.io/wg-easy/wg-easy@sha256:66352ccb…`), so a moving tag cannot swap the
binary under those privileges, and the container carries no other capabilities.

`SYS_MODULE` is also **inert in the Kubernetes path**: the live `wg-easy` pod
mounts only a config volume and `/dev/shm`, not `/lib/modules`, so it cannot load
modules from inside the container even with the capability. It survives only
because the node's kernel already has `wireguard` loaded. If the deployment ever
guarantees that preload (for example via a node-level `modules-load.d` entry),
`SYS_MODULE` can be dropped there without changing behaviour. The compose files
are a different case — they mount `/lib/modules`, so there the capability is
functional and should stay.

**Floating tags in the compose files: accepted (2026-11-10).** `compose.yaml`
and `compose.dockge.yaml` reference `lscr.io/linuxserver/wireguard:latest` and
`ghcr.io/ea7klk/simple-ham-bbs:latest` (the latter with `pull_policy: always`).
That is accepted because **these files are not part of the deployed system**:
production is a Fleet-managed k3s Deployment whose image is digest-pinned in
`spainip/infra`, and the cluster's WireGuard is digest-pinned as well
(`ghcr.io/wg-easy/wg-easy@sha256:66352ccb…`, verified on the live pod). A
floating tag in a compose file that nothing deploys cannot swap the binary
running under those privileges.

`README.md` now states this explicitly: the compose path is filed under
[Local development](README.md#local-development) rather than presented as
a way to run the service, and says in as many words that it is a workstation
convenience whose tags are not kept in step with the cluster.

**Consequence for anyone who does use them.** A local run tracks whatever the
registry serves. That is the intended trade for a development convenience; if
these files are ever promoted to a real deployment path, they must be pinned by
digest first, and that decision belongs in the same change.

### M5. Reachable dependency advisory in `golang.org/x/text` — FIXED

**File (as found):** `bbs/go.mod` (indirect, `v0.21.0`)

`govulncheck` reported **GO-2026-5970** — infinite loop on invalid input in
`golang.org/x/text` — with a symbol trace through `gorm`'s initialisation
(`bbs.init → gorm.init → norm.Form.Properties`). The same scan found 23
advisories in required modules that this code does not call.

**Fix (implemented 2026-11-10).**

- `golang.org/x/text` v0.21.0 → **v0.39.0**, the release that carries the fix,
  and `golang.org/x/sync` v0.10.0 → v0.21.0 alongside it.
- v0.39.0 requires Go 1.25, so `go.mod` moves to `go 1.25.0` and the build stage
  of the Dockerfile moves from `golang:1.22-alpine` to `golang:1.25-alpine`.
  CI reads the version from `go.mod`, so it follows automatically.
- `go mod tidy` now marks the directly imported modules (`gorm.io/gorm`,
  `gorm.io/driver/sqlite`, lipgloss, bubbletea, bubbles, x/ansi) as direct
  requirements; they had been annotated `// indirect` since the imports changed.
- Go 1.25's vet flagged one pre-existing bug: `logBBSAction` was called with a
  runtime-built string as its format argument in the sysop message editor. It is
  now passed as `"%s", logDetail`. Every other call site was already constant.

`govulncheck` now reports **no vulnerabilities reachable from this code**. It
still lists 23 advisories in required modules that no symbol here calls; those
are dependency-tree noise until a future import makes one reachable.

### M6. Log rotation can drop records and duplicate them — FIXED

**Files (as found):** `bbs/cmd/bbs/aprs_supervisor.go`

`rotateLog()` copied the active file and then called `Truncate(0)`. Anything
appended between the copy and the truncate was lost — and appends to these files
come from other processes (the receiver, every BBS session). There was no lock
and no `rename`-based handoff. In the other direction, `tailLogFile()` resets
its offset to zero when it observes a shrink, so post-rotation it re-emitted
whatever was left instead of resuming cleanly.

**Fix (implemented 2026-11-10). Rotation is a rename under a lock.**

- An exclusive `flock` on a dedicated `<log>.lock` file serialises rotation.
  The lock cannot live on the log itself, because the rename replaces that
  inode: a waiter would wake holding the archived file and rotate an empty one
  under a second name.
- After the lock, the active file is stat'ed **by path**, renamed to its dated
  archive, and a fresh file with the same permissions is created. If creating
  the replacement fails, the archive is renamed back so appenders are never left
  without a path.
- No data can be dropped: `appendLogFile` opens the path on every write, so a
  writer either lands before the rename (and correctly ends up in the archive)
  or opens the new active file afterwards. Nothing is duplicated either, because
  nothing is copied.
- The tailer's shrink detection now does the right thing — the new active file is
  empty, so it resumes from zero against the new inode.

`filelock_unix.go` implements the lock with `syscall.Flock`;
`filelock_other.go` is a no-op for non-unix builds, where rotation is
single-process anyway.

**Verification** (`logrotate_test.go`): the rename contract and file
permissions, appends landing on the correct side of a rotation, the empty-log
no-op leaving no stray archive, four rotations racing (exactly one may claim the
payload, and no archive is left empty), and the tailer's offset reset.

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

### M8. Unbounded reply depth — FIXED

**Files (as found):** `bbs/cmd/bbs/boards.go`

The 500-message cap applied only to top-level messages. Replies were appended
without limit, and `flattenMessages`, `saveMessageRows`, `totalMessages` and
`deleteMessageAtPath` all recurse over the tree, so a user who replied
repeatedly produced a thread bounded only by patience; rendering it into a
22-line panel cost `O(depth²)` label construction.

**Fix (implemented 2026-11-10). Nesting is capped at five levels.**

- `maxReplyDepth = 5` (`config.go`): a thread is at most six rows — a root plus
  five nested replies.
- The cap is enforced in the storage layer (`createMessageRow` →
  `messageDepth`), which walks the parent chain in SQL with a hard bound so a
  corrupt parent chain terminates instead of looping. A caller that bypasses the
  form hits the same wall.
- `loadBoardMessages` derives each message's `Depth`; `readMessage` drops the
  reply action at the limit, and `replyToMessage` shows a translated
  `thread_too_deep` message rather than a raw error.

**The per-board budget is unchanged and stays top-level only, as intended:**
replies are bounded by depth rather than by count, so a deep thread never
consumes the 500-message allowance. `TestRepliesDoNotCountTowardMessageBudget`
pins it: a board holding 500 top-level messages plus a full-depth thread
survives a trim untouched, and the 501st top-level message drops only the oldest
thread.

---

## Low / notes

- **L1. Predictable APRS message IDs — ACCEPTED.** `aprsMessageIDs()`
  (`aprs.go`) seeds from `time.Now().UnixMilli()` plus the callsign characters
  rather than a CSPRNG, and `markSentAPRSAck()` accepts any ack matching
  `(user, from, to, id)`, so a third party who guesses the next ID can pre-ack a
  message.

  Accepted because an unpredictable ID would not buy anything: **APRS-IS
  broadcasts every packet in clear text.** Anyone who wants to forge an ack
  reads the real message ID off the wire — no guessing required — so moving from
  a timestamp-seeded ID to `crypto/rand` would remove a shortcut nobody needs
  while adding an entropy source whose failure modes are harder to reason
  about. APRS has no authentication to strengthen here; the ack is advisory
  (it marks a local history row as acknowledged) and cannot be used to change
  state on another station. Randomising the ID would also make the IDs harder to
  correlate with the sent history during support work, for no security gain.
- **L2. Unmetered registration cost — FIXED.** Creating an account runs
  `hashPassword()` at 200k PBKDF2 iterations (`storage.go`), and nothing limited
  how many accounts one caller could create, so walking the address space could
  burn CPU at will.

  `registrationAllowed` / `recordRegistration` (`ratelimit.go`) now meter new
  accounts per client address: **five per hour**, reset on a rolling window and
  pruned nightly with the login counters. Design notes:

  - The check does not consume a slot. A caller who opens the registration form
    and cancels is not charged; the count increments only after an account
    exists. Two simultaneous registrations from one address can therefore
    overshoot by one, which is bounded and acceptable for a limit aimed at bulk
    creation.
  - Only *new* accounts count. Completing an existing profile, or setting a
    password on a half-registered account, is not metered — it creates no row
    and costs one hash the user has already been entitled to.
  - Five per hour leaves room for a club station where several operators share
    one address, while keeping the cost of a per-address attack to a handful of
    hashes.
  - A block is logged (`registration_blocked`) and refused before the
    registration form is drawn, so a limited caller cannot sit in the form.

  Tests (`ratelimit_test.go`): the window and per-address budget, that checking
  without recording never charges, prune behaviour, and two end-to-end paths
  through `authenticate()` — one blocked before the form, one charged exactly
  once.
- **L3. User enumeration — ACCEPTED (by design).** Unknown callsigns go to a
  registration form, known ones to a password prompt (`auth.go`). Accepted:
  the station directory lists every active callsign, full name and locator to
  any logged-in caller, and registration is open by design — "does this
  callsign have an account here?" is public information on this service, not a
  secret the login flow could protect. Obscuring it would mean either closing
  self-registration (which H1 already notes as possible follow-up work) or
  making the directory private, and neither is warranted for a community BBS
  whose point is that operators can find each other.
- **L4. `aprs-test.py` is a stray development script — FIXED (removed).** It
  carried a real callsign and its derived APRS-IS passcode and, if run, opened
  a live APRS-IS session. The passcode is a public function of the callsign, so
  nothing secret leaked, but the file sat in the repository root next to
  production code and was one `python3 aprs-test.py` away from generating
  traffic from the operator's station.

  Removed. It was a scratch script from the pre-Go prototype, superseded by the
  receiver in `aprs_receiver.go`, and nothing in the build, tests or deployment
  referenced it. Anyone who wants to poke at APRS-IS directly can write the
  ten-line socket client in a scratch directory; it does not belong in this
  repository.
- **L5. World-readable data files — ACCEPTED (shared with the web BBS).** The
  SQLite database, `bbs.log`, and `aprs.log` are created `0644` (`logging.go`,
  `aprs_supervisor.go`). The database holds every user's email and password
  hash, and `aprs.log` holds message text including third-party packets, so
  `0600`/`0640` would be strictly better *in isolation*.

  It is not isolated: `/var/lib/bbs` is a volume shared with the companion web
  BBS, which reads the same database. Verified on the live cluster: both
  `deployment/bbs` and `deployment/bbs-web` mount the same claim at
  `/var/lib/bbs`, and `bbs-web` can open `bbs.sqlite` today precisely because of
  the `0644` mode. Tightening the files would break the web front end unless
  both images agreed on a shared uid/gid, which is a cross-repository change to
  a service this one does not own.

  The mode is also not the boundary here. Both containers run as `root` inside
  their own namespaces, and the real access control is the host path's
  permissions on `/opt/stacks/ham-bbs/data/bbs` plus container isolation — a
  `0600` file owned by `bbs` would be just as readable to a root process as a
  `0644` one. Accepted on that basis, with the caveat recorded: if the web BBS
  ever moves to a non-root uid, the two images should agree on a group and the
  files should move to `0640` together.
- **L6. `/etc/bbs/bbs.env` is `0644` and sourced by bash — ACCEPTED.**
  (`docker-entrypoint.sh`, `bbs_session.sh`). The file deliberately excludes
  `BBS_USER_PASSWORD`, every value is written with `printf '%q'` so the
  serialisation round-trips through the shell safely, and the writer is the
  entrypoint itself — the only thing that can put content there is the operator's
  own environment. Accepted on that basis, with the assumption stated so it is
  not mistaken for a property of the format: this is a shell-parsed file whose
  safety rests on `%q` quoting and on operator input being trusted. If a future
  change ever writes a value there from user input, or drops `%q`, the file
  becomes an injection vector and must be replaced with explicit `export`
  statements or a non-shell format.
- **L7. `readJSON` swallowed malformed JSON — FIXED.** A corrupt
  `translations.json` silently yielded an empty translation map, so the BBS came
  up looking healthy and every screen showed raw keys.

  `readJSON` now returns a wrapped `parse <path>: <err>` for a malformed file
  while still applying the fallback, and `newApp` records it to `bbs.log` as
  `action=translations_parse_error` with the path. Startup still succeeds: a BBS
  that refuses to boot because one JSON file is corrupt is worse than one that
  runs degraded and says so in its log.

  Tests: `readJSON` returns an error naming the file for malformed input and
  still fills the fallback, and an end-to-end `newApp()` run against a corrupt
  file starts successfully and leaves the log line.
- **L8. `parseSysops` did not validate — FIXED, plus a sysop marker on screen.**
  A typo in `BBS_SYSOPS` was accepted, normalised, and then simply never
  matched, so an operator could believe they had a sysop when they did not.

  Three changes:

  - `parseSysops` (`app.go`) validates every entry against `callsignRE` and
    returns an error naming the offending entries, so `newApp` fails at startup
    with `BBS_SYSOPS contains N invalid callsign(s): …`. Empty entries are still
    ignored, which is what an unset variable looks like.
  - `newApp` parses before building the config, so a bad value stops the process
    instead of producing a half-configured app.
  - Since H1 made `BBS_SYSOPS` bootstrap-only, an operator who configures it and
    never logs in with one of those callsigns has no sysop at all — and nothing
    said so. `reportBootstrapSysopState` (`app.go`, called from `seedData` once
    per process) logs one of `sysop_bootstrap_unclaimed` (no account holds the
    role; the first listed callsign to log in is granted it) or
    `sysop_bootstrap_pending` (an account exists, role arrives on next login),
    and stays silent once a sysop is active.

  **Sysop marker in the screens.** The role is now in the banner, which every
  screen renders, instead of only the main-menu header: `app.currentUserIsSysop`
  is set at login and refreshed on a profile change, and `banner()` appends
  `[Sysop]` to the current-user line. A sysop therefore cannot mistake which
  session they are in — the distinction matters because the sysop menu mutates
  other people's accounts.

  Tests: `parseSysops` rejects and names a bad entry; `newApp` refuses to start
  on one; the banner carries the marker only for a sysop session; and the
  bootstrap-state reporter logs unclaimed, pending, and nothing once a sysop
  exists.
- **L9. Watchdog could not recover a wedged child — FIXED.**
  `watchAPRSReceiver()` started the receiver and blocked in `Wait()` with no
  timeout or kill path, so a receiver stuck in a dial or a filesystem call left
  the watchdog waiting forever — the hourly self-restart only helps a child that
  is still making progress.

  The supervisor now runs each receiver through `runSupervisedProcess`
  (`aprs_supervisor.go`), which uses `exec.CommandContext` with a **75-minute
  ceiling** and reports whether the deadline fired, so the log distinguishes
  "killed because wedged" from "exited with an error". 75 minutes sits above the
  receiver's own 60-minute restart, so a healthy process is never killed; a
  test asserts that relationship rather than trusting the constant.

  `cmd.WaitDelay` (10 s) bounds the wait for output to drain after the kill, so a
  stuck pipe cannot re-create the same stall. The receiver spawns no children, so
  killing the direct child is sufficient here — a supervisor for a process that
  does spawn children would also need a process group.

  Tests: a child outliving its deadline is killed and the call returns promptly,
  normal and failing exits are reported correctly, a missing binary surfaces the
  start error, and the ceiling exceeds the receiver's self-restart interval.
- **L10. Triple restart layering — RECOMMENDATION RECORDED (not implemented).**
  Three independent retry loops sit on top of each other, each with its own
  delay:

  1. `docker-entrypoint.sh` loops `bbs_app aprs-supervisor` with a fixed 10 s
     sleep, in a background shell, then `exec`s sshd.
  2. `watchAPRSReceiver()` loops `bbs_app aprs-receiver` with a fixed 10 s
     sleep.
  3. `runAPRSReceiver()` reconnects internally, and exits deliberately every
     hour so the supervisor restarts it and the APRS-IS session is refreshed.

  Failure behaviour is therefore hard to observe: a receiver that crashes every
  30 seconds produces a 30-second restart loop, a silent shell loop that never
  gives up, and no single place that says how many times something has failed.
  Nothing backs off, so a persistent failure becomes a steady 10-second retry
  churn, and the hourly exit means a healthy process is killed and recreated
  once an hour for no reason other than to recycle a socket.

  **Recommended fix: one supervisor, one backoff, and the container runtime as
  the outermost layer.**

  - Make `bbs_app aprs-supervisor` the container's main process, started under
    `tini` as PID 1. It starts sshd and the receiver as children and waits on
    both. Any fatal error returns from `main`, the container exits non-zero, and
    the runtime restarts it — `restartPolicy: Always` on k8s, `restart:
    unless-stopped` in compose. That is the outermost loop and it already
    exists; the shell `while true` in the entrypoint should be deleted.
  - The supervisor supervises the receiver with **exponential backoff and
    jitter** (`1s, 2s, 4s … capped at 60s`), resetting after a run that lasted
    longer than, say, a minute. That gives one place to log attempts, delays and
    give-up thresholds.
  - Drop the receiver's hourly exit. The reason for it — refreshing the APRS-IS
    session — is better served by closing and re-opening the connection inside
    `runAPRSReceiver()`'s existing loop, which avoids a process restart, keeps
    the multipart buffers, and removes the third loop entirely.
  - Keep sshd's lifetime tied to the supervisor's, so a supervisor crash is a
    container restart rather than a container that serves SSH with a dead APRS
    path.

  This is recorded rather than implemented because it restructures process
  ownership in the image (entrypoint, supervisor, sshd, receiver) and deserves
  its own change with a live test, not a tail-end addition to a review sweep.
  L9's deadline makes the current arrangement survivable in the meantime: the
  watchdog can no longer block forever.
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
4. ~~**H4** — placeholder password defaults~~ — **accepted, safe and as
   intended** (see H4: guest mode deletes the transport password, so the
   placeholder is never applied; the config hazard only exists with guest mode
   off).
5. ~~**M5** — dependency update~~ — **done**; ~~**M7**~~ — **done** in the paths
   H2 rewrote (see M7; read paths still ignore load errors).
6. ~~**M1, M2** — rate limits at the two places untrusted input arrives (BBS
   login, APRS inbound)~~ — **done** (see M1 and M2).
7. ~~**M3** — CI runner and permission hardening~~ — **done** (see M3);
   ~~**M4** — WireGuard capabilities and compose tags~~ — **accepted** (see M4).
8. ~~**M6, M8**~~ — **done** (see their sections); **L1–L11** — hygiene.

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
