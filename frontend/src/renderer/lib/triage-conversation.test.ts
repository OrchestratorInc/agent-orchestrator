import { expect, it } from "vitest";
import type { TriageRun } from "../../shared/triage";
import { triageConversationSnapshot, triagePendingActivity } from "./triage-conversation";

const run: TriageRun = { id: "report", version: 1, createdAt: "2026-10-19T10:00:00Z", updatedAt: "2026-10-19T10:01:00Z",
	request: { harness: "codex", model: "selected", effort: "high", publishAuthorized: false }, state: "running", activity: "Reading diagnostics", transport: "chat",
	messages: [{ id: "human", role: "user", text: "Blank window", at: "2026-10-19T10:00:00Z" },
		{ id: "agent", role: "assistant", text: "Checking the startup report.", at: "2026-10-19T10:00:10Z" }] };

it("adapts fallback messages to the normal Chat workspace without inventing native turns", () => {
	const snapshot = triageConversationSnapshot(run);
	expect(snapshot).toMatchObject({ sessionId: "triage:report", harness: "codex", mode: "chat", turns: [], capabilities: [],
		settings: { model: "selected", reasoningEffort: "high" }, controller: { state: "busy" } });
	expect(snapshot.items.map((item) => item.id)).toEqual(["human", "agent", "triage:report:activity"]);
	expect(snapshot.items[0]).toMatchObject({ role: "user", origin: "human", text: "Blank window" });
});

it("renders helper notices as system activities and completed runs as ready", () => {
	const snapshot = triageConversationSnapshot({ ...run, state: "completed", messages: [{ id: "notice", role: "system", text: "Finished", at: run.updatedAt }] });
	expect(snapshot.items[0]).toMatchObject({ kind: "activity", activityKind: "system", summary: "Finished" });
	expect(snapshot.controller).toEqual({ state: "ready" });
});

it.each(["approval", "user_input"] as const)("preserves native %s detail for the existing cards and dock", (kind) => {
	const detail = { requestId: "input", schema: { type: "object", properties: { answer: { type: "string" } } } };
	const activity = triagePendingActivity({ ...run, pendingInput: { id: "input", kind, summary: "Continue?", detail } });
	expect(activity).toMatchObject({ activityKind: kind, status: "pending", requestId: "input", summary: "Continue?" });
	expect(activity?.detail).toEqual(detail);
});
