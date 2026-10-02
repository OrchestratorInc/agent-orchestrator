# Same-account switch evidence

Actual isolated Electron captures from `e26734ebec4183bab9e2a53907039851dd102f1a`, using synthetic account labels, the real daemon, native preload and provider catalog. No live provider authentication was exercised and the recorded UI interaction submitted no switch request.

The current account is absent from the target menu. A managed current account retains native credentials as an enabled alternate. With a native current account, an eligible managed account is offered; when none is eligible, the menu and submit action are disabled and the neutral empty-state message is shown.

- [Managed current, native alternate](managed-current-native-alternate.png)
- [Native alternate selected](managed-current-native-selected.png)
- [Native current, managed alternate](native-current-managed-alternate.png)
- [Native current, no alternatives](native-current-no-alternatives.png)
- [12-second interaction recording](same-target-interaction.mp4)

The four screenshots were visually inspected for synthetic labels and private data. The recording is H.264, 950 by 1038, twelve seconds; its bytes match the independently reviewed owner seal. File hashes are in SHA256SUMS.

The backend successor `1993c37ed5ae91c21d1a428bd757947fdb59005d` preserves every frontend byte from the capture source and closes the input-admission defect found in that parent's backend. The final main reconciliation preserves the account-control source bytes. Its separate cloud-session loading-indicator changes are not shown here. These captures demonstrate UI presentation and selection only; the held-input no-op guarantee is established by the successor's race tests, not this recording.

Permanent removal, native-platform runtime acceptance and full live-account workflows remain outside this evidence. No request IDs, credentials or provider diagnostic bodies are included in these public artifacts.
