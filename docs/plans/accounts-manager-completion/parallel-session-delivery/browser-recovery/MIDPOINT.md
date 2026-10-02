# Browser recovery midpoint review

Failed-first evidence: `red.log` has two assertion failures on the original source, one for each sign-in mode. The original source plus changed regressions are archived in `red-source.tar.gz`, SHA256 `cd632e35a7af2a8a8136e5f23341f483ebac4f8790ad31faa5794dea191bcdaa`.

The accepted operation stays in component memory until inventory observes it. The same operation can reopen or copy its HTTPS link. A failed external opener is not an authorization failure and no longer sends cancellation. Explicit cancellation remains a bodyless acknowledgement, separate from observed status. Terminal observations retire the transient link even when large numeric revisions round equally.

Self-review found another race: an old browser handoff finishing after cancellation could clear the busy flag of a newer sign-in. `late-handoff-red.log` reproduces the enabled start button during the newer request. Completion and error publication are now gated by the form generation, and closing the form releases its own busy state. A late start response does not replace the current operation before checking the form generation.

Negative controls cover unsafe schemes, URL credentials, unusable expiry, clipboard failure, repeated clicks, expired links, observed completion/failure/expiry and pruning. No link is written to storage or logs. Manual copying requires a click. Missing or unsafe start URLs still trigger cancellation rather than being offered for use.

The first full component run exposed an incomplete old fixture that supplied only an ID and URL. It now supplies the real response's required provider, mode, status, device code and expiration fields. Its acknowledgement-versus-status and saved-credential assertions remain unchanged. A syntax error in the rerender helper was corrected; `midpoint.log` is a failed diagnostic, not passing evidence.

No backend, native switching, Subscriptions, guest, vault, runtime or generated contract is modified by this slice. Final full checks and real desktop observation follow this review. Native browser handoff is not proof of provider authorization, and a copied URL is not a saved account.
