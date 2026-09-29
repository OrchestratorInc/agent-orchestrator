# Isolated desktop integration verification

Scope: W6 local desktop behavior on HEAD ec7efde0652f21690d8e98e2dcfa71d5b132e422 plus the sealed 58-file integrated correction. No containment adoption, live-provider claim, native-platform claim or publication.

1. Preserve the integrated, protected, generated and guest manifests. Create a separate detached checkout from the current HEAD and copy the exact local delta as an archived snapshot. Record all source hashes before building. Use a genuine dependency installation in that checkout.
2. Build the daemon and account runner from that snapshot. Launch the real Electron application in the foreground, with an empty private home, separate data/profile/run file and a loopback daemon. Keep the user's home and existing desktop instances untouched.
3. Exercise the real provider catalog, empty inventory, enablement, login choices, wrong-method rejection, request-ID display, initial native/managed choices, unavailable managed execution and separate device-login status. Do not insert simulated accounts into the app or use reconstructed screens as evidence.
4. Restart the owned application and recheck durable local settings and empty-account state. Inspect screenshots from the actual Electron page and a short recording. Record untestable positive quota, A/B and removal flows explicitly.
5. Review each discovered defect against the public contract. Reproduce introduced defects before correcting only the affected UI boundary. Run focused tests, typecheck and the affected full package, then repeat the desktop scenario. Preserve independent backend review seals.

Evidence root: `/var/tmp/pr-5769-desktop-final-79.yrWkBr`.

The desktop-development skill determines launch isolation and real-app evidence. The gate discipline keeps negative local checks separate from actual-account and native-platform acceptance. All prior archives remain immutable.
