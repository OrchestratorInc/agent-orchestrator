import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SESSION_SWITCHING_ATTR, SESSION_WORKSPACE_ID, setSessionSwitchHeld, useSessionSwitchSnap } from "./useSessionSwitchSnap";

const listeners = new Map<string, (event: unknown) => void>();
vi.mock("@tanstack/react-router", () => ({
	useRouter: () => ({
		subscribe: (type: string, listener: (event: unknown) => void) => {
			listeners.set(type, listener);
			return () => listeners.delete(type);
		},
	}),
}));

function navigate(pathname: string) {
	listeners.get("onBeforeNavigate")?.({ pathChanged: true, toLocation: { pathname, href: pathname } });
}

function rendered(pathname: string) {
	listeners.get("onRendered")?.({ toLocation: { pathname, href: pathname } });
}

describe("useSessionSwitchSnap", () => {
	afterEach(() => {
		vi.useRealTimers();
		document.documentElement.removeAttribute(SESSION_SWITCHING_ATTR);
	});

	it.each(["/projects/p1/sessions/s1", "/sessions/s2", "/host/h1/session/s3"])(
		"suppresses chrome transitions for %s until two frames after render",
		(pathname) => {
			const frames: FrameRequestCallback[] = [];
			vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => frames.push(callback));
			renderHook(() => useSessionSwitchSnap());
			navigate(pathname);
			expect(document.documentElement).toHaveAttribute(SESSION_SWITCHING_ATTR);
			rendered(pathname);
			frames.shift()?.(0);
			expect(document.documentElement).toHaveAttribute(SESSION_SWITCHING_ATTR);
			frames.shift()?.(16);
			expect(document.documentElement).not.toHaveAttribute(SESSION_SWITCHING_ATTR);
		},
	);

	it("keeps transitions off until a held destination commits", () => {
		const frames: FrameRequestCallback[] = [];
		vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => frames.push(callback));
		renderHook(() => useSessionSwitchSnap());
		navigate("/sessions/s1");
		setSessionSwitchHeld(true);
		rendered("/sessions/s1");
		frames.shift()?.(0);
		frames.shift()?.(16);
		expect(document.documentElement).toHaveAttribute(SESSION_SWITCHING_ATTR);
		setSessionSwitchHeld(false);
		frames.shift()?.(32);
		expect(document.documentElement).toHaveAttribute(SESSION_SWITCHING_ATTR);
		frames.shift()?.(48);
		expect(document.documentElement).not.toHaveAttribute(SESSION_SWITCHING_ATTR);
	});

	it("keeps the flag through a late render of an earlier switch", () => {
		const frames: FrameRequestCallback[] = [];
		vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => frames.push(callback));
		renderHook(() => useSessionSwitchSnap());
		navigate("/sessions/s1");
		navigate("/sessions/s2");
		rendered("/sessions/s1");
		expect(frames).toHaveLength(0);
		rendered("/sessions/s2");
		frames.shift()?.(0);
		frames.shift()?.(16);
		expect(document.documentElement).not.toHaveAttribute(SESSION_SWITCHING_ATTR);
	});

	it("clears a pending switch when a navigation away from sessions supersedes it", () => {
		renderHook(() => useSessionSwitchSnap());
		navigate("/sessions/s1");
		navigate("/projects/p1");
		expect(document.documentElement).not.toHaveAttribute(SESSION_SWITCHING_ATTR);
	});

	it("leaves non-session navigation animated", () => {
		renderHook(() => useSessionSwitchSnap());
		navigate("/projects/p1");
		expect(document.documentElement).not.toHaveAttribute(SESSION_SWITCHING_ATTR);
	});

	it("clears itself when a navigation never renders", () => {
		vi.useFakeTimers();
		renderHook(() => useSessionSwitchSnap());
		navigate("/sessions/s1");
		vi.advanceTimersByTime(1000);
		expect(document.documentElement).not.toHaveAttribute(SESSION_SWITCHING_ATTR);
	});
	it("moves focus out of the session on screen as soon as a switch starts", () => {
		renderHook(() => useSessionSwitchSnap());
		const workspace = document.createElement("div");
		workspace.id = SESSION_WORKSPACE_ID;
		const composer = document.createElement("textarea");
		workspace.append(composer);
		const outside = document.createElement("input");
		document.body.append(workspace, outside);
		try {
			composer.focus();
			navigate("/projects/p1/sessions/s2");
			expect(document.activeElement).not.toBe(composer);

			// Focus elsewhere, such as the command palette's input, is left alone.
			outside.focus();
			navigate("/projects/p1/sessions/s3");
			expect(document.activeElement).toBe(outside);
		} finally {
			workspace.remove();
			outside.remove();
		}
	});
});
