import { beforeEach, expect, it, vi } from "vitest";
import { readSideChatDraft, saveSideChatDraft } from "./sideChatDraftWriter";
const { put, get } = vi.hoisted(() => ({ put: vi.fn(), get: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: { PUT: put, GET: get } }));
beforeEach(() => {
	vi.clearAllMocks();
	get.mockResolvedValue({ data: { contentJson: "receipt" } });
	put.mockResolvedValue({});
});
it("orders an old autosave, send receipt and remount read", async () => {
	let finish!: (value: object) => void;
	put.mockImplementationOnce(
		() =>
			new Promise((resolve) => {
				finish = resolve;
			}),
	);
	const old = saveSideChatDraft("session", "side", "old draft");
	await vi.waitFor(() => expect(put).toHaveBeenCalledTimes(1));
	const receipt = saveSideChatDraft("session", "side", "receipt");
	const remount = readSideChatDraft("session", "side");
	await Promise.resolve();
	expect(put).toHaveBeenCalledTimes(1);
	expect(get).not.toHaveBeenCalled();
	finish({});
	await Promise.all([old, receipt, remount]);
	expect(put.mock.calls.map(([, options]) => options.body.contentJson)).toEqual(["old draft", "receipt"]);
	expect(get.mock.invocationCallOrder[0]).toBeGreaterThan(put.mock.invocationCallOrder[1]);
});
it("continues after a failed write and keeps different sides independent", async () => {
	let fail!: (error: Error) => void;
	put.mockImplementationOnce(
		() =>
			new Promise((_, reject) => {
				fail = reject;
			}),
	);
	const old = saveSideChatDraft("session", "blocked", "old");
	const rejected = expect(old).rejects.toThrow("lost");
	await vi.waitFor(() => expect(put).toHaveBeenCalledTimes(1));
	const receipt = saveSideChatDraft("session", "blocked", "receipt");
	await saveSideChatDraft("session", "other", "independent");
	expect(put.mock.calls[1][1].body.contentJson).toBe("independent");
	fail(new Error("lost"));
	await rejected;
	await receipt;
	expect(put.mock.calls[2][1].body.contentJson).toBe("receipt");
});
