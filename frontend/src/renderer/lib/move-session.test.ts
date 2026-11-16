import { describe, expect, it } from "vitest";
import { MOVE_PART_BYTES, MoveError, moveSession } from "./move-session";
import { hostsWithRepo } from "./repo-hosts";
import type { WorkspaceSummary } from "../types/workspace";

type Handler = (options: { params?: { path?: Record<string, unknown> }; body?: unknown }) => unknown;

function fakeClient(handlers: Record<string, Handler>) {
	const calls: string[] = [];
	const bodies: Record<string, unknown> = {};
	const call = (method: string) => async (path: string, options: Parameters<Handler>[0] = {}) => {
		const part = options.params?.path?.part;
		calls.push(`${method} ${path}${part === undefined ? "" : ` #${part}`}`);
		bodies[`${method} ${path}`] = options.body;
		const handler = handlers[`${method} ${path}`];
		return handler ? handler(options) : { data: {} };
	};
	return { calls, bodies, client: { GET: call("GET"), POST: call("POST"), PUT: call("PUT") } as never };
}

const transfer = { transferId: "tr1", branch: "feat/x", headSha: "abc", native: { size: 3, sha256: "s", nativeId: "n", sourceCwd: "/a" } };

const received = { data: { session: { id: "web-9" }, warnings: [], resumed: true } };

function hosts(archive = new ArrayBuffer(MOVE_PART_BYTES + 10), receive: Handler = () => received) {
	const source = fakeClient({
		"POST /api/v1/sessions/{sessionId}/transfer/prepare": () => ({ data: { transfer } }),
		"GET /api/v1/sessions/{sessionId}/transfer/{transferId}/native": () => ({ data: archive }),
		"POST /api/v1/sessions/{sessionId}/resume-agent": () => ({ error: { code: "AGENT_NOT_EXITED", message: "agent is running" } }),
	});
	// An empty body is the preflight, which a current daemon refuses as incomplete.
	const target = fakeClient({
		"POST /api/v1/sessions/transfer/receive": (options) =>
			(options.body as { transfer?: unknown }).transfer ? receive(options) : { error: { code: "TRANSFER_INCOMPLETE", message: "incomplete" } },
	});
	const input = {
		source: { client: source.client, hostId: "host-a", sessionId: "web-3", label: "MacBook A" },
		target: { client: target.client, hostId: "host-b", label: "MacBook B", projectId: "web" },
		transferId: "tr1",
	};
	return { source, target, input };
}

describe("moveSession", () => {
	it("prepares, copies the archive in parts, receives, then marks the source moved", async () => {
		const { source, target, input } = hosts();
		const steps: string[] = [];
		const result = await moveSession({ ...input, onStep: (step) => steps.push(step) });

		expect(result).toEqual({ sessionId: "web-9", warnings: [], committed: true, commitError: undefined, transferId: "tr1", destinationLabel: "MacBook B", resumed: true });
		expect(steps).toEqual(["prepare", "copy", "receive", "commit"]);
		expect(source.calls).toEqual([
			"POST /api/v1/sessions/{sessionId}/transfer/prepare",
			"GET /api/v1/sessions/{sessionId}/transfer/{transferId}/native",
			"POST /api/v1/sessions/{sessionId}/transfer/commit",
		]);
		expect(target.calls).toEqual([
			"POST /api/v1/sessions/transfer/receive",
			"PUT /api/v1/sessions/transfer/{transferId}/native/{part} #0",
			"PUT /api/v1/sessions/transfer/{transferId}/native/{part} #1",
			"POST /api/v1/sessions/transfer/receive",
		]);
	});

	it("names this computer by hostname on the other daemon, not as This computer", async () => {
		const { source, input } = hosts(undefined, () => ({ data: { ...received.data, hostLabel: "studio" } }));
		await moveSession({ ...input, source: { ...input.source, hostId: "local", label: "This computer" } });
		expect(source.bodies["POST /api/v1/sessions/{sessionId}/transfer/prepare"]).toMatchObject({ sourceLabel: "" });

		const back = hosts(undefined, () => ({ data: { ...received.data, hostLabel: "studio" } }));
		const result = await moveSession({ ...back.input, target: { ...back.input.target, hostId: "local", label: "This computer" } });
		expect(result.destinationLabel).toBe("studio");
		expect(back.source.bodies["POST /api/v1/sessions/{sessionId}/transfer/commit"]).toMatchObject({ destination: { hostId: "local", label: "studio" } });
	});

	it("cancels the move and resumes the source agent when the destination refuses", async () => {
		const { source, input } = hosts(undefined, () => ({ error: { code: "TRANSFER_REPO_MISMATCH", message: "the project here is a different repository" } }));
		const sourceClient = source.client as unknown as { POST: (path: string, options?: unknown) => Promise<unknown> };
		const post = sourceClient.POST;
		sourceClient.POST = async (path, options) => (path.endsWith("/resume-agent") ? (await post(path, options), { data: { ok: true } }) : post(path, options));
		const error = await moveSession(input).catch((e: unknown) => e);

		expect(error).toBeInstanceOf(MoveError);
		expect((error as MoveError).step).toBe("receive");
		expect((error as MoveError).resumedSource).toBe(true);
		expect(source.calls.slice(-2)).toEqual(["POST /api/v1/sessions/{sessionId}/transfer/cancel", "POST /api/v1/sessions/{sessionId}/resume-agent"]);
		expect(source.bodies["POST /api/v1/sessions/{sessionId}/transfer/cancel"]).toEqual({ transferId: "tr1" });
		expect(source.calls).not.toContain("POST /api/v1/sessions/{sessionId}/transfer/commit");
	});

	it("stops before touching the source when the destination cannot receive", async () => {
		const { source, target, input } = hosts();
		const targetClient = target.client as unknown as { POST: (path: string, options?: unknown) => Promise<unknown> };
		targetClient.POST = async () => ({ error: { code: "NOT_FOUND", message: "Not Found" } });
		const error = (await moveSession(input).catch((e: unknown) => e)) as MoveError;

		expect(error).toBeInstanceOf(MoveError);
		expect(error.message).toContain("cannot receive sessions");
		expect(error.resumedSource).toBe(true);
		expect(source.calls).toEqual([]);
	});

	it("finishes an unfinished earlier move under its own transfer id", async () => {
		const { source, input } = hosts();
		const sourceClient = source.client as unknown as { POST: (path: string, options?: unknown) => Promise<unknown> };
		const post = sourceClient.POST;
		sourceClient.POST = async (path, options) =>
			path.endsWith("/transfer/prepare") ? (await post(path, options), { data: { transfer: { ...transfer, transferId: "tr-earlier" } } }) : post(path, options);
		const result = await moveSession(input);

		expect(result.transferId).toBe("tr-earlier");
		expect(source.bodies["POST /api/v1/sessions/{sessionId}/transfer/commit"]).toMatchObject({ transferId: "tr-earlier" });
	});

	it("asks the source to stop a running turn and reports bytes copied", async () => {
		const { source, input } = hosts();
		const progress: number[] = [];
		await moveSession({ ...input, stopTurn: true, onProgress: (sent) => progress.push(sent) });

		expect(source.bodies["POST /api/v1/sessions/{sessionId}/transfer/prepare"]).toMatchObject({ stopTurn: true });
		expect(progress).toEqual([MOVE_PART_BYTES, MOVE_PART_BYTES + 10]);
	});

	it("reports a move that started from a summary", async () => {
		const { input } = hosts(undefined, () => ({ data: { ...received.data, resumed: false } }));
		expect((await moveSession(input)).resumed).toBe(false);
	});

	it("leaves a busy agent alone when the source refuses to prepare", async () => {
		const { source, input } = hosts();
		const sourceClient = source.client as unknown as { POST: (path: string, options?: unknown) => Promise<unknown> };
		const post = sourceClient.POST;
		sourceClient.POST = async (path, options) => {
			if (path.endsWith("/transfer/prepare")) return { error: { code: "TRANSFER_AGENT_BUSY", message: "the agent is in the middle of a turn" } };
			return post(path, options);
		};
		const error = (await moveSession(input).catch((e: unknown) => e)) as MoveError;

		expect(error).toBeInstanceOf(MoveError);
		expect(error.resumedSource).toBe(true);
		expect(error.message).toContain("middle of a turn");
	});

	it("retries a dropped receive once instead of resuming the source", async () => {
		let attempts = 0;
		const { source, input } = hosts(undefined, () => {
			attempts++;
			if (attempts === 1) throw new TypeError("Failed to fetch");
			return { data: { ...received.data, warnings: ["Could not claim a PR"] } };
		});
		const result = await moveSession(input);

		expect(attempts).toBe(2);
		expect(result.warnings).toEqual(["Could not claim a PR"]);
		expect(source.calls).not.toContain("POST /api/v1/sessions/{sessionId}/resume-agent");
	});

	it("leaves the move pending when the destination stops answering during receive", async () => {
		const { source, input } = hosts(undefined, () => {
			throw new TypeError("Failed to fetch");
		});
		const error = (await moveSession(input).catch((e: unknown) => e)) as MoveError;

		expect(error.message).toBe("MacBook B is not reachable");
		expect(error.code).toBe("RECEIVE_UNCONFIRMED");
		expect(error.transferId).toBe(transfer.transferId);
		expect(error.resumedSource).toBe(false);
		expect(source.calls).not.toContain("POST /api/v1/sessions/{sessionId}/transfer/cancel");
		expect(source.calls).not.toContain("POST /api/v1/sessions/{sessionId}/resume-agent");
	});

	it("names the computer a copy cannot reach and resumes the source", async () => {
		const { source, target, input } = hosts();
		const targetClient = target.client as unknown as { PUT: () => Promise<unknown> };
		targetClient.PUT = () => Promise.reject(new TypeError("Failed to fetch"));
		const error = (await moveSession(input).catch((e: unknown) => e)) as MoveError;

		expect(error.message).toBe("MacBook B is not reachable");
		expect(error.step).toBe("copy");
		expect(source.calls).toContain("POST /api/v1/sessions/{sessionId}/resume-agent");
	});

	it("keeps the move when only marking the source fails", async () => {
		const { source, input } = hosts();
		const sourceClient = source.client as unknown as { POST: (path: string, options?: unknown) => Promise<unknown> };
		const post = sourceClient.POST;
		sourceClient.POST = async (path, options) => {
			if (path.endsWith("/transfer/commit")) return { error: { message: "host offline" } };
			return post(path, options);
		};
		const result = await moveSession(input);

		expect(result.committed).toBe(false);
		expect(result.commitError).toContain("host offline");
		expect(result.sessionId).toBe("web-9");
	});
});

describe("hostsWithRepo", () => {
	const project = (id: string, repoKey?: string, hostId?: string): WorkspaceSummary => ({ id, name: id, path: "", repoKey, hostId, sessions: [] });

	it("lists this computer first, then each remote host once", () => {
		const local = [project("web", "github.com/acme/web"), project("api", "github.com/acme/api")];
		const remote = [project("web", "github.com/acme/web", "a"), project("web-2", "github.com/acme/web", "a"), project("site", "github.com/acme/web", "b")];
		expect(hostsWithRepo(local, remote, "github.com/acme/web")).toEqual([
			{ hostId: "local", projectId: "web", projectName: "web" },
			{ hostId: "a", projectId: "web", projectName: "web" },
			{ hostId: "b", projectId: "site", projectName: "site" },
		]);
	});

	it("matches nothing without a key", () => {
		expect(hostsWithRepo([project("web")], [], undefined)).toEqual([]);
	});
});
