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

Slice E must use the same birth timestamp as `ProcessStartedAt`: macOS `kern.proc.pid`, converted with `time.Unix(P_starttime.Sec, int64(P_starttime.Usec)*1000).UTC()`. A timestamp taken around process launch or from second-resolution `ps` is not interchangeable.

The supervising daemon calls `Release(ctx, target)` at attempt cleanup and `Close(ctx)` on shutdown, including after a startup error. Release revokes the attempt's Cua session. Close stops only the daemon this instance launched, using its custom socket, PID file, saved birth timestamp and `stop --expected-pid`. Cleanup errors retain the owned PID and paths for diagnosis. Nothing kills arbitrary Cua or Electron processes.

## Storage and listeners

All configured runtime paths resolve under `<supervisorDataDir>/testing/cua/`: `driver.sock`, `driver.pid`, `home/`, `tmp/`, `captures/`, and `daemon.stdout.log` / `daemon.stderr.log`. Socket paths longer than 103 bytes are refused. Captured PNG staging files are removed after reading their original bytes. Evidence persistence belongs to `TestingEvidenceStore`.

Child commands strip every inherited `AO_*` and `CUA_DRIVER_*` variable. Explicit app environment sets `CUA_DRIVER_RS_HOME`, `CUA_DRIVER_TELEMETRY_HOME`, `TMPDIR`, `CUA_DRIVER_RS_UPDATE_CHECK=0`, and `CUA_DRIVER_RS_TELEMETRY_ENABLED=0`. Both HTTP listener variables are omitted. The adapter also refuses configured `CUA_DRIVER_RS_MCP_HTTP_PORT` or `CUA_DRIVER_ENVELOPE_HTTP_PORT` in launchd's environment, which LaunchServices can inherit independently of the caller.

Cua 0.34.0 still reads hard-coded `~/.cua-driver/config.json`; it has no supported override. Its version-cache and direct-capture-proof paths also ignore package-home overrides. Update checks are disabled, and the adapter never runs the permission-proof or Computer History flows that write those fixed paths. OS-managed TCC and LaunchServices metadata cannot be redirected. These are provider limitations, not AO state directories.

## Screenshot receipts and input

`Screenshot` uses `get_window_state` with injected PID/window/session, `max_image_dimension:0`, and an explicit staging path. It validates the PNG's actual dimensions against the native point rectangle and 1×/2× scale, then checks that the window has not moved or resized. The frame carries its original pixel dimensions, scale and capture time.

The service saves the screenshot first, assigns its evidence `ScreenshotID`, and retains the **entire** returned frame, including its JSON-excluded `CaptureHandle`. The handle indexes an in-memory receipt containing Cua's private `capture_id`. The service resolves screenshot IDs within the current attempt before calling this port. Frames reconstructed from worker JSON or durable JSON cannot regain a private receipt.

Every click, type and key requires that current receipt. One admitted input consumes it, even if Cua reports a failure or its effect is uncertain. Take a fresh screenshot before the next input. Changed process birth times, target identities, frame metadata, geometry or expired receipts are refused before input. Invalid coordinates are refused before any Cua call. Window geometry validation uses a read-only PID-scoped `list_windows` before dispatch.

Agent coordinates stay in original screenshot pixels. Cua converts them to native points once. The adapter must not divide by the Retina scale before sending pixel coordinates. Clicks pass the immutable Cua capture ID for its additional atomic target/frame admission. Cua's type/key schemas lack that admission field, so those operations rely on the adapter's fresh receipt and exact target checks as well as Cua's own target guards.

Background is the default. Electron can silently ignore background character/key events while Cua reports delivery. Results report delivery only; screenshots or target observations must establish the application's effect. There is no automatic retry or foreground escalation.

`DeliveryMode: cua.Foreground` explicitly permits exact-target foreground assistance for typing and keys. Clicks remain background. The service must journal this policy before dispatch, and action results include the actual requested mode. Foreground assistance can raise the window, change keyboard focus, and move the physical mouse cursor. It interrupts the user and is unsuitable for a promise of concurrent desktop use. Cua attempts to restore the previous app afterwards; that is provider behavior, not an adapter guarantee.

Foreground screenshots also retain a private AX snapshot. Type resolves exactly one text field at the requested screenshot pixel and sends its fresh element token, so Cua focuses that field inside its foreground interval. If no unique field exists, the adapter refuses input. A following foreground key re-focuses the last addressed typing field using a token from a new screenshot. This prevents Electron window reactivation from clearing renderer focus between type and key. Click clears that remembered typing field. Tokens never enter the worker API or durable screenshot JSON.

Cua can report `type_text_incomplete` even when the full text later appears in the renderer. The adapter propagates that provider error and consumes the receipt. Inspect a fresh screenshot before deciding what, if anything, remains to type. It never suppresses the error or repeats the full input.

## Recording

`StartRecording` and `StopRecording` return an explicit `RecordingResult.Gap`. Cua 0.34.0's MIT recorder captures the main display and has no window/region selector. It cannot be invoked inside this window-only boundary. The service records the gap on the attempt and does not report video evidence.

The smallest candidate alternative is macOS `/usr/sbin/screencapture -v`, whose local help lists `-l<windowid>`, `-R<x,y,w,h>` and `-V<seconds>`. Window-video behavior has not been tested. A CLI invoked from AO can inherit AO's responsible app identity for Screen Recording; it would need permission-identity analysis before any prompt. This adapter does not invoke it. A moving screen region also cannot guarantee window-only pixels if another window overlaps it.

## Checks

Ordinary tests use a fake command runner and do not launch desktop processes. An opt-in macOS test is compiled only with `-tags cua_live`. It requires `CUA_LIVE_ROOT` to name an explicitly created disposable Electron fixture with `fixture-pid.json`; it is not an AO-app launcher. With `CUA_LIVE_EXPECT_EFFECT=1`, the fixture console log must contain the typed marker, Return event and button event, in addition to saved window screenshots.
