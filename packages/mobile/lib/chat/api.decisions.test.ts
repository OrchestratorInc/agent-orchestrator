import { beforeEach, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ authHeaders: () => ({}), httpBase: () => "http://host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
import { getConversationPage } from "./api";
import type { ServerConfig } from "../config";

const config = {} as ServerConfig;
beforeEach(() => request.mockReset());

// The approval card picks its primary action by kind, so the parser keeps it.
it("keeps each approval decision's kind", async () => {
	const decisions = [
		{ id: "reject", kind: "reject_once", label: "Deny" },
		{ id: "allow", kind: "allow_once", label: "Allow Once" },
		{ id: "plain", label: "No kind" },
		{ id: "odd", kind: 3, label: "Not a string" },
	];
	request.mockResolvedValueOnce({ json: async () => ({ controller: "ready", latestSequence: 1, activities: [{ id: "a1", activityKind: "approval", status: "pending", requestId: "r1", detail: { decisions } }] }) });
	const snapshot = await getConversationPage(config, "session");
	expect(snapshot.items[0]).toMatchObject({ kind: "activity", decisions: [
		{ id: "reject", kind: "reject_once", label: "Deny" },
		{ id: "allow", kind: "allow_once", label: "Allow Once" },
		{ id: "plain", label: "No kind" },
		{ id: "odd", label: "Not a string" },
	] });
	const parsed = (snapshot.items[0] as { decisions?: Array<Record<string, unknown>> }).decisions ?? [];
	expect("kind" in parsed[2]).toBe(false);
	expect("kind" in parsed[3]).toBe(false);
});
