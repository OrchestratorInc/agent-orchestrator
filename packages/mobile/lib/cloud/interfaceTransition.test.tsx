import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionInterfaceTransition } from "../chat/api";
import { useInterfaceTransitionWith } from "../session/useInterfaceTransition";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import { cloudInterfaceTransitionBackend, cloudSwitchNoticeShown, cloudTerminalBusy, useCloudSwitchEnded } from "./interfaceTransition";
import { interfaceModeName, switchNotice, switchPhaseLabel, switchPhaseShortLabel } from "./interfaceSwitchCopy";

vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) } }));
vi.mock("../telemetry/runtime", () => ({ trackFeature: (_name: string, run: () => unknown) => run() }));
// A machine's backend reads the paired machine's config, which needs the phone's keychain.
vi.mock("../session/interfaceTransitionBackend", () => ({ daemonInterfaceTransitionBackend: () => null }));

const transition = (phase: SessionInterfaceTransition["phase"], extra: Partial<SessionInterfaceTransition> = {}): SessionInterfaceTransition => ({
	id: "t-1", sessionId: "s1", sourceMode: "chat", targetMode: "tui", policy: "drain", phase, createdAt: "2026-11-09T10:00:00Z", updatedAt: "2026-11-09T10:00:01Z", ...extra,
});

describe("a cloud session's switch between Chat and Terminal UI", () => {
	it("asks Cloud, and says its refusals in Cloud's words, never a machine's", async () => {
		const client = {
			getInterfaceTransition: vi.fn(async () => ({ supported: true, targetMode: "tui" })),
			startInterfaceTransition: vi.fn(async () => ({ transition: transition("requested") })),
			cancelInterfaceTransition: vi.fn(async () => ({ ok: true })),
			acknowledgeInterfaceTransitionNotice: vi.fn(async () => ({ ok: true })),
		};
		const backend = cloudInterfaceTransitionBackend(client as unknown as CloudClient, "org");
		// Terminal UI starts on the chat's model and effort; Cloud refuses them for a switch to Chat.
		expect(await backend.start("s1", "tui", "drain", { model: "claude-opus-5-5", reasoningEffort: "high" })).toMatchObject({ id: "t-1", phase: "requested" });
		expect(client.startInterfaceTransition).toHaveBeenLastCalledWith("org", "s1", { targetMode: "tui", policy: "drain", model: "claude-opus-5-5", reasoningEffort: "high" });
		await backend.start("s1", "chat", "interrupt", { model: "claude-opus-5-5" });
		expect(client.startInterfaceTransition).toHaveBeenLastCalledWith("org", "s1", { targetMode: "chat", policy: "interrupt" });
		expect(await backend.acknowledgeNotice("s1", "t-1")).toBeUndefined();
		// A 401 from Cloud is a sign-in to renew, not "rejected this phone's password".
		const refused = backend.describe(new CloudError("Your session expired. Sign in again.", { status: 401, code: "UNAUTHORIZED" }));
		expect(refused.status).toBe(401);
		expect(refused.message).not.toMatch(/password/);
	});

	// Seen on the Pixel: a drain from an idle Cloud terminal sat at "finishing
	// current work", since Cloud cannot see a terminal's turn end. The desktop
	// stops an idle terminal directly and asks only when it is at work.
	it("stops an idle terminal directly, and asks first when it is at work", () => {
		expect(cloudTerminalBusy({ activityState: "idle", status: "ready" })).toBe(false);
		expect(cloudTerminalBusy({ activityState: "active", status: "ready" })).toBe(true);
		expect(cloudTerminalBusy({ activityState: "waiting_input", status: "ready" })).toBe(true);
		expect(cloudTerminalBusy({ activityState: "idle", status: "working" })).toBe(true);
		expect(cloudSwitchNoticeShown({ sourceMode: "tui", targetMode: "chat", errorCode: "SOURCE_DRAIN_FAILED" })).toBe(false);
		expect(cloudSwitchNoticeShown({ sourceMode: "chat", targetMode: "tui", errorCode: "SOURCE_DRAIN_FAILED" })).toBe(true);
		expect(cloudSwitchNoticeShown(undefined)).toBe(true);
	});

	it("reads each phase in the words a machine's Chat and Terminal screens use", () => {
		expect(interfaceModeName("tui")).toBe("Terminal UI");
		expect(switchPhaseShortLabel("draining")).toBe("finishing current work");
		expect(switchPhaseLabel("tui", "source_stopping")).toBe("Stopping the Chat controller before the terminal starts.");
		expect(switchPhaseLabel("chat", "source_stopping")).toBe("Stopping the terminal controller before Chat starts.");
		expect(switchPhaseLabel("chat", "completed")).toBe("Chat is ready. Opening it now.");
		expect(switchPhaseLabel("tui", undefined)).toBe("Checking that the terminal can resume this agent's native conversation.");
	});

	it("keeps a failed switch's notice until it is dismissed", () => {
		expect(switchNotice(transition("failed"))).toEqual({ text: "The interface switch failed; the current interface remains available.", recovered: false });
		expect(switchNotice(transition("recovery_required", { errorCode: "DAEMON_RESTARTED" }))?.recovered).toBe(true);
		expect(switchNotice(transition("failed", { errorDetail: "The terminal could not resume." }))?.text).toBe("The terminal could not resume.");
		expect(switchNotice(transition("failed", { noticeAcknowledgedAt: "2026-11-09T10:01:00Z" }))).toBeUndefined();
		expect(switchNotice(transition("completed"))).toBeUndefined();
	});
});

describe("the switch hook over Cloud", () => {
	let renderer: ReactTestRenderer | undefined;
	let hook: ReturnType<typeof useInterfaceTransitionWith>;
	function Probe({ client }: { client: CloudClient }) {
		hook = useInterfaceTransitionWith(cloudInterfaceTransitionBackend(client, "org"), "s1");
		return null;
	}
	const settle = () => act(async () => { for (let tick = 0; tick < 4; tick++) await new Promise((resolve) => setTimeout(resolve, 0)); });
	beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
	afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

	// Cloud answers a dismissal with only "ok": the notice clears once the status is read again.
	it("reads the status again after dismissing a notice", async () => {
		const status = { supported: true, targetMode: "tui", transition: transition("failed") };
		const client = {
			getInterfaceTransition: vi.fn(async () => status),
			acknowledgeInterfaceTransitionNotice: vi.fn(async () => {
				status.transition = transition("failed", { noticeAcknowledgedAt: "2026-11-09T10:01:00Z" });
				return { ok: true };
			}),
		} as unknown as CloudClient;
		act(() => { renderer = create(<Probe client={client} />); });
		await settle();
		expect(switchNotice(hook.transition)).toBeDefined();
		await act(async () => { await hook.acknowledgeNotice("t-1"); });
		expect(switchNotice(hook.transition)).toBeUndefined();
	});
});

// Seen on the Pixel: a finished switch vanished from Cloud's status, and the
// screen sat on the old interface until the session's next poll.
it("reads the session again once a watched switch is no longer under way", () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const ended = vi.fn();
	function Watch({ id }: { id?: string }) { useCloudSwitchEnded(id, ended); return null; }
	let renderer: ReactTestRenderer | undefined;
	act(() => { renderer = create(<Watch />); });
	expect(ended).not.toHaveBeenCalled();
	act(() => renderer!.update(<Watch id="t-1" />));
	act(() => renderer!.update(<Watch id="t-1" />));
	expect(ended).not.toHaveBeenCalled();
	act(() => renderer!.update(<Watch />));
	expect(ended).toHaveBeenCalledTimes(1);
	act(() => renderer!.update(<Watch />));
	expect(ended).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	vi.unstubAllGlobals();
});
