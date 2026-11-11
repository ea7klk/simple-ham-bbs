# HAMNET Radio BBS

A small SSH BBS for amateur radio operators, shipped as a container image. It listens on SSH port `2222` and, when the WireGuard profile is installed, is reachable through HamNet on the WireGuard interface as well.

Production runs as a k3s Deployment delivered by Fleet from a GitOps repository — see [Deployment](#deployment). The compose files here are a **local development convenience** for running the stack on a workstation; they are not part of the deployed system and are not a supported production path.

The first version is intentionally no-frills: a Charm Bubble Tea/Lip Gloss terminal UI, forms, cursor-key menus, paginated lists, local message boards, station directory, bulletins, and APRS-IS message sending/receiving. Screens target a 132×24 terminal; on each screen the BBS sends a best-effort xterm resize request to the client and keeps Bubble Tea's logical renderer at the same size. If the SSH client blocks remote window resizing, enable its window-operation permission or set the client window to 132×24 manually; SSH has no protocol-level server request for changing the local emulator window.

## Why This Shape

This project uses OpenSSH as the transport and a small Go BBS application as the forced SSH command. The terminal interface is built with Charm's Bubble Tea, Bubbles, and Lip Gloss toolkits. That keeps the first deployment simple, inspectable, and easy to extend. If you later want to swap the app layer for a larger open-source BBS package such as Synchronet or ENiGMA 1/2, the container boundary and HamNet wiring can stay mostly the same.

## Files

- `compose.yaml`, `compose.dockge.yaml` — local development convenience only; see [Local development](#local-development). Not used by the deployment.
- `bbs/` contains the SSH server image and BBS application.
- `bbs/cmd/bbs/` contains the Go BBS application.
- `bbs/go.mod` declares the Charm terminal UI dependencies.
- `.github/workflows/docker-image.yml` tests, builds and pushes the image.
- `hamnet/wg0.conf.example` contains a safe, redacted WireGuard example.
- `hamnet/wg_confs/` is ignored by git and should contain the real WireGuard config.
- `bbs-data` is the Docker named volume that stores the SQLite database and logs.
- `ssh/authorized_keys` is optional and ignored by git.

## Local development

The steps below run the stack with Docker Compose on a workstation, to try the
BBS or work on it. They are a convenience, not the deployment path: the compose
files reference floating `:latest` tags and are not kept in step with what the
cluster runs. For anything beyond local use, deploy the image through the GitOps
repository described in [Deployment](#deployment).

1. Copy the environment file:

   ```sh
   cp .env.example .env
   ```

2. Edit `.env` and set a strong `BBS_USER_PASSWORD`.

3. Install the real HamNet WireGuard config:

   ```sh
   mkdir -p hamnet/wg_confs
   cp hamnet/wg0.conf.example hamnet/wg_confs/wg0.conf
   ```

4. Edit `hamnet/wg_confs/wg0.conf` and replace `REPLACE_WITH_PRIVATE_KEY` with your real private key.

5. Start the BBS:

   ```sh
   docker compose up -d --build
   ```

6. Connect locally:

   ```sh
   ssh -p 2222 bbs@localhost
   ```

With the WireGuard profile installed, the same port is reachable from HamNet on
the assigned address:

```sh
ssh -p 2222 bbs@44.27.132.79
ssh -p 2222 bbs@<docker-host-address>
```

That applies to whichever way the stack is running — a local compose run with
the WireGuard container, or the Fleet deployment.

## SSH Access

The Docker image creates one transport user:

- Username: `bbs`
- Password: `BBS_USER_PASSWORD` from `.env`

Once connected, the BBS asks callers for their amateur radio callsign or handle. You can also use public-key auth by creating `ssh/authorized_keys`; the entrypoint copies it into the `bbs` user's SSH config on startup.

The SSH account is only the transport. The BBS has its own callsign-based account layer:

- Unknown callsigns are registered on first login.
- Full name and email are mandatory.
- Maidenhead locator is optional.
- Language is mandatory: English, Spanish, French, or German.
- Users set a BBS password during registration.
- BBS passwords are stored as salted PBKDF2-SHA256 hashes.
- Returning users must enter their BBS password after entering their callsign.
- The menu and profile flow are shown in the user's selected language.
- Profile changes are available from `Change my profile`, including password changes with matching password fields.
- Interactive terminals can use cursor keys and Enter for menus; dialog-style forms use Tab to move between fields/buttons, direct typing to edit, the Save/Cancel buttons, and F2 as a save shortcut. Long message text fields word-wrap, and Up/Down scroll within the text field instead of moving form focus. Scripted/non-interactive sessions can still type menu numbers and field values.
- Menu translations live in `bbs/translations.json`, separate from the main BBS application code.
- Five failed BBS passwords from one address within fifteen minutes lock that address out for fifteen minutes. The counter is shared by all sessions, so reconnecting does not reset it.
- New accounts are limited to five per hour from one address. Completing an existing profile is not counted.
- The banner shows `[Sysop]` next to the callsign when the session has the sysop role, on every screen.
- Accounts are activated by email when SMTP is configured: registration says a
  code is coming, and a 4-character code (capitals and digits) must be entered
  before the account can be used. Without SMTP, activation is skipped and logged.
- `Forgot password` on the login screen emails a reset link valid for 2 hours.
- Changing the email address in the profile sends one confirmation link to the
  current address and one validation link to the new address; the change applies
  only when both are confirmed, and the links are valid for 24 hours.

Sysop users can administer accounts from the sysop menu:

- Promote or demote users as sysops.
- Disable or re-enable users.
- Delete users.
- Publish new bulletins.
- Add, rename, or delete local message boards.
- Edit or delete individual messages from message boards.
- Sysops cannot delete or disable their own account.
- The BBS prevents removal of the last active sysop.
- The user list shows each account's registration date alongside its last
  connection, status and role; the account detail screen repeats it.

The registration date is recorded when the account is created and never
changes. Accounts that predate the field take their date from the first-seen
timestamp written at creation, backfilled automatically on startup.

Bootstrap a sysop from `.env` with a comma-separated callsign list:

```sh
BBS_SYSOPS=EA1ABC,DL1ABC
```

`BBS_SYSOPS` is a **bootstrap only**: it is read while the BBS has no active
sysop at all, and the first listed callsign that logs in is granted the role.
As soon as any active sysop exists, the variable is ignored completely, so a
listed callsign that registers later gets an ordinary account.

Two consequences worth knowing before you expose the port:

- Register your sysop callsign immediately after the first start, before anyone
  else can. Until an account holds the role, the bootstrap grant is available to
  whoever claims a listed callsign first.
- Once bootstrapped, a sysop is an ordinary sysop: they can be demoted from the
  sysop menu like anyone else, subject to the last-active-sysop guard. Remove
  the entry from `BBS_SYSOPS` afterwards if you do not want it lying around.

Entries are validated at startup: an invalid callsign in `BBS_SYSOPS` stops the
BBS with an error naming it, rather than being silently ignored. If no sysop
exists yet, the startup log says whether the bootstrap is still unclaimed
(`sysop_bootstrap_unclaimed`) or waiting for a login
(`sysop_bootstrap_pending`).

There is no reliable way to verify that a caller owns the callsign they type.
Anyone can claim any callsign that does not yet have an account, so treat BBS
accounts as unverified pseudonyms rather than identities.

The local message board feature supports multiple boards. New installs start with a `General` board. Existing installs with the older flat `messages.json` format are migrated automatically into the `General` board the next time the BBS starts.

For an open guest BBS, set this in `.env`:

```sh
BBS_GUEST_MODE=true
```

Then callers can connect without an SSH password:

```sh
ssh bbs@localhost -p 2222
```

SSH still requires a username at the protocol level. To hide that from regular callers, they can add a local SSH alias:

```sshconfig
Host ham-bbs
  HostName 44.27.132.79
  Port 2222
  User bbs
```

Then they connect with:

```sh
ssh ham-bbs
```

## HamNet WireGuard Notes

The supplied WireGuard profile has these public/non-secret values:

- Interface addresses: `44.27.132.79/32`, `fe80::29b2:e119:a855:66af/128`, `44.27.25.64/28`
- DNS: `1.1.1.1`, `1.0.0.1`
- MTU: `1380`
- Peer endpoint: `44.27.227.1:44000`
- Persistent keepalive: `20`
- Allowed IPs: `0.0.0.0/0`, `::/0`

The private key you pasted is deliberately not written into tracked files. Put it only in `hamnet/wg_confs/wg0.conf` or rotate it before using this repository anywhere public.

## APRS Messaging

The APRS menu currently lets each user set `Enable APRS` to `true` or `false`
through a dialog-style form. The send screen sends APRS message packets
directly to APRS-IS from Go. For each send, the BBS uses the logged-in user's
callsign with SSID `-0` and calculates the APRS-IS passcode automatically.
Messages longer than the APRS message body limit are split into numbered parts
before sending.

Threads nest at most five replies deep. Only top-level messages count toward the
500-message per-board budget, so a deep thread never uses up the message
allowance.

Users, bulletins, local boards, threaded messages, and APRS history are stored
in SQLite through GORM. The default database path is:

```sh
/var/lib/bbs/bbs.sqlite
```

The latest 200 sent APRS messages and 500 received APRS messages per BBS user
are retained in the database.

The container also starts an APRS supervisor helper:

```sh
/usr/local/bin/bbs_app aprs-supervisor
```

The supervisor tails APRS logs into the container's own stdout (so they appear
in `kubectl logs`, or `docker compose logs` for a local run), rotates those
logs nightly at 03:00 UTC, keeps rotated logs for seven days, and watches the
APRS receiver process. The receiver connects to APRS-IS with `filter t/m`,
listens for APRS message packets, and stores messages addressed to BBS users
who have `Enable APRS` set to `true`. Received messages are stored per callsign
in the database and are shown under `Received APRS messages` in the APRS menu.
APRS message IDs appended by senders, such as `{2044`, are removed from the
user-facing message text before storage/display, while the raw packet field is
preserved as received. Set `APRS_RECEIVER_CALLSIGN` in `.env` to choose the
receive-only APRS-IS login callsign; if it is empty, the receiver uses the
first valid callsign from `BBS_SYSOPS`, then falls back to `N0CALL`. The
receiver exits once per hour; the supervisor restarts it so the APRS-IS
connection is refreshed regularly. Acknowledgements for received messages are
queued instead of dialled inline: the queue holds 64, sends at most one every
ten seconds, and drops (with a logged count) when inbound traffic outruns that
budget, so no third party can turn inbound packets into an outbound dial storm.

APRS send attempts, generated packets, APRS-IS login responses, and receiver
activity are appended to `/var/lib/bbs/aprs/aprs.log`. BBS user and sysop
activity is appended to `/var/lib/bbs/bbs.log`. The supervisor tails both files
to Docker logs and rotates both nightly at 03:00 UTC, keeping seven days of
archives.

Future APRS work can add:

- Local-only dry-run mode for testing
- A menu screen for station beacons and delivery acknowledgements

## Email

Three account flows use outbound mail: activation at registration, password
reset, and the double confirmation of an email change. All of it is optional and
configured from the environment, so no credential lives in this repository.

```sh
# Mailcow-shaped defaults; see .env.example.
BBS_SMTP_HOST=mail.ea7lk.es
BBS_SMTP_PORT=587
BBS_SMTP_SECURITY=starttls      # starttls (587) | tls (465) | none (local relay)
BBS_SMTP_USERNAME=bbs@ea7lk.es
BBS_SMTP_PASSWORD=...
BBS_SMTP_FROM=bbs@ea7lk.es
BBS_SMTP_FROM_NAME=HAMNET Radio BBS
BBS_WEB_BASE_URL=https://bbs.ea7lk.es
```

Mail is enabled only when `BBS_SMTP_HOST` and `BBS_SMTP_FROM` are both set. With
mail disabled the BBS stays usable: activation is skipped (and logged as
`activation_skipped`), password reset reports that email is not configured, and
an email change is applied immediately with a log line saying it was unverified.

Messages are sent in the user's language (English, German, Spanish, French) and
contain a plain-text link, never an HTML body. Sending is synchronous with a 20 s
deadline, so a stalled server cannot hold a login open.

### Tokenised links and the web BBS

Reset and email-change links point at `BBS_WEB_BASE_URL`, not at the SSH
service:

```
https://bbs.ea7lk.es/reset?token=<token>
https://bbs.ea7lk.es/email/confirm?token=<token>
```

The tokens live in the SQLite database the BBS already shares with the web BBS
(`/var/lib/bbs`), so the web front end can honour a link without calling back
here. The contract is small:

- `db_tokens.token_hash` is the hex SHA-256 of the token in the URL. Look the row
  up by that hash; nothing else in the database identifies the link.
- Reject the link when `consumed_at` is set or `expires_at` has passed. Set
  `consumed_at` to the current Unix time when it is accepted — tokens are
  single-use.
- `purpose` is `password_reset`, `email_change_old` or `email_change_new`.
- For an email change, both rows share a `change_id`. The BBS applies the new
  address itself once both halves are marked consumed (checked at startup and on
  each login), so the web front end does not write to the user table.
- A password reset has to write `db_users.password_hash` in the format
  `pbkdf2_sha256$<iterations>$<base64 salt>$<base64 digest>` (SHA-256, 200000
  iterations). Alternatively, change that flow to a form the web BBS owns and
  keep this repository's table untouched.

`db_activations` holds the pending activation code hash for an account; it is
only used by the SSH flow and the web BBS can ignore it.

## Operations

View logs:

```sh
kubectl -n ham-bbs logs -f deploy/bbs
kubectl -n ham-bbs logs -f deploy/bbs-web
```

Stop:

```sh
kubectl -n ham-bbs scale deploy/bbs --replicas=0
```

Backup BBS data (the SQLite database and logs live on a host path):

```sh
tar -czf bbs-data-backup.tgz -C /opt/stacks/ham-bbs/data/bbs .
```

## Deployment

The cluster runs this BBS as a **k3s Deployment managed by Fleet**, not as a
Docker Compose stack. There is no Docker daemon on the deployment host, so
`docker compose` commands do not apply there.

The source of truth is the GitOps repository:

- Repository: `spainip/infra`
- File: `fleet/managed-apps.yaml`
- Bundle: `infra-fleet`, reconciled by Fleet into namespace `ham-bbs`

The BBS image is pinned by digest, and Fleet has drift correction enabled. That
has two consequences worth knowing before you try to ship a change:

- `kubectl set image` and `kubectl rollout restart` are reverted on the next
  reconcile. Change the digest in `spainip/infra` instead.
- The `:latest` tag is published for convenience but is not what runs. The
  pinned digest is.

A release goes out in two steps:

1. Publish a GitHub release. CI tests, then builds and pushes
   `ghcr.io/ea7klk/simple-ham-bbs:<tag>` and `:latest` on GitHub-hosted runners,
   and prints the published digest in the job summary.
2. Pin that digest in `spainip/infra` `fleet/managed-apps.yaml`. If the
   `INFRA_REPO_TOKEN` secret is configured (a fine-grained PAT with
   `contents: write` on that repository), the workflow does this automatically;
   otherwise copy the digest from the job summary and commit it by hand.

To rebuild an image for a tag whose commit predates the current workflow (for
example `v1.20`, published before the pipeline was fixed), run the workflow
manually on the branch that has the fix:

```sh
gh workflow run docker-image.yml --ref main -f version=v1.20
```

`/opt/stacks/ham-bbs` on the host is legacy: it still holds the data and SSH
bind paths that the Deployments mount, but its `compose.yaml` is not used and
must not be treated as the deployment definition.

For the same reason, `compose.yaml` and `compose.dockge.yaml` in this repository
do not describe production. They are workstation conveniences (see
[Local development](#local-development)); they use floating `:latest` tags and
are not kept in step with the cluster.
