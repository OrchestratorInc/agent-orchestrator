# Cua desktop adapter

This adapter implements `ports.TestingDesktopControl` for signed Cua Driver 0.34.0 on macOS. It calls the CLI over a private Unix socket. It never starts a Cua MCP server or HTTP listener and never sends desktop-scope input.

The supervising daemon creates one instance with its resolved data directory:

```go
desktop, err := cua.New(cua.Config{
    DataDir: supervisorDataDir,
    AppPath: "/Applications/CuaDriver.app",
    DeliveryMode: cua.Background,
    CaptureTTL: 30 * time.Second,
})
```

`New` starts nothing. `BindWindow` validates the target Electron PID and kernel birth timestamp, then starts Driver through LaunchServices with its own signed app permission identity. It refuses pre-existing socket/PID paths and verifies the app's signature, bundle/team identity and exact version before launch. It accepts one usable layer-zero window or a daemon-specified exact window ID, and refuses ambiguous results. IDs, generations, PID birth times and windows cannot be supplied or changed through worker tool arguments.

Both adapters use the shared `process.StartTime`: macOS `kern.proc.pid`, converted with `time.Unix(P_starttime.Sec, int64(P_starttime.Usec)*1000).UTC()`. A timestamp taken around process launch or from second-resolution `ps` is not interchangeable.

The supervising daemon calls `Release(ctx, target)` at attempt cleanup and `Close(ctx)` on shutdown, including after a startup error. Release revokes the attempt's Cua session. Close stops only the daemon this instance launched, using its custom socket, PID file, saved birth timestamp and `stop --expected-pid`. Cleanup errors retain the owned PID and paths for diagnosis. Nothing kills arbitrary Cua or Electron processes.

## Storage and listeners

All configured runtime paths resolve under `<supervisorDataDir>/testing/cua/`: `driver.sock`, `driver.pid`, `home/`, `tmp/`, `captures/`, and `daemon.stdout.log` / `daemon.stderr.log`. Socket paths longer than 103 bytes are refused. Captured PNG staging files are removed after reading their original bytes. Evidence persistence belongs to `TestingEvidenceStore`.

Child commands strip every inherited `AO_*` and `CUA_DRIVER_*` variable. Explicit app environment sets `CUA_DRIVER_RS_HOME`, `CUA_DRIVER_TELEMETRY_HOME`, `TMPDIR`, `CUA_DRIVER_RS_UPDATE_CHECK=0`, and `CUA_DRIVER_RS_TELEMETRY_ENABLED=0`. Both HTTP listener variables are omitted. The adapter also refuses configured `CUA_DRIVER_RS_MCP_HTTP_PORT` or `CUA_DRIVER_ENVELOPE_HTTP_PORT` in launchd's environment, which LaunchServices can inherit independently of the caller.

Cua 0.34.0 still reads hard-coded `~/.cua-driver/config.json`; it has no supported override. Its version-cache and direct-capture-proof paths also ignore package-home overrides. Update checks are disabled, and the adapter never runs the permission-proof or Computer History flows that write those fixed paths. OS-managed TCC and LaunchServices metadata cannot be redirected. These are provider limitations, not AO state directories.

## Screenshot receipts and input

`Screenshot` uses `get_window_state` with injected PID/window/session, `max_image_dimension:0`, and an explicit staging path. It validates the PNG's actual dimensions against the native point rectangle and 1×/2× scale, then checks that the window has not moved or resized. It returns a PNG with a long edge of at most 1568 pixels and reports that image's width and height. Smaller captures stay unchanged. The frame's scale records the original backing scale. A resized screenshot also carries the original PNG and frame in its JSON-excluded `Original` field for evidence storage.

The service saves the screenshot first, assigns its evidence `ScreenshotID`, and retains the **entire** returned frame, including its JSON-excluded `CaptureHandle`. The handle indexes an in-memory receipt containing Cua's private `capture_id`. The service resolves screenshot IDs within the current attempt before calling this port. Frames reconstructed from worker JSON or durable JSON cannot regain a private receipt.

Every click, type and key requires that current receipt. One admitted input consumes it, even if Cua reports a failure or its effect is uncertain. Take a fresh screenshot before the next input. Changed process birth times, target identities, frame metadata, geometry or expired receipts are refused before input. Invalid coordinates are refused before any Cua call. Window geometry validation uses a read-only PID-scoped `list_windows` before dispatch.

Agent coordinates use the returned image's pixels. The adapter maps each axis onto the original capture once, including the first and last pixels. Cua then converts original capture pixels to native points. The adapter must not divide by the Retina scale. AX field lookup uses the mapped original point too. Clicks pass the immutable Cua capture ID for its additional atomic target/frame admission. Cua's type/key schemas lack that admission field, so those operations rely on the adapter's fresh receipt and exact target checks as well as Cua's own target guards.

Background is the default. Electron can silently ignore background character/key events while Cua reports delivery. Results report delivery only; screenshots or target observations must establish the application's effect. There is no automatic retry or foreground escalation.

`DeliveryMode: cua.Foreground`, configured through `AO_TESTING_DESKTOP_DELIVERY`, permits exact-target foreground assistance for clicks, typing and keys. The service must journal this policy before dispatch, and action results include the actual requested mode. Foreground assistance can raise the window, change keyboard focus, and move the physical mouse cursor. It interrupts the user and is unsuitable for a promise of concurrent desktop use. Cua attempts to restore the previous app afterwards; that is provider behavior, not an adapter guarantee.

Foreground screenshots also retain a private AX snapshot. When exactly one text field resolves at the mapped screenshot pixel, Type sends its fresh element token so Cua focuses that field inside its foreground interval. Otherwise Type uses Cua's pixel-focus path within the same pinned window. Incomplete AX data does not block screenshot-based typing, and no retry occurs after dispatch. A following foreground key re-focuses the last addressed typing field using a token from a new screenshot, refusing if that fresh token is unavailable. This prevents Electron window reactivation from clearing renderer focus between type and key. Click clears that remembered typing field. Tokens never enter the worker API or durable screenshot JSON.

Cua can report `type_text_incomplete` even when the full text later appears in the renderer. The adapter propagates that provider error and consumes the receipt. Inspect a fresh screenshot before deciding what, if anything, remains to type. It never suppresses the error or repeats the full input.

## Recording

`StartRecording(ctx, target, evidenceDir)` launches `/usr/sbin/screencapture -v -o -x -l<ownedWindowID> <attemptPath>`. It records only the exact bound window, without audio or a display fallback. Final movie and diagnostics use a private UUID directory inside the attempt's evidence directory. AO never lists, reads, recovers or deletes Apple's protected ScreenRecordings folder. Apple manages its own staging; the receipt retains StagingCleanup for wire compatibility and records that AO does not inspect external storage.

Start retains the recorder PID and kernel birth timestamp immediately after launch. Cancellation retains cleanup ownership. The built-in recorder provides no first-frame readiness signal, so Start proves process ownership only. Start it before the first test action. Stop validates the finalized movie; an immediate stop can honestly fail with recording_empty. No sleep or minimum-duration wait hides that case.

Stop validates the saved process identity before SIGINT, waits for recorder finalization and process exit, and validates the owned final movie with `/usr/bin/avmediainfo`. Validation requires positive duration, one video track with system decoder support, zero analyzer errors, at least one indexed frame and the original capture dimensions. Recorder failures retain a gap even when a partial movie is playable. A timeout escalates to SIGTERM only after rechecking the exact PID and birth timestamp, and retains a gap even if a file validates. There is no recording-start delay or automatic input replay.

Release stops pending recording, revokes the attempt's Cua session and verifies driver shutdown after its last binding. Close stops every owned recorder and the controller's private driver. Driver shutdown requires a positive missing-process or changed-birth result plus absent PID file and socket; an unavailable process probe cannot establish shutdown. Cleanup errors retain ownership for a later cleanup attempt.

## Checks

Ordinary tests use a fake command runner and do not launch desktop processes. An opt-in macOS test is compiled only with `-tags cua_live`. It requires `CUA_LIVE_ROOT` to name an explicitly created disposable Electron fixture with `fixture-pid.json`; it is not an AO-app launcher. With `CUA_LIVE_EXPECT_EFFECT=1`, the fixture console log must contain the typed marker, Return event and button event, in addition to saved window screenshots.

Always pass `-count=1` for live tests. `CUA_LIVE_STAGE=video` runs open-ended recording and SIGINT stop; `video-close` closes the native fixture before Stop. Both save process receipts, window screenshots and finalized movie receipts. Cached Go test output is not a fresh native proof.
