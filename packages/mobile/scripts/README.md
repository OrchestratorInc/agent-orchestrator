# Connecting a physical phone (LAN bridge)

> **Note:** `ao-phone-proxy.js` is **superseded** by the built-in Connect Mobile feature (Settings → Mobile in the desktop app). This script is retained for reference only and is no longer the recommended approach.

The AO daemon binds to **localhost only** (`127.0.0.1:3001`) by design — it has no
auth, so it never exposes itself to the network. That means a **physical phone**
(a separate device on your Wi-Fi) can't reach it directly.

`ao-phone-proxy.js` is a tiny bridge that fixes this **without weakening the
daemon**: it opens **one** LAN port, forwards it to the loopback daemon, and uses
**trust-on-first-connect** - the first device that connects is pinned as the
_only_ allowed device; every other machine on the Wi-Fi is refused.

## Run it

From the repo root (Node is the only requirement):

```bash
node packages/mobile/scripts/ao-phone-proxy.js
```

You'll see:

```
AO phone bridge: 0.0.0.0:3011 -> 127.0.0.1:3001  | waiting for first device (trust-on-first-connect)
```

## Connect the phone

1. Make sure the phone and the computer are on the **same Wi-Fi**.
2. Find the computer's LAN IP: `ipconfig getifaddr en0` (macOS).
3. In the AO app's **Settings**:
   - **Host:** that LAN IP (e.g. `192.168.1.84`)
   - **API Port:** `3011`
   - **Use TLS:** off
4. Open the app. The bridge logs `[paired] <phone-ip> is now the only allowed
device` - the phone is now the single trusted device. Done.

## Re-pair a different phone

```bash
RESET=1 node packages/mobile/scripts/ao-phone-proxy.js
```

Then connect the new phone (it becomes the pinned device).

## Options

| Env      | Default                  | Meaning                                                  |
| -------- | ------------------------ | -------------------------------------------------------- |
| `PORT`   | `3011`                   | LAN port to expose to the phone                          |
| `TARGET` | `3001`                   | Loopback daemon port to forward to                       |
| `STATE`  | `~/.ao/phone-allow.json` | Where the paired-device IP is remembered                 |
| `RESET`  | -                        | `RESET=1` clears the pairing, then pairs the next device |

## Notes

- **Keep the daemon on its default localhost bind** - don't set `AO_HOST`. This
  bridge is the only thing exposed to the LAN.
- **DHCP drift:** if the phone's IP changes, its new IP won't match the pin and
  it'll be blocked - `RESET=1` and reconnect, or set a **DHCP reservation** for
  the phone in your router so its IP is fixed.
- **Trust model:** whoever connects _first_ is trusted, and IP allowlisting is a
  lightweight LAN control (a hostile device on the same Wi-Fi could spoof the
  paired IP). Fine for a trusted home network; for shared/untrusted Wi-Fi use
  Tailscale or real auth instead.
