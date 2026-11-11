# Brand mark

Every Orchestrator icon, logo and mascot image comes from two sources. Change
a source, rerun the script, and commit the outputs. Never edit an output by
hand.

| Source | What it holds |
| --- | --- |
| The `Ruto` section of [`scripts/generate-brand-assets.swift`](../../scripts/generate-brand-assets.swift) | Ruto, the mark, as drawn on the desktop icon before Liquid Glass and traced onto its 1024 grid: the outline, eye holes and baton, the face gradient, and the colours of the chamfered rims and eye walls. The script builds the rims from the outline and writes the result to [`ruto.svg`](ruto.svg), with the parts in two groups, `baton` and `body`. |
| [`AO.icon/icon.json`](AO.icon/icon.json) | The Liquid Glass settings, an Apple Icon Composer document: the dark gradient tile, Ruto and the baton as separate glass groups with specular highlights and shadows, and where they sit on the tile. Open `AO.icon` in Icon Composer to tune it; its `Assets/` layers are written by the script. |

```sh
swift scripts/generate-brand-assets.swift
```

Needs macOS 26 or later with Xcode 26 or later: it renders with Icon Composer's
`ictool`, compiles with `actool` and packs with `iconutil`. Runs in a few
seconds and writes the same bytes every time. `frontend/assets/Assets.car` is
the exception: `actool` output changes on every compile, so it is only rebuilt
when the bundle changes, tracked by `Assets.car.sha256`.

## Outputs

| File | Used by |
| --- | --- |
| `ruto.svg` | The whole mark on the 1024 grid, the vector source of truth for everything below. |
| `AO.icon/Assets/{ruto,baton}.svg` | The Icon Composer layers. |
| `frontend/assets/Assets.car` | macOS 26+ app icon (Liquid Glass, light, dark, tinted, clear), named by `CFBundleIconName` in `frontend/forge.config.ts`. |
| `frontend/assets/icon.icns` | App icon before macOS 26, the dmg volume icon and the dictation helper. The glass tile rendered at the 824 pt macOS icon body with the standard drop shadow, 16 to 1024 px. |
| `frontend/assets/icon.png`, `icon.ico` | Linux packages and window icon, Windows installer and executable. |
| `frontend/assets/icon-dev.png` | Dock icon of unpackaged (dev) runs, with the DEV badge. |
| `frontend/assets/ao-mascot.svg` | App UI: sidebar brand, startup loader, first run. Padded so Ruto fills 80% of the slot. |
| `frontend/assets/ao-logo.svg`, `assets/ao-logo.svg` | Docs Open Graph images; the copy the website mirrors. Tight square crop. |
| `frontend/assets/tray*Template*.png` | Menu bar template icons, drawn by `frontend/scripts/generate-awake-tray-icon.swift` from a silhouette the script passes it. |
| `frontend/src/renderer/assets/ao-qr-logo.png` | Centre of the Connect Mobile QR code. |
| `frontend/src/browser-extension/icons/icon-*.png` | AO Browser Bridge extension. |
| `docs/assets/readme/logo.png` | Repository README header (192 px for a 96 pt slot). |
| `packages/mobile/assets/AO.icon` | iOS icon (Expo builds it from the bundle). |
| `packages/mobile/assets/icon.png` | Expo fallback icon: full-bleed square, no rounding, no alpha. |
| `packages/mobile/assets/android-icon-{background,foreground,monochrome}.png` | Android adaptive icon. The foreground and monochrome layers are scaled so Ruto stays inside the 66 dp safe zone. |
| `packages/mobile/assets/notification-icon.png` | Android notification icon (white silhouette). |
| `packages/mobile/assets/splash-icon.png`, `favicon.png` | Splash screen and web favicon. |
| `packages/mobile/assets/mascot.png` | Onboarding, chat empty state and connection lamp: the flat artwork, like `ao-mascot.svg`, so it reads on light themes. |

To see the iOS icon in every appearance, as a device draws it, run
`packages/mobile/scripts/preview-icon.sh`.

Glass renders that need no tile (Android foreground, splash)
come from rendering the bundle on a black and a white tile and solving for
each pixel's alpha, so they carry the same glass shading as the app icon.

## Outside this repository

These still show the previous artwork and need a manual update from the
outputs above:

- orchestrator.inc: `/ao-logo.svg` (docs header logo) and `/favicon.svg` (docs
  favicon), from `assets/ao-logo.svg`.
- The X and GitHub organization avatars
  ([`assets/social/ruto-twitter-avatar.png`](../social/ruto-twitter-avatar.png)
  is a separate, hand-made upload asset).
- App Store and Google Play listing icons and screenshots, which come from the
  next mobile build.
