// Package zcodeproto is the hand-written wire vocabulary of `zcode app-server
// --stdio`: method names, request/response/notification shapes, and the error
// codes the server uses.
//
// Hand-written because zcode ships no schema generator (Codex has
// `codex app-server generate-json-schema`; zcode has nothing comparable). Each
// declaration is tagged with where it was established:
//
//   - PROVEN: exercised live during the Phase 1 probes (HANDOFF.md "Phase 1
//     live-probe findings", runs 3-5, 2026-10-19) on the API-key provider route.
//     The redacted NDJSON captures live in probes/captures/; the frames each
//     driver test needs are extracted verbatim into the driver's testdata/
//     fixtures, and every PROVEN row in Methods names the fixture that holds it
//     (MethodInfo.Evidence). The conformance test refuses a PROVEN row with no
//     fixture.
//   - STATIC: read from the bundle's zod schemas and handlers but never
//     exercised. A STATIC shape can be wrong in ways only a live exchange
//     reveals; the driver keeps STATIC behaviour behind fail-closed branches.
//
// Every struct in types.go names its bundle symbol and its provenance in its
// doc comment. Nothing here is a guess: a field that could not be read from
// the bundle or a capture is left out rather than invented.
package zcodeproto

// ProviderVersion is the zcode build the bundle digest and every claim were
// read from (HANDOFF.md, `zcode --version`).
const ProviderVersion = "zcode 0.16.9"

// BundlePath is where the desktop app installs the CLI bundle every claim in
// this package was read from. The conformance test digests the file at this
// path when it exists and skips otherwise.
const BundlePath = "/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs"

// BundleSHA256 is `shasum -a 256` of BundlePath at the time the protocol was
// read. A bundle upgrade shows up as a digest mismatch, which is the cue to
// re-run the Phase 1 captures rather than trust the recorded rows.
const BundleSHA256 = "31965ce1d5d4a808ef156784f67996de42ccab335591d2386a61ea8f6f506a54"
