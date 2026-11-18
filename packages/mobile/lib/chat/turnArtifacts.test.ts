import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ authHeaders: () => ({}), httpBase: () => "http://host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
import { splitTurnArtifacts } from "../../../shared/artifacts/turn-artifacts";
import type { SessionArtifact } from "../artifacts";
import type { ServerConfig } from "../config";
import { getConversationPage } from "./api";
import { turnResults, turnsTouchArtifacts } from "./turnArtifactsModel";
import type { ConversationTurn, DiffFile } from "./types";

const SESSION = "ao-12";
const ROOT = `/Users/me/.ao/data/artifacts/${SESSION}`;
const turn = (overrides: Partial<ConversationTurn> = {}): ConversationTurn => ({ id: "t1", state: "completed", requestedAt: "2026-11-12T10:00:00Z", ...overrides });
const file = (path: string, status: DiffFile["status"] = "added"): DiffFile => ({ path, status, additions: 3, deletions: 0 });
const artifact = (path: string): SessionArtifact => ({ path, name: path.split("/").pop() ?? path, kind: "file", size: 10, updatedAt: "2026-11-12T10:00:00Z", registered: true });

beforeEach(() => request.mockReset());

describe("conversation turns", () => {
	it("unwraps the daemon's turn artifacts and keeps only created or edited files", async () => {
		request.mockResolvedValueOnce({ json: async () => ({
			controller: "ready",
			latestSequence: 1,
			turns: [
				turn({ id: "made" }) as object,
				{ ...turn({ id: "wrote" }), artifacts: { files: [{ path: "report.md", status: "added" }, { path: "old.md", status: "deleted" }, { path: 4, status: "added" }] } },
				{ ...turn({ id: "empty" }), artifacts: { files: [] } },
			],
		}) });
		const snapshot = await getConversationPage({} as ServerConfig, SESSION);
		expect(snapshot.turns.map((entry) => entry.artifacts)).toEqual([undefined, [{ path: "report.md", status: "added" }], undefined]);
		expect(snapshot.turns[2]).not.toHaveProperty("artifacts");
	});
});

describe("turnsTouchArtifacts", () => {
	it("reads the artifact list only once a turn changed an artifact", () => {
		expect(turnsTouchArtifacts([turn({ diff: { files: [file("src/app.ts")] } })], SESSION)).toBe(false);
		expect(turnsTouchArtifacts([turn({ artifacts: [{ path: "report.md", status: "added" }] })], SESSION)).toBe(true);
		expect(turnsTouchArtifacts([turn({ diff: { files: [file(`${ROOT}/report.md`)] } })], SESSION)).toBe(true);
		expect(turnsTouchArtifacts([turn({ diff: { files: [file("/Users/me/.ao/data/artifacts/other/report.md")] } })], SESSION)).toBe(false);
		expect(turnsTouchArtifacts([turn({ artifacts: [{ path: "report.md", status: "added" }] })], "")).toBe(false);
	});
});

describe("splitTurnArtifacts on the phone", () => {
	it("moves listed deliverables out of the changed files and keeps the rest", () => {
		const diff = [file("src/app.ts", "modified"), file(`${ROOT}/report.md`), file(`${ROOT}/scratch/log.txt`)];
		const split = splitTurnArtifacts(diff, SESSION, [artifact("report.md"), artifact("chart.html"), { ...artifact("scratch/log.txt"), registered: false }], undefined, [{ path: "chart.html", status: "modified" }]);
		expect(split.artifacts.map(({ artifact: entry, status }) => [entry.path, status])).toEqual([["report.md", "added"], ["chart.html", "modified"]]);
		expect(split.files.map((entry) => entry.path)).toEqual(["src/app.ts", `${ROOT}/scratch/log.txt`]);
	});

	it("shows every changed file while the artifact list is unread", () => {
		const diff = [file(`${ROOT}/report.md`)];
		expect(splitTurnArtifacts(diff, SESSION, undefined, undefined, [{ path: "report.md", status: "added" }])).toEqual({ artifacts: [], files: diff });
	});
});

describe("turnResults", () => {
	const diff = { files: [file("src/app.ts", "modified"), file(`${ROOT}/report.md`)] };
	const source = { sessionId: SESSION, artifacts: [artifact("report.md")] };

	it("moves a listed artifact from the changed files to a tile on a completed turn", () => {
		const results = turnResults(turn({ diff }), source);
		expect(results.artifacts.map(({ artifact: entry }) => entry.path)).toEqual(["report.md"]);
		expect(results.files.map((entry) => entry.path)).toEqual(["src/app.ts"]);
	});

	it.each(["running", "queued", "failed", "interrupted", "cancelled", "recovered"] as const)("keeps a %s turn's artifact among its changed files", (state) => {
		const results = turnResults(turn({ state, diff, artifacts: [{ path: "report.md", status: "added" }] }), source);
		expect(results.artifacts).toEqual([]);
		expect(results.files.map((entry) => entry.path)).toEqual(["src/app.ts", `${ROOT}/report.md`]);
	});

	it("finds an artifact renamed after the turn by the path the turn reported", () => {
		const renamed = { ...artifact("final.md"), previousPaths: ["report.md"] };
		const results = turnResults(turn({ diff }), { sessionId: SESSION, artifacts: [renamed] });
		expect(results.artifacts.map(({ artifact: entry }) => entry.path)).toEqual(["final.md"]);
		expect(results.files.map((entry) => entry.path)).toEqual(["src/app.ts"]);
	});

	it("lists every changed file while the artifact list is unread", () => {
		expect(turnResults(turn({ diff }), null)).toEqual({ artifacts: [], files: diff.files });
		expect(turnResults(turn({ diff }), { sessionId: SESSION })).toEqual({ artifacts: [], files: diff.files });
	});
});
