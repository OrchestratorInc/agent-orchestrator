import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SESSION_SWITCHING_ATTR, useSessionSwitchSnap } from "./useSessionSwitchSnap";

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
});
