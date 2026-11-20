---
name: ao-mobile-dev
description: "Run, show, or check this checkout's mobile app on a connected iPhone. Use whenever asked to run the mobile app on a phone or verify a mobile change on a device: npm run device reuses a cached Debug build per native fingerprint and serves the JS from Metro, so most branches are on the phone in seconds."
---

# AO Mobile Dev

Put the current checkout's mobile app on the user's iPhone with one command. This is
the default way to run the app on a device; do not reach for `npx expo run:ios` or a
Release build unless the user asks for one or the task is about native release behavior
(store builds, OTA updates, Local Network prompts in Release).

## Run

From the repository root:

```bash
cd packages/mobile
npm run device
```

Run it in the background: a new native fingerprint means a cold Xcode build of several
minutes. When the fingerprint matches a cached build it finishes in seconds. It prints
`ready in N s` with the device, branch, whether native was cached or rebuilt, and the
Metro URL. Report those to the user. Metro's own log (including the first
`iOS Bundled ...ms` line once the phone loads the app) is at the printed path.

What it does, so you can reason about failures:

1. `node_modules`: cloned from `~/.ao/dev/mobile-device/node_modules/<lockfile key>`, or
   `npm ci` and stored.
2. Native: Expo's iOS fingerprint (`@expo/fingerprint`, the app's runtime version) keys a
   cached Debug `Orchestrator.app`. A miss runs `expo prebuild --clean`, `pod install`
   and `xcodebuild` with `SKIP_BUNDLING=1`.
3. Install with `devicectl` only when the fingerprint or the Mac's LAN IP changed (an IP
   change rewrites `ip.txt` in a copy and re-signs it).
4. Metro on `:8081` in `tmux -L ao-mobile-device`, then launch the app.

Flags: `--rebuild`, `--reinstall`, `--no-launch`. Env: `AO_MOBILE_UDID`,
`AO_MOBILE_TEAM`, `AO_MOBILE_BUNDLE_ID`, `AO_MOBILE_KEEP_PUSH=1`.

## Errors

- **No reachable iPhone / unavailable:** ask the user to unlock the phone and connect it.
- **Several iPhones reachable:** ask which one, then set `AO_MOBILE_UDID`.
- **Free profile allows 3 apps:** the error lists the bundle ids using the slots. Ask the
  user which one to remove before uninstalling anything.
- **Port 8081 held by another process:** ask before stopping it.
- **`waiting for another run to finish`:** another checkout is building or installing; let it
  finish. Runs take turns because they share the build cache and the phone.
- **`replacing Metro serving <checkout>`:** the phone now loads this checkout's JS, not the
  other one's. Mention it to the user if that checkout belongs to another session.
- **`launch failed; reinstalling`:** the app was probably deleted from the phone; the script
  reinstalls and retries once.
- **`expo prebuild changed tracked files`:** report `git status`; do not revert.
- **Signing errors on a first build:** `-allowProvisioningUpdates` normally registers the
  bundle id; otherwise report the xcodebuild error.

## After it runs

The app is **AO Dev**, separate from any store build, and pairs with the desktop on its
own the first time, through its in-app QR scanner. It registers dev-only URL schemes
(`aomobile-dev`, `ao-app-dev`), so the pairing QR scanned with the Camera opens the store
app, not AO Dev. iOS asks to allow Local Network access on first launch; it needs that
to reach Metro. Do not take screenshots of the phone or drive it unless asked.

## Stop

Only when the user asks: `npm run device -- stop` (Metro). `npm run device -- status`
shows what is installed and whether Metro runs.
