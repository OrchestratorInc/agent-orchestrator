# Stay Awake: controls deferred to a future PR

PR2 exposes standalone desktop awake sessions, automatic activation, lid/display
wake, battery/heat protection, and automatic Low Power Mode. The following
advanced controls are deliberately deferred. Backend/native foundations remain
internal: PR2 rejects non-default deferred settings even on capable hosts, clears
and persists older saved opt-ins before starting, and reports these capabilities
as unavailable. The browser-action route is not mounted or present in the public
API/client. Internal foundation tests do not authorize external activation.

| Future controls | Existing foundation | Work and acceptance for the next PR |
| --- | --- | --- |
| Display, keyboard and audio | Native effect journal; capability-gated display/keyboard drivers; output-identity-aware mute restoration | Add explicit opt-ins and supported-device explanations. Validate same-device restoration, manual changes, reopening, output switching and crash windows. Screen-lock authentication must remain intact. Private display/keyboard APIs currently support macOS 26–27 only. |
| Pause selected apps | Selected bundle IDs; PID/start identity journal; foreground/system/AO/agent/terminal protections | Add app selection and clear pause/resume status. Validate recording/meeting exclusions, protected descendants, pre-stopped apps, PID reuse, partial failures and explicit recovery of uncertain pauses. |
| Saved Wi-Fi / hotspot recovery | Saved-profile-only native association; outage debounce and bounded retry budget | Add saved-network selection, explicit opt-in, permission/error handling and durable outcome display. Never copy or store Wi-Fi passwords. Verify user network changes cancel recovery. |
| Guarded agent reconnection | Confirmed interrupted Chat transport; reconnect-only request; one durable attempt per controller generation | Add opt-in and outcome controls. Verify explicit stop, hibernation, ownership/interface change and unknown transport states exclude recovery. Never manufacture prompts, restore worktrees or start replacement conversations. |
| Supported browser actions | Confirmed request IDs; signed Chrome/Brave checks; graceful relaunch | Add supported browser selection and a separate confirmation explaining window closure and browser-owned tab restoration. Exclude AO/agent browser profiles. An uncertain action must never auto-replay. |
| General recovery and history | Session history, extension/end operations and native recovery request | Add active-source/history management, extension, clear-history and recovery controls. Keep action replay protections when history is cleared. Preserve unresolved journals and provide actionable recovery states. |

Low Power Mode has its own narrowly scoped recovery control in PR2; the general
recovery/history UI above is still deferred. A follow-up must deliberately
remove the service scope guard, add explicit opt-ins, and restore the browser
API operation together with its controller/client if needed. Each should use the
existing daemon boundary, discover actual host capabilities, and receive independent native
acceptance tests before release.

The physical test handoff for current PR2 code is separate:
[Stay Awake physical acceptance](stay-awake-physical-tests.md).
