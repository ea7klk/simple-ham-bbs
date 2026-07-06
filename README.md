# HAMNET Radio BBS

A small, fully Dockerized SSH BBS for amateur radio operators. It listens on SSH port `2222` and, when the WireGuard profile is installed, is reachable through HamNet on the WireGuard interface as well.

The first version is intentionally no-frills: ANSI text, simple menus, a local message board, station directory, bulletins, and a placeholder integration point for APRS messaging.

## Why This Shape

This project uses OpenSSH as the transport and a small Python BBS application as the forced SSH command. That keeps the first deployment simple, inspectable, and easy to extend. If you later want to swap the app layer for a larger open-source BBS package such as Synchronet or ENiGMA 1/2, the container boundary and HamNet wiring can stay mostly the same.

## Files

- `compose.yaml` runs WireGuard and the BBS containers.
- `bbs/` contains the SSH server image and BBS application.
- `hamnet/wg0.conf.example` contains a safe, redacted WireGuard example.
- `hamnet/wg_confs/` is ignored by git and should contain the real WireGuard config.
- `bbs-data` is the Docker named volume that stores users/messages/bulletins.
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
- Profile changes are available from `Change my profile`, including password changes with current-password verification.
- Menu translations live in `bbs/translations.json`, separate from the main BBS application code.

Sysop users can administer accounts from the sysop menu:

- Promote or demote users as sysops.
- Disable or re-enable users.
- Delete users.
- Sysops cannot delete or disable their own account.
- The BBS prevents removal of the last active sysop.

Bootstrap one or more sysops from `.env` with a comma-separated callsign list:

```sh
BBS_SYSOPS=EA1ABC,DL1ABC
```

Calls listed in `BBS_SYSOPS` are always treated as sysops and cannot be demoted from inside the BBS.

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

## APRS Roadmap

The menu already includes an APRS placeholder. A later implementation can add:

- APRS-IS connection settings through `.env`
- Message send/receive queue under `/var/lib/bbs/aprs/`
- Callsign validation and passcode handling
- Local-only dry-run mode for testing
- A menu screen for inbox, outbox, and station beacons

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
