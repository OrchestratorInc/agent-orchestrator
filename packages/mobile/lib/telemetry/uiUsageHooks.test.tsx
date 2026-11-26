import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Runs the focus callback like a screen that has focus; a re-run on new deps too.
vi.mock("expo-router", async () => {
	const react = await import("react");
	return { useFocusEffect: (callback: () => void) => react.useEffect(callback, [callback]) };
});

import { useToast } from "../useToast";
import type { UiUsageTracker } from "./uiUsage";
import { setUiUsageTracker } from "./uiUsageRegistry";
import { useErrorShown } from "./useErrorShown";
import { useSessionSurface } from "./useSessionSurface";

let renderer: ReactTestRenderer | undefined;
let tracker: { [K in keyof UiUsageTracker]: ReturnType<typeof vi.fn> };

beforeEach(() => {
	tracker = {
		setRoute: vi.fn(), setVariant: vi.fn(), setForeground: vi.fn(), sessionOpened: vi.fn(), openedBy: vi.fn(),
		errorShown: vi.fn(), restore: vi.fn(), setEnabled: vi.fn(), current: vi.fn(),
	};
	setUiUsageTracker(tracker as unknown as UiUsageTracker);
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	setUiUsageTracker(null);
});

describe("useToast", () => {
	it("reports a warning toast as an error shown, never its text", () => {
		let toast!: ReturnType<typeof useToast>;
		function Probe() {
			toast = useToast();
			return null;
		}
		act(() => { renderer = create(<Probe />); });
		act(() => toast.show("Path copied."));
		expect(tracker.errorShown).not.toHaveBeenCalled();
		act(() => toast.show("Couldn't merge acme/private#12", { tone: "warning" }));
		expect(tracker.errorShown).toHaveBeenCalledWith("toast", "action_failed");
	});
});

describe("useErrorShown", () => {
	function Probe({ kind }: { kind: "unreachable" | "auth" | null }) {
		useErrorShown(kind);
		return null;
	}

	it("reports a shown error, and again when its kind changes", () => {
		act(() => { renderer = create(<Probe kind={null} />); });
		expect(tracker.errorShown).not.toHaveBeenCalled();
		act(() => renderer!.update(<Probe kind="unreachable" />));
		act(() => renderer!.update(<Probe kind="unreachable" />));
		act(() => renderer!.update(<Probe kind="auth" />));
		expect(tracker.errorShown.mock.calls).toEqual([["screen", "unreachable"], ["screen", "auth"]]);
	});
});

describe("useSessionSurface", () => {
	function Probe({ form, id = "s1" }: { form: "chat" | "tui" | null; id?: string }) {
		useSessionSurface(form, `h|${id}`);
		return null;
	}

	it("names the screen and counts one opening across a mode switch", () => {
		act(() => { renderer = create(<Probe form={null} />); });
		expect(tracker.sessionOpened).not.toHaveBeenCalled();
		act(() => renderer!.update(<Probe form="chat" />));
		expect(tracker.setVariant).toHaveBeenLastCalledWith("session", "session_chat");
		act(() => renderer!.update(<Probe form="tui" />));
		expect(tracker.setVariant).toHaveBeenLastCalledWith("session", "session_tui");
		expect(tracker.sessionOpened.mock.calls).toEqual([["h|s1"]]);
		// The same route showing another session is a new opening.
		act(() => renderer!.update(<Probe form="tui" id="s2" />));
		expect(tracker.sessionOpened.mock.calls).toEqual([["h|s1"], ["h|s2"]]);
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(tracker.setVariant).toHaveBeenLastCalledWith("session", null);
	});
});

describe("reportOpenedBy", () => {
	// expo-router reads the launch link before telemetry starts.
	it("hands a source that arrived before the tracker to it when it registers", async () => {
		const { reportOpenedBy } = await import("./uiUsageRegistry");
		setUiUsageTracker(null);
		reportOpenedBy("deep_link", "s9", () => 1_000);
		setUiUsageTracker(tracker as unknown as UiUsageTracker, () => 2_000);
		expect(tracker.openedBy).toHaveBeenCalledWith("deep_link", "s9");
	});

	it("drops a source that waited too long", async () => {
		const { reportOpenedBy } = await import("./uiUsageRegistry");
		setUiUsageTracker(null);
		reportOpenedBy("deep_link", "s9", () => 1_000);
		setUiUsageTracker(tracker as unknown as UiUsageTracker, () => 60_000);
		expect(tracker.openedBy).not.toHaveBeenCalled();
	});
});

describe("sessionIdInPath", () => {
	it("reads the session id from a route or a link, and nothing else", async () => {
		const { sessionIdInPath } = await import("./uiUsageRegistry");
		expect(sessionIdInPath("/session/web-1?hostId=a")).toBe("web-1");
		expect(sessionIdInPath("aomobile://session/worker%2F7")).toBe("worker/7");
		expect(sessionIdInPath("aomobile:///session/abc#x")).toBe("abc");
		for (const path of ["aomobile:///", "/files/web-1", "/sessions", "aomobile://pair?code=x"]) expect(sessionIdInPath(path)).toBeUndefined();
	});
});
