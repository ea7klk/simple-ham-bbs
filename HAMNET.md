# HamNET reachability

Status: **investigation only — nothing implemented.** Written 2026-11-11.
Question: what stops the SSH BBS and the web BBS from being reachable over
HamNET (AMPRNet, `44.0.0.0/8`), and what has to change to reach them there
**while they stay reachable from the public internet**?

## Today

### What is reachable now

| Surface | Address | How |
| --- | --- | --- |
| BBS SSH | `135.181.17.116:2222` | pod `hostPort: 2222`, so it binds every node address |
| Web BBS | `135.181.17.116:80` → 301 → `:443` | one Traefik `IngressRoute`, `Host(\`bbs.ea7klk.es\`)`, ACME |
| Egress | default via `135.181.17.65` | Hetzner |
| Also public | `51820/udp`, `51821`, `6443` | wg-easy and the k3s API |

Verified: `ip_forward=1`, `INPUT`/`FORWARD` both `ACCEPT`, no host firewall
rules beyond Kubernetes' own.

### What exists for HamNET

- `hamnet/wg0.conf.example` carries the real, non-secret HamNET parameters:
  address `44.27.132.79/32`, routed block `44.27.25.64/28`, peer
  `44.27.227.1:44000`, MTU `1380`, keepalive `20` — with
  `REPLACE_WITH_PRIVATE_KEY`.
- `compose.yaml` has a `hamnet` service for the standalone setup.
- `/opt/stacks/ham-bbs/hamnet/wg_confs/` exists on the host and is **empty**.

So the prepared work is the design and the compose wiring. No tunnel is running.

## The gap

### 1. There is no HamNET tunnel

`wg-easy` in namespace `wireguard` runs `wg0` as `10.8.0.1/24` with two peers:

```
nas       10.8.0.2/32
pi-star   10.8.0.3/32
```

That is a generic VPN, not AMPRNet. Its `AllowedIPs` cover only those two /32s,
so nothing routes `44/8` into it, and it has no relationship to the profile in
this repository.

### 2. The node holds no HamNET address

```console
$ ip -4 addr | grep 'inet 44\.'
(no output)
$ ip route get 44.27.132.79
44.27.132.79 via 135.181.17.65 dev enp41s0 src 135.181.17.116
```

The node treats `44.27.132.79` as a remote host reachable over the public
internet — not as an address of its own. Until `44.27.132.79/32` exists on an
interface, no HamNET packet can be delivered to this machine at all.

### 3. Web routing would 404 even once the address exists

This is the gap that is easiest to miss, so it was tested rather than assumed.
Requests were sent to the public address with the Host header a HamNET client
would produce:

```
Host: bbs.ea7klk.es   :443  ->  HTTP 303 (login)         works
Host: 44.27.132.79    :443  ->  HTTP 404 Not Found       no router matches
Host: 44.27.132.79    :80   ->  HTTP 301 -> https://44.27.132.79/
                                then a certificate failure, since ACME cannot
                                issue for a bare IP
```

There is exactly one router for this service, keyed on the hostname. A client
browsing to `44.27.132.79` matches nothing. HTTPS by IP is not available at all
with the current certificate story.

### 4. SSH needs nothing but the address

The BBS pod already publishes `hostPort: 2222`, and it was confirmed answering
on the public address. A `hostPort` binds every address on the node, so the
moment `44.27.132.79` exists, `ssh -p 2222 bbs@44.27.132.79` works with **no
code, manifest or service change**.

## The constraint that decides the topology

The example profile ends with:

```
AllowedIPs = 0.0.0.0/0, ::/0
```

That is a **full-tunnel default route**. Applied to this node it would break the
public side, which is the requirement that must not be traded away:

- **Return path.** A public SYN arrives on `enp41s0`; with the default route
  moved into the tunnel, the reply leaves via `wg`, so SSH and the web would
  appear to hang for internet users — connections accepted, answers never
  arriving.
- **Egress.** APRS-IS, ACME renewals and image pulls would all leave through the
  HamNET gateway, coupling this deployment's availability to that path.
- **MTU.** MTU 1380 against a 1500 host and CNI path needs MSS clamping, or
  large frames are silently dropped — which looks like "SSH connects, then
  hangs".

So the tunnel must carry `44/8` and leave the default route alone.

## Proposed implementation

### Tunnel: a second interface, not a replacement

Add `hamnet0` alongside `wg-easy`, which keeps its two clients untouched.

- Peer `AllowedIPs = 44.0.0.0/8` — only HamNET traffic enters the tunnel.
- Route `44.0.0.0/8 dev hamnet0`, **leaving the default route on `enp41s0`**.
- If the HamNET side insists on advertising a default route, use policy routing
  (an `ip rule` for the `44.x` source and its own table) rather than replacing
  the main table. That keeps both worlds working with one interface.
- MSS clamping on `hamnet0` (`iptables -t mangle -A FORWARD -o hamnet0 -p tcp
  --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu`).

Where it runs: a small privileged pod with `hostNetwork` and `NET_ADMIN`, config
from a Secret, so the tunnel is described in GitOps next to everything else.
A systemd `wg-quick` unit on the host is simpler but would sit outside Fleet and
outside review.

Prerequisites: `NET_ADMIN` on the node (already granted to wg-easy, so the
pattern exists) and the `wireguard` module, which is loaded
(`CONFIG_WIREGUARD=m`, present in `lsmod`).

### Web: a second router, HTTP-only, restricted to HamNET

One new `IngressRoute` in `ham-bbs`:

- matches `Host(\`44.27.132.79\`)` on the **HTTP** entrypoint, to
  `bbs-web:8080`;
- sets `traefik.ingress.kubernetes.io/router.middlewares` to an `IPAllowList` of
  `44.0.0.0/8`, so the plaintext path exists only on HamNET;
- must **exclude** the http→https redirect middleware that applies to the public
  router, or the client is bounced to a certificate it cannot validate.

The public router (`Host(\`bbs.ea7klk.es\`)`, `websecure`, ACME) is untouched.
Both then serve the same service from one deployment.

TLS on HamNET is a separate decision. Paying for it needs a certificate with an
`IP:44.27.132.79` subject alternative name, or a HamNET-resolvable DNS name added
to the existing certificate — either is a change to issuance, not routing.

### SSH: nothing

`hostPort: 2222` already answers on every node address.

### What must not change

- The default route stays on `enp41s0`.
- No `AllowedIPs = 0.0.0.0/0` on this node.
- The public Traefik router, its ACME certificate and the `bbs.ea7klk.es` DNS
  records are untouched.

## Risks and checks

| Risk | Mitigation |
| --- | --- |
| Public access breaks when the tunnel comes up | Default route untouched; test SSH and HTTPS from outside before and after |
| Only one address family works | `44.27.132.79/32` is IPv4; HamNET also assigns `fe80::29b2:e119:a855:66af/128`, which is link-local and not routable — do not rely on it |
| Large transfers hang | MSS clamping on `hamnet0`; test with a multi-megabyte transfer, not just a ping |
| Web router leaks the app publicly | `IPAllowList 44.0.0.0/8` on the HTTP router, verified from a public address (must 404) |
| Route flaps and takes the phone line down | Keepalive 20 on the peer; the tunnel is additive, so a flap costs only HamNET reachability |
| Fleet drift | The tunnel pod and route live in `spainip/infra`; a hand-made `ip route` would be reverted and should not be used as the fix |

Verification once implemented, all from outside the node:

1. `ssh -p 2222 bbs@44.27.132.79` reaches the login screen.
2. `http://44.27.132.79/` serves the web BBS (a login redirect, not a 404).
3. `https://bbs.ea7klk.es/` and `ssh -p 2222 bbs@135.181.17.116` still work.
4. `traceroute 44.27.132.79` from a public host still shows the internet path.
5. `ip route show default` still names `enp41s0`.

## Open questions for the operator

1. **The private key** for `44.27.132.79` — the example has a placeholder. It
   belongs in a Secret, never in this repository.
2. **Is `44.27.227.1:44000` still the current HamNET gateway**, and does it
   require the routed block `44.27.25.64/28` to be announced from our side?
3. **Where should the tunnel live** — privileged pod in GitOps, or a host unit
   outside it?
4. **Does HamNET need to reach a hostname rather than an address?** If so, which
   name, and can it be added to the existing certificate as a SAN?
