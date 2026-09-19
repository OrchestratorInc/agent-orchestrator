import { describe, expect, it, vi } from "vitest";
import { createCloudSessionSource } from "./source";

function fullSession(id: string) {
	return {
		id,
		orgId: "o1",
		projectId: "p1",
		kind: "worker",
		harness: "claude-code",
		displayName: `session ${id}`,
		branch: "main",
		mode: "trusted",
		deniedCommands: [],
		activityState: "idle",
		status: "building",
		runtimeConnected: true,
		isTerminated: false,
		createdAt: "2026-09-01T00:00:00Z",
		updatedAt: "2026-09-01T00:00:00Z",
	};
}

function clientStub(overrides: Record<string, unknown> = {}) {
	return {
		listProjects: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		listSessions: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		createSession: vi.fn(async () => ({ session: { id: "s-new" } })),
		deleteSession: vi.fn(async () => ({ session: { id: "x", desiredState: "deleted" } })),
		...overrides,
	};
}

describe("createCloudSessionSource", () => {
	it("identifies as the cloud environment", () => {
		expect(createCloudSessionSource({ client: clientStub() as never, orgId: "o1" }).kind).toBe("cloud");
	});

	it("lists projects for the bound org and maps them", async () => {
		const client = clientStub({
			listProjects: vi.fn(async () => ({
				items: [{
					id: "p1", orgId: "o1", displayName: "web",
					repositoryUrl: "https://github.com/acme/web", defaultBranch: "main",
					config: {}, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
				}],
				page: { hasMore: false },
			})),
		});
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await source.listProjects()).toEqual([{ id: "p1", name: "web", kind: "single_repo" }]);
		expect(client.listProjects).toHaveBeenCalledWith("o1", expect.anything());
	});

	// A phone must not stop at page one and silently hide half the board.
	it("follows pagination cursors to the end", async () => {
		const pages = [
			{ items: [fullSession("a")], page: { hasMore: true, nextCursor: "c2" } },
			{ items: [fullSession("b")], page: { hasMore: false } },
		];
		let call = 0;
		const client = clientStub({ listSessions: vi.fn(async () => pages[call++]) });
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		const sessions = await source.listSessions();
		expect(sessions.map((s) => s.id)).toEqual(["a", "b"]);
		expect(client.listSessions).toHaveBeenCalledTimes(2);
	});

	it("spawns a worker session in the bound org, idempotently", async () => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		expect(await source.createSession({ projectId: "p1", prompt: "fix it", harness: "codex" }))
			.toEqual({ id: "s-new" });
		expect(client.createSession).toHaveBeenCalledWith("o1", expect.objectContaining({
			projectId: "p1", kind: "worker", harness: "codex", prompt: "fix it",
		}), expect.objectContaining({ idempotencyKey: expect.any(String) }));
	});

	it("deletes a session in the bound org", async () => {
		const client = clientStub();
		const source = createCloudSessionSource({ client: client as never, orgId: "o1" });
		await source.deleteSession("s1");
		expect(client.deleteSession).toHaveBeenCalledWith("o1", "s1");
	});

	it("throws naming the task that will implement unbuilt methods", async () => {
		const source = createCloudSessionSource({ client: clientStub() as never, orgId: "o1" });
		await expect(source.getConversationPage("s1")).rejects.toThrow(/Task 12/);
		await expect(source.sendMessage("s1", { text: "hi" } as never)).rejects.toThrow(/Task 13/);
		await expect(source.cancelTurn("s1", "t1")).rejects.toThrow(/Task 13/);
		expect(() => source.subscribeEvents("s1", () => {})).toThrow(/Task 12/);
	});
});
