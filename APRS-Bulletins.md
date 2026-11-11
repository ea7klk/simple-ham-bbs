# APRS bulletins in the BBS

Status: **text-mode implementation done**. Web version not implemented.
Date: 2026-11-11

## Goal

Let BBS users read every APRS bulletin received in the last ten days, grouped by
meaningful categories. Today they are received and thrown away.

## What the receiver does now

The APRS receiver subscribes to APRS-IS with `filter t/m`, parses each
message-format packet, and calls `storeReceivedAPRS`. That function resolves the
addressee as a **user account**:

```go
key := aprsRecipientKey(msg.To)      // "BLN1NWS" -> "BLN1NWS"
profile, ok := a.loadUser(key)
if !ok { return key, false, nil }    // no such user -> silently dropped
```

APRS bulletins address `BLNn` plus a group, not a callsign, so they match no
account and are discarded. They never reached a table, a screen, or even a log
line — confirmed by reading `/var/lib/bbs/aprs/aprs.log`, which shows real
ANSRVR, APRSPH and station traffic and zero `BLN` entries.

## The APRS bulletin format

A bulletin is a message-format packet whose addressee field is `BLNn` followed by
a five-character group, space-padded to the nine-character address field:

```
:BLN1NWS  :Tornado warning until 21:00Z for the metro area
 ^^^ ^^^^
 slot group
```

- Slots are `BLN1`–`BLN5` per APRS101; other digits occur in practice, so the
  slot is parsed but treated as advisory.
- **Bulletins repeat.** Weather products are retransmitted every 10–30 minutes,
  so duplicates are the norm rather than the exception. Dedupe is by content.
- A trailing message ID (`{XXXXX`) is present on some; the existing
  `stripAPRSMessageID` handles it.
- Bulletins must never be acknowledged. They already are not, because the ack
  path resolves the addressee as a user too; that behaviour is preserved
  deliberately.
- The `[n/m]` multipart prefix this BBS uses for long messages is its own
  convention, not an APRS standard, so it rarely applies to third-party
  bulletins. The existing buffer is reused when it does.

## Storage

One table, `db_aprs_bulletins`, global rather than per-user:

| Column | Purpose |
| --- | --- |
| `id` | primary key |
| `dedupe_key` | unique; SHA-256 of sender, group and normalised text |
| `first_seen` / `last_seen` | timestamps as this BBS writes them |
| `seen_count` | how many times the same bulletin arrived |
| `from` | sending station, normalised |
| `slot` | 1–9, advisory |
| `group` | raw group, upper case, up to five characters |
| `category` | resolved category id |
| `text` | bulletin body, sanitised |
| `raw` | the packet as received, sanitised |

A repeat upserts: `seen_count` increments and `last_seen` moves. The row is not
duplicated, so the list stays readable when a sender repeats every ten minutes.

Retention is ten days on `last_seen`, not `first_seen`, so a bulletin that is
still being transmitted stays available. The sweep rides the nightly housekeeping
loop in `rotateRuntimeLogsNightly`, alongside the existing throttle and token
pruning. A hard row cap backs it up.

Indexes: unique on `dedupe_key`, plus `category` and `last_seen` for the screens.

Every text field passes through `sanitizeTerminalText` on write and on read.
This is unmoderated third-party content rendered into other users' terminals; it
gets the same treatment as every other user-visible string (see H3 in
`REVIEW.md`).

Volume: at a few hundred global bulletins a day, ten days is a couple of
thousand rows and well under a megabyte — irrelevant next to the 100 MiB
measured in `SCALABILITY.md`.

## Categories

The group field is chosen by the sender and is often empty or a local
abbreviation, so a single mapping is not enough. Resolution is staged:

1. **Declared group** — authoritative. `NWS` maps to weather.
2. **Content fallback** — for an empty or unrecognised group, match keywords in
   the text: `WARNING`, `WATCH`, `TORNADO`, `FLOOD`, `EVACUATION`, `EMERGENCY`,
   `NET`, `CONTEST`, `DX`, `SAT`, `OUTAGE`, and so on.
3. **Default** — `general`.

An unrecognised group is logged once (`aprs_bulletin_unknown_group`) so the
taxonomy can grow from observed traffic instead of guesses.

| Category | Description | Typical groups |
| --- | --- | --- |
| `weather` | Watches, warnings, advisories and spotter activation | `NWS`, `WX`, `SKY`, `SKYW` |
| `emergency` | Declared emergencies, evacuations, served-agency coordination | `EMERG`, `EMER`, `ARES`, `RACES` |
| `traffic` | National Traffic System and formal radiogram traffic | `NTS`, `TRAFF`, `RADIO` |
| `organisation` | Society bulletins, rule changes, membership notices | `ARRL`, `AMSAT`, `IARU` |
| `nets` | Net times, frequencies and control stations | `NET`, `SCHED` |
| `events` | Field days, contests, special-event stations | `EVENT`, `CONTEST`, `FIELD`, `FD` |
| `dx` | DXpeditions, band openings, propagation forecasts | `DX`, `PROP`, `SPOT` |
| `satellite` | Satellite passes, transponder status, ISS operations | `SAT`, `AMSAT`, `ISS` |
| `network` | APRS network announcements, igate and digipeater service notes | `APRS`, `IGATE`, `DIGI` |
| `service` | Repeater, gateway and infrastructure disruption | `OUTAG`, `MAINT`, `SERV` |
| `test` | Test bulletins, usually from a station trying the feature | `TEST` |
| `general` | Uncategorised: empty group or a bare `BLNn` | — |
| `other` | A group we do not recognise, kept with the raw group shown | — |

`other` exists so nothing is hidden: an unknown group is still readable, and the
raw group is displayed next to it.

## Screens

Implemented as a new entry in the APRS menu (`APRS bulletins`), read-only and
available to any logged-in user. It
is deliberately **not** gated on `EnableAPRS`: that flag governs sending and
receiving mail for an account, and reading public bulletins harms nobody.

1. **Categories** — each with its count and newest timestamp.
2. **Bulletin list** — newest first, sender and relative age, paginated with the
   menu windowing that already exists.
3. **Detail** — full text, sender, group and slot, first seen, last seen, how
   many times heard, and the raw packet.

Age renders relatively (`2 h ago`) in lists and absolutely in the detail view, so
a repeated bulletin does not look stale.

## Scope decisions

- **Global, not local.** `filter t/m` is unfiltered, so this holds bulletins from
  the whole APRS-IS message stream, not just HamNet. That is what "all received
  bulletins" means, and it is stated in the README.
- **No acks and no per-user copies.** Bulletins are shared. They do not enter
  `db_aprs_receiveds` and do not count against that table's per-user cap.
- **Superseded slots.** Slots `BLN1`–`BLN5` are intended to replace each other.
  History is kept for the ten-day window; the UI does not (yet) mark an earlier
  bulletin as superseded by a later one from the same sender and slot. Recorded
  here as a possible refinement.
- **Web version.** Not implemented. The table is shared with the web BBS, so a
  web screen can read `db_aprs_bulletins` directly without further backend work.

## Implementation notes

- `bbs/cmd/bbs/aprs_bulletins.go` — parsing, categorisation, storage, retention,
  and the relative-age renderer.
- `bbs/cmd/bbs/aprs_bulletin_ui.go` — the three screens.
- The receive loop checks `parseBulletinAddress` **before** the per-user path and
  `continue`s, so a bulletin never reaches the ack queue or
  `db_aprs_receiveds`. That is what keeps a group address from being treated as
  a station to answer.
- The category menu lists only categories that currently hold something, so it
  reflects what has been heard rather than the whole taxonomy.
- Translations: 40 new keys × 4 languages, including a label and a description
  for every category.
- `pruneAPRSBulletins` runs with the other nightly housekeeping in
  `rotateRuntimeLogsNightly`.

## A bug found on deploy, and the fix

The first release of this feature shipped **without its table**. The one-shot
migration from the scalability work is gated on a `schema.version` marker, and
adding a table requires bumping that version — which was forgotten. The marker
read as current, `initialize` skipped the migration, and the deployed pod had no
`db_aprs_bulletins` table. Nothing failed loudly: only an operator querying the
schema would have noticed.

Two changes, because a version constant that must be remembered is a defect with
a delay:

- `schemaModels()` is now the single list of expected tables, used both by
  `AutoMigrate` and by a new `missingTables()` check, so the two cannot drift.
- `initialize` re-runs the migration when any expected table is absent, whatever
  the marker says, and logs `schema_repair` with the names. A forgotten bump now
  degrades to a slower boot instead of a broken feature.

The version was also bumped to `2` for this schema change. A test drops the table
while the marker reads current — the exact state that shipped — and asserts the
next initialisation repairs it.
