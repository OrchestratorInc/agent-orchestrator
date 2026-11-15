import { describe, expect, it } from "vitest";
import type { CloudCpClientEvent } from "../../lib/cloud-cp";
import { renderChat as render } from "../../test/render-chat";
import type { WorkspaceSession } from "../../types/workspace";
import { ChatWorkspace } from "./ChatWorkspace";
import { toSnapshot } from "./CloudSessionChatSurface";

const session = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "project",
	title: "Orchestrator",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	runtimeConnected: true,
	updatedAt: "2026-11-08T10:00:00Z",
	prs: [],
} satisfies WorkspaceSession;

function history() {
	const events: CloudCpClientEvent[] = [];
	const at = () => new Date(Date.UTC(2026, 10, 8, 10, 0, 0) + events.length * 1000).toISOString();
	const push = (type: string, payload: Record<string, unknown>) =>
		events.push({ sessionId: session.id, sequence: events.length + 1, type, payload, createdAt: at() });
	return {
		events,
		prompt: (turn: string) => push("chat.user_message", { text: `P:${turn}`, turnId: turn }),
		started: (turn: string) => push("chat.turn_started", { turnId: turn }),
		reply: (turn: string, item: string) => push("chat.assistant_delta", { text: `A:${turn}.${item}`, turnId: turn, itemId: item }),
		completed: (turn: string) => push("chat.turn_completed", { turnId: turn }),
	};
}

function fanIn(reports: number) {
	const h = history();
	h.prompt("r0");
	h.started("r0");
	h.reply("r0", "a");
	for (let report = 1; report <= reports; report++) {
		h.prompt(`r${report}`);
		h.reply(`r${report - 1}`, "b");
		h.completed(`r${report - 1}`);
		h.started(`r${report}`);
		h.reply(`r${report}`, "a");
	}
	h.reply(`r${reports}`, "b");
	h.completed(`r${reports}`);
	return h.events;
}

function from(events: CloudCpClientEvent[], text: string) {
	return events.slice(events.findIndex((event) => (event.payload as { text?: string }).text === text));
}

function shown(events: CloudCpClientEvent[], partial: boolean) {
	const view = render(<ChatWorkspace snapshot={toSnapshot(session, events, undefined, { partial })} session={session} />);
	const texts = Array.from(document.querySelectorAll("[data-chat-message-id]"))
		.map((element) => /[PA]:(?:r\d|long|next|x|y|z)(?:\.[abc])?/.exec(element.textContent ?? "")?.[0])
		.filter((text): text is string => Boolean(text));
	view.unmount();
	return texts;
}

describe("a Cloud chat read back from its newest events", () => {
	it("reads a full history as it always did", () => {
		const events = fanIn(3);
		expect(toSnapshot(session, events).turns.some((turn) => turn.anchorSequence !== undefined)).toBe(false);
		expect(shown(events, false)).toEqual(["P:r0", "A:r0.b", "P:r1", "A:r1.b", "P:r2", "A:r2.b", "P:r3", "A:r3.b"]);
	});

	it("shows the end of a turn whose prompt is above the window before the report that queued inside it", () => {
		const window = from(fanIn(3), "P:r2");
		expect(shown(window, true)).toEqual(["A:r1.b", "P:r2", "A:r2.b", "P:r3", "A:r3.b"]);
	});

	it("keeps that order as the older page brings the turn's prompt in", () => {
		const events = fanIn(3);
		expect(shown(from(events, "P:r2"), true)).toEqual(["A:r1.b", "P:r2", "A:r2.b", "P:r3", "A:r3.b"]);
		expect(shown(from(events, "P:r1"), true)).toEqual(["A:r0.b", "P:r1", "A:r1.b", "P:r2", "A:r2.b", "P:r3", "A:r3.b"]);
		expect(shown(events, false)).toEqual(["P:r0", "A:r0.b", "P:r1", "A:r1.b", "P:r2", "A:r2.b", "P:r3", "A:r3.b"]);
	});

	it("keeps a turn's group, and the reader's place on it, when its prompt loads above it", () => {
		const events = fanIn(3);
		const view = render(<ChatWorkspace snapshot={toSnapshot(session, from(events, "P:r2"), undefined, { partial: true })} session={session} />);
		const group = () => document.querySelector('[data-chat-turn="turn:r1"]');
		const before = group();
		expect(before?.textContent).toContain("A:r1.b");
		expect(before?.textContent).not.toContain("P:r1");
		view.rerender(<ChatWorkspace snapshot={toSnapshot(session, from(events, "P:r1"), undefined, { partial: true })} session={session} />);
		expect(group()).toBe(before);
		expect(group()?.textContent).toContain("P:r1");
		view.unmount();
	});

	it("orders a window that starts inside a turn longer than the control plane's cap", () => {
		const h = history();
		h.prompt("long");
		h.started("long");
		h.reply("long", "a");
		h.prompt("next");
		h.reply("long", "b");
		h.reply("long", "c");
		h.completed("long");
		h.started("next");
		h.reply("next", "a");
		h.completed("next");
		const cutAt = (text: string) => h.events.slice(h.events.findIndex((event) => (event.payload as { text?: string }).text === text));
		expect(shown(cutAt("A:long.a"), true)).toEqual(["A:long.c", "P:next", "A:next.a"]);
		expect(shown(cutAt("P:next"), true)).toEqual(["A:long.c", "P:next", "A:next.a"]);
	});

	it("keeps several turns begun before the window in the order they began", () => {
		const h = history();
		h.prompt("x");
		h.prompt("y");
		h.started("x");
		h.reply("x", "a");
		h.completed("x");
		h.started("y");
		h.reply("y", "a");
		h.prompt("z");
		h.reply("y", "b");
		h.completed("y");
		h.started("z");
		h.reply("z", "a");
		h.completed("z");
		const window = h.events.slice(h.events.findIndex((event) => (event.payload as { text?: string }).text === "A:x.a"));
		const snapshot = toSnapshot(session, window, undefined, { partial: true });
		const anchors = Object.fromEntries(snapshot.turns.map((turn) => [turn.id, turn.anchorSequence]));
		expect(anchors.x).toBeLessThan(anchors.y!);
		expect(anchors.y).toBeLessThan(window[0].sequence);
		expect(anchors.z).toBeUndefined();
		expect(shown(window, true)).toEqual(["A:x.a", "A:y.b", "P:z", "A:z.a"]);
	});
});
