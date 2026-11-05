import { afterEach, describe, expect, it } from "vitest";
import { useShellAgentStore } from "./shell-agent-store";

const entry = (state: "working" | "idle" | "waiting") => ({
	handleId: "shellterm-1",
	harness: "claude-code",
	state,
	since: "2026-11-05T00:00:00Z",
});

afterEach(() => useShellAgentStore.setState({ agents: {}, unseenDone: {} }));

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
