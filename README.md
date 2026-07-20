# HAMNET Radio BBS

A small, fully Dockerized SSH BBS for amateur radio operators. It listens on SSH port `2222` and, when the WireGuard profile is installed, is reachable through HamNet on the WireGuard interface as well.

The first version is intentionally no-frills: a Charm Bubble Tea/Lip Gloss terminal UI, forms, cursor-key menus, paginated lists, local message boards, station directory, bulletins, and APRS-IS message sending/receiving. Screens target a 132×43 terminal; on each screen the BBS sends a best-effort xterm resize request to the client and keeps Bubble Tea's logical renderer at the same size. If the SSH client blocks remote window resizing, enable its window-operation permission or set the client window to 132×43 manually; SSH has no protocol-level server request for changing the local emulator window.

## Why This Shape

This project uses OpenSSH as the transport and a small Go BBS application as the forced SSH command. The terminal interface is built with Charm's Bubble Tea, Bubbles, and Lip Gloss toolkits. That keeps the first deployment simple, inspectable, and easy to extend. If you later want to swap the app layer for a larger open-source BBS package such as Synchronet or ENiGMA 1/2, the container boundary and HamNet wiring can stay mostly the same.

## Files

- `compose.yaml` runs WireGuard and the BBS containers.
- `bbs/` contains the SSH server image and BBS application.
- `bbs/cmd/bbs/` contains the Go BBS application.
- `bbs/go.mod` declares the Charm terminal UI dependencies.
- `hamnet/wg0.conf.example` contains a safe, redacted WireGuard example.
- `hamnet/wg_confs/` is ignored by git and should contain the real WireGuard config.
- `bbs-data` is the Docker named volume that stores the SQLite database and logs.
- `ssh/authorized_keys` is optional and ignored by git.

## Quick Start

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

From HamNet, connect to the assigned WireGuard address on SSH port `2222`:

```sh
ssh -p 2222 bbs@44.27.132.79
ssh -p 2222 bbs@<docker-host-address>
```

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

Sysop users can administer accounts from the sysop menu:

- Promote or demote users as sysops.
- Disable or re-enable users.
- Delete users.
- Publish new bulletins.
- Add, rename, or delete local message boards.
- Edit or delete individual messages from message boards.
- Sysops cannot delete or disable their own account.
- The BBS prevents removal of the last active sysop.

Bootstrap one or more sysops from `.env` with a comma-separated callsign list:

```sh
BBS_SYSOPS=EA1ABC,DL1ABC
```

Calls listed in `BBS_SYSOPS` are always treated as sysops and cannot be demoted from inside the BBS.

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

The supervisor tails APRS logs into `docker-compose logs bbs`, rotates those
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
connection is refreshed regularly.

APRS send attempts, generated packets, APRS-IS login responses, and receiver
activity are appended to `/var/lib/bbs/aprs/aprs.log`. BBS user and sysop
activity is appended to `/var/lib/bbs/bbs.log`. The supervisor tails both files
to Docker logs and rotates both nightly at 03:00 UTC, keeping seven days of
archives.

Future APRS work can add:

- Local-only dry-run mode for testing
- A menu screen for station beacons and delivery acknowledgements

## Operations

View logs:

```sh
docker compose logs -f bbs
docker compose logs -f hamnet
```

Stop:

```sh
docker compose down
```

If your machine has the older Compose command, use `docker-compose` in place of `docker compose`.

Backup BBS data:

```sh
docker run --rm -v bbs-hamnet_bbs-data:/data -v "$PWD":/backup alpine \
  tar -czf /backup/bbs-data-backup.tgz -C /data .
```
