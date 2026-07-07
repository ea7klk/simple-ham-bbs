# HamNet WireGuard Config

Put the real WireGuard config in:

```text
hamnet/wg_confs/wg0.conf
```

That directory is ignored by git. Start from `wg0.conf.example`, then paste the real private key locally.

The WireGuard config mount is intentionally writable because the LinuxServer WireGuard image adjusts ownership and generated support files during startup. Keep the directory private on the host.

The BBS container shares the WireGuard container's network namespace. That means:

- OpenSSH listens on port `2222` inside the shared container network namespace.
- Docker publishes that same listener as host port `2222`.
- Traffic arriving through the WireGuard interface can reach the BBS directly on SSH port `2222`.

During local development without a valid WireGuard config, the shared namespace
may start without a default route. The BBS container startup checks for that
case and adds a Docker gateway fallback route so outbound services such as
APRS-IS can still work. If WireGuard provides its own default route, the check
does nothing.

If you do not want containerized WireGuard, you can remove the `hamnet` service and give the `bbs` service a normal port mapping instead:

```yaml
ports:
  - "2222:2222/tcp"
```
