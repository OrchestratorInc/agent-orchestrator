import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SESSION_HOLD_LIMIT_MS, SESSION_WORKSPACE_ID, useHeldSession } from "./useHeldSession";

const { heldMock } = vi.hoisted(() => ({ heldMock: vi.fn() }));
vi.mock("./useSessionSwitchSnap", async (importOriginal) => ({
	...await importOriginal<typeof import("./useSessionSwitchSnap")>(),
	setSessionSwitchHeld: heldMock,
}));

type Route = { sessionId?: string; projectId?: string; pending?: boolean };
type Shown = ReturnType<typeof useHeldSession>;

let workspace: HTMLDivElement;
let composer: HTMLTextAreaElement;
let inertChanges: ("set" | "removed")[];
let observer: MutationObserver;

function renderHost(initial: Route) {
	const renders: Shown[] = [];
	const hook = renderHook(
		({ sessionId, projectId, pending }: Route) => {
			const shown = useHeldSession(sessionId, projectId, pending);
			renders.push(shown);
			return shown;
		},
		{ initialProps: initial },
	);
	const go = (route: Route) => {
		const from = renders.length;
		act(() => hook.rerender(route));
		return renders.slice(from);
	};
	return { ...hook, go, renders };
}

const inertChange = (record: MutationRecord) => (record.oldValue === null ? "set" : "removed");

function recordedInert() {
	inertChanges.push(...observer.takeRecords().map(inertChange));
	return inertChanges;
}

beforeEach(() => {
	heldMock.mockReset();
	workspace = document.createElement("div");
	workspace.id = SESSION_WORKSPACE_ID;
	composer = document.createElement("textarea");
	workspace.append(composer);
	document.body.append(workspace);
	inertChanges = [];
	// Record each toggle as it happens: the held commit is followed by the
	// destination commit inside the same act().
	observer = new MutationObserver((records) => {
		inertChanges.push(...records.map(inertChange));
	});
	observer.observe(workspace, { attributes: true, attributeFilter: ["inert"], attributeOldValue: true });
});

afterEach(() => {
	observer.disconnect();
	workspace.remove();
});

describe("useHeldSession", () => {
	it("holds the session on screen, inert and unfocused, until the destination renders", () => {
		const host = renderHost({ sessionId: "a", projectId: "p1" });
		expect(host.result.current).toEqual({ sessionId: "a", projectId: "p1", switching: false });
		composer.focus();
		expect(document.activeElement).toBe(composer);

		const renders = host.go({ sessionId: "b", projectId: "p2" });

		// The held render keeps the old session and its project together.
		expect(renders).toContainEqual({ sessionId: "a", projectId: "p1", switching: true });
		expect(renders.at(-1)).toEqual({ sessionId: "b", projectId: "p2", switching: false });
		// A keyboard switch leaves focus in the old composer; it must not keep it.
		expect(document.activeElement).not.toBe(composer);
		expect(recordedInert()).toEqual(["set", "removed"]);
		expect(workspace).not.toHaveAttribute("inert");
		expect(heldMock.mock.calls.map(([held]) => held)).toEqual([false, true, false]);
	});

	it("leaves focus alone outside the held workspace", () => {
		const host = renderHost({ sessionId: "a" });
		const sidebarRow = document.createElement("button");
		document.body.append(sidebarRow);
		sidebarRow.focus();

		host.go({ sessionId: "b" });

		expect(document.activeElement).toBe(sidebarRow);
		sidebarRow.remove();
	});

	it("renders a session directly when entering from another route", () => {
		const host = renderHost({ sessionId: "a" });
		expect(host.go({})).toContainEqual({ sessionId: undefined, projectId: undefined, switching: false });

		const renders = host.go({ sessionId: "b" });

		expect(renders.some((shown) => shown.sessionId === "a" || shown.switching)).toBe(false);
		expect(renders.at(-1)).toEqual({ sessionId: "b", projectId: undefined, switching: false });
		expect(recordedInert()).toEqual([]);
	});

	it("settles on the last of several quick switches with nothing left inert", () => {
		const host = renderHost({ sessionId: "a" });
		host.go({ sessionId: "b" });
		const renders = host.go({ sessionId: "c" });

		expect(renders.at(-1)).toEqual({ sessionId: "c", projectId: undefined, switching: false });
		expect(workspace).not.toHaveAttribute("inert");
		expect(heldMock).toHaveBeenLastCalledWith(false);
	});

	it("keeps the session on screen while the destination's history is on its first read", () => {
		const host = renderHost({ sessionId: "a", projectId: "p1" });

		const renders = host.go({ sessionId: "b", projectId: "p2", pending: true });

		expect(renders.at(-1)).toEqual({ sessionId: "a", projectId: "p1", switching: true });
		expect(workspace).toHaveAttribute("inert");

		// The read lands: the destination renders with its history in one go.
		const landed = host.go({ sessionId: "b", projectId: "p2", pending: false });

		expect(landed.at(-1)).toEqual({ sessionId: "b", projectId: "p2", switching: false });
		expect(workspace).not.toHaveAttribute("inert");
		expect(heldMock).toHaveBeenLastCalledWith(false);
	});

	it("renders the destination with its loading state once a slow read passes the hold limit", () => {
		vi.useFakeTimers();
		try {
			const host = renderHost({ sessionId: "a" });
			host.go({ sessionId: "b", pending: true });
			expect(host.result.current.sessionId).toBe("a");

			act(() => vi.advanceTimersByTime(SESSION_HOLD_LIMIT_MS));

			expect(host.result.current).toEqual({ sessionId: "b", projectId: undefined, switching: false });
			// The read landing later changes nothing.
			expect(host.go({ sessionId: "b", pending: false }).at(-1)).toEqual({ sessionId: "b", projectId: undefined, switching: false });
		} finally {
			vi.useRealTimers();
		}
	});

	it("goes straight back to the session on screen while the destination is still reading", () => {
		const host = renderHost({ sessionId: "a" });
		host.go({ sessionId: "b", pending: true });

		// Back to a: its own history is not pending.
		const renders = host.go({ sessionId: "a", pending: false });

		expect(renders.every((shown) => shown.sessionId === "a")).toBe(true);
		expect(renders.at(-1)).toEqual({ sessionId: "a", projectId: undefined, switching: false });
		expect(workspace).not.toHaveAttribute("inert");
	});

	it("does not hold for a pending read when entering from another route", () => {
		const host = renderHost({ sessionId: "a" });
		host.go({});

		const renders = host.go({ sessionId: "b", pending: true });

		expect(renders.some((shown) => shown.switching)).toBe(false);
		expect(renders.at(-1)).toEqual({ sessionId: "b", projectId: undefined, switching: false });
	});

	it("releases the hold when the host unmounts", () => {
		const host = renderHost({ sessionId: "a" });
		heldMock.mockClear();
		host.unmount();
		expect(heldMock).toHaveBeenLastCalledWith(false);
	});
});
