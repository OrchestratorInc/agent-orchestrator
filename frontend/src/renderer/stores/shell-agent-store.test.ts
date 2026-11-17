import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../lib/api-client";
import { useShellAgentStore, useShellAgentStream } from "./shell-agent-store";

const entry = (state: "working" | "idle" | "waiting") => ({ handleId: "shellterm-1", harness: "claude-code", state });

afterEach(() => {
	cleanup();
	setApiBaseUrl(null);
	vi.useRealTimers();
	vi.unstubAllGlobals();
	useShellAgentStore.setState({ agents: {}, unseenDone: {} });
});

describe("shell agent store", () => {
	it("marks an agent done when a turn finishes, until the tab is seen", () => {
		const { replace, markSeen } = useShellAgentStore.getState();
		replace([entry("working")]);
		expect(useShellAgentStore.getState().unseenDone).toEqual({});
		replace([entry("idle")]);
		expect(useShellAgentStore.getState().unseenDone).toEqual({ "shellterm-1": true });
		markSeen("shellterm-1");
		expect(useShellAgentStore.getState().unseenDone).toEqual({});
	});

	it("does not mark an agent that was already idle", () => {
		useShellAgentStore.getState().replace([entry("idle")]);
		expect(useShellAgentStore.getState().unseenDone).toEqual({});
	});

	it("keeps the done mark across a stream reconnect", () => {
		const { replace, clearAgents } = useShellAgentStore.getState();
		replace([entry("working")]);
		replace([entry("idle")]);
		clearAgents();
		expect(useShellAgentStore.getState().agents).toEqual({});
		replace([entry("idle")]);
		expect(useShellAgentStore.getState().unseenDone).toEqual({ "shellterm-1": true });
	});

	it("drops the done mark when the agent exits", () => {
		const { replace } = useShellAgentStore.getState();
		replace([entry("working")]);
		replace([entry("idle")]);
		replace([]);
		expect(useShellAgentStore.getState().unseenDone).toEqual({});
	});
});

class FakeSocket {
	static instances: FakeSocket[] = [];
	onopen: (() => void) | null = null;
	onclose: (() => void) | null = null;
	onmessage: ((event: MessageEvent) => void) | null = null;
	send = vi.fn();
	close = vi.fn();
	constructor(public url: string) { FakeSocket.instances.push(this); }
	message(frame: unknown) {
		act(() => this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(frame) })));
	}
}

function socket() {
	const latest = FakeSocket.instances.at(-1);
	if (!latest) throw new Error("expected a status socket");
	return latest;
}

describe("shell agent connection", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		FakeSocket.instances = [];
		vi.stubGlobal("WebSocket", FakeSocket);
		setApiBaseUrl("http://127.0.0.1:3001");
	});

	it("shares a socket until the last tab closes and cancels retries", () => {
		const first = renderHook(() => useShellAgentStream(true));
		const second = renderHook(() => useShellAgentStream(true));
		expect(FakeSocket.instances).toHaveLength(1);
		socket().onopen?.();
		expect(socket().send).toHaveBeenCalledWith(JSON.stringify({ ch: "shell-agents", type: "subscribe" }));
		first.unmount();
		expect(socket().close).not.toHaveBeenCalled();
		act(() => socket().onclose?.());
		second.unmount();
		act(() => vi.advanceTimersByTime(5_000));
		expect(FakeSocket.instances).toHaveLength(1);
	});

	it("closes the socket and clears status when the preview is disabled", () => {
		const { rerender } = renderHook(({ enabled }) => useShellAgentStream(enabled), { initialProps: { enabled: true } });
		const activeSocket = socket();
		activeSocket.message({ ch: "shell-agents", type: "snapshot", agents: [entry("working")] });
		rerender({ enabled: false });
		expect(activeSocket.close).toHaveBeenCalledOnce();
		expect(useShellAgentStore.getState().agents).toEqual({});
		act(() => vi.advanceTimersByTime(5_000));
		expect(FakeSocket.instances).toHaveLength(1);
	});

	it("clears status and retries after a network close", () => {
		renderHook(() => useShellAgentStream(true));
		socket().message({ ch: "shell-agents", type: "snapshot", agents: [entry("working")] });
		act(() => socket().onclose?.());
		expect(useShellAgentStore.getState().agents).toEqual({});
		act(() => vi.advanceTimersByTime(5_000));
		expect(FakeSocket.instances).toHaveLength(2);
	});

	it.each([null, "http://127.0.0.1:4001"])("clears stale status when the daemon URL becomes %s", (url) => {
		renderHook(() => useShellAgentStream(true));
		const old = socket();
		old.message({ ch: "shell-agents", type: "snapshot", agents: [entry("working")] });
		act(() => setApiBaseUrl(url));
		expect(old.close).toHaveBeenCalledOnce();
		expect(useShellAgentStore.getState().agents).toEqual({});
		expect(FakeSocket.instances).toHaveLength(url ? 2 : 1);
	});

	it("ignores late messages and opens from the old socket", () => {
		renderHook(() => useShellAgentStream(true));
		const old = socket();
		act(() => setApiBaseUrl("http://127.0.0.1:4001"));
		socket().message({ ch: "shell-agents", type: "snapshot", agents: [] });
		old.message({ ch: "shell-agents", type: "snapshot", agents: [entry("working")] });
		old.onopen?.();
		expect(old.send).not.toHaveBeenCalled();
		expect(useShellAgentStore.getState().agents).toEqual({});
	});

	it("ignores unknown and malformed frames without replacing good status", () => {
		renderHook(() => useShellAgentStream(true));
		socket().message({ ch: "shell-agents", type: "snapshot", agents: [entry("working")] });
		for (const frame of [
			{ ch: "terminal", type: "snapshot", agents: [] },
			{ ch: "shell-agents", type: "error" },
			{ ch: "shell-agents", type: "snapshot", agents: [entry("idle"), null] },
		]) {
			socket().message(frame);
			expect(useShellAgentStore.getState().agents["shellterm-1"]?.state).toBe("working");
		}
	});
});
