import { beforeEach, expect, it, vi } from "vitest";
import type { ClientEvent } from "@aoagents/cloud-client";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(async () => null), setItem: vi.fn(), removeItem: vi.fn() } }));
import { createCloudSessionSource } from "./source";
import { cloudConversationReplayCache } from "./conversationReplay";

beforeEach(() => cloudConversationReplayCache.clear());
it.each([
	["archived", true, "stopped", "stopped"],
	["archived with retained running intent", true, "running", "running"],
	["paused", false, "paused", "stopped"],
])("keeps the Setup failure and output in %s history when no failure checklist owns it", async (_label, isTerminated, desiredState, observedState) => {
	const events: ClientEvent[] = [{
		sessionId: "s1", sequence: 4, type: "workspace.setup", createdAt: "now",
		payload: { status: "failed", step: 2, exitCode: 3, durationMs: 500, output: "Setup failed output tail" },
	}];
	const session = { id: "s1", orgId: "org", projectId: "project", harness: "codex", interfaceMode: "chat", mode: "trusted", runtimeConnected: false, isTerminated, desiredState, observedState, createdAt: "now", updatedAt: "now", startupError: { code: "project_setup_failed", message: "Setup failed", at: "now" } };
	const client = { getSession: vi.fn(async () => ({ session })), replayEvents: vi.fn(async () => ({ events, hasMore: false, nextAfter: 4 })) };
	const source = createCloudSessionSource({ client: client as never, orgId: "org", scopeKey: "setup-history", isCurrent: () => true });
	const history = await source.getConversationPage("s1");
	expect(history.items).toMatchObject([{
		kind: "activity", activityKind: "system", sequence: 4, status: "failed", summary: "Setup failed",
		detail: { event: "workspace.setup", setupStatus: "failed", setupStep: 2, exitCode: 3, output: "Setup failed output tail" },
	}]);
	expect(history).toMatchObject({ latestSequence: 4, oldestSequence: 4 });
});

it.each([true, false])("uses the durable workspace failure to place Setup history while runtimeConnected is %s", async (runtimeConnected) => {
	const events: ClientEvent[] = [
		{ sessionId: "s1", sequence: 1, type: "workspace.setup", payload: { status: "failed", step: 1, exitCode: 2, durationMs: 500 }, createdAt: "now" },
		{ sessionId: "s1", sequence: 2, type: "workspace.setup", payload: { status: "succeeded", exitCode: 0, durationMs: 1_000 }, createdAt: "now" },
		{ sessionId: "s1", sequence: 3, type: "chat.user_message", payload: { text: "hello" }, createdAt: "now" },
		{ sessionId: "s1", sequence: 4, type: "workspace.setup", payload: { status: "failed", step: 2, exitCode: 3, durationMs: 500 }, createdAt: "now" },
	];
	const session = { id: "s1", orgId: "org", projectId: "project", harness: "codex", interfaceMode: "chat", mode: "trusted", runtimeConnected, isTerminated: false, desiredState: "running", observedState: "running", createdAt: "now", updatedAt: "now", startupError: { code: "project_setup_failed", message: "Setup failed", at: "now" } };
	const client = { getSession: vi.fn(async () => ({ session })), replayEvents: vi.fn(async () => ({ events, hasMore: false, nextAfter: 4 })) };
	const source = createCloudSessionSource({ client: client as never, orgId: "org", scopeKey: "setup-history", isCurrent: () => true });
	const failed = await source.getConversationPage("s1");
	expect(failed.items.map((item) => item.sequence)).toEqual([2, 3]);
	expect(failed).toMatchObject({ latestSequence: 4, oldestSequence: 1 });
	client.getSession.mockResolvedValue({ session: { ...session, startupError: { code: "", message: "", at: "" } } });
	const recovered = await source.getConversationPage("s1");
	expect(recovered.items.map((item) => item.sequence)).toEqual([2, 3, 4]);
	expect(recovered).toMatchObject({ latestSequence: 4, oldestSequence: 1 });
	client.getSession.mockResolvedValue({ session: { ...session, startupError: { code: "project_setup_failed", message: "  ", at: "now" } } });
	expect((await source.getConversationPage("s1")).items.map((item) => item.sequence)).toEqual([2, 3, 4]);
});
