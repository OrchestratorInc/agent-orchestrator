import { describe, expect, it } from "vitest";
import {
	assertLocalEnvironment,
	isBoardConfigured,
	dispatchCurrentCloudBoardRequest,
	publishCloudBoardResult,
	selectBoardState,
	type BoardState,
	type CloudBoardRequest,
} from "./boardSelection";

const local: BoardState<string, string> = {
	projects: ["local-project"],
	sessions: ["local-session"],
	loading: false,
	error: "Local daemon unavailable",
};

const cloud: BoardState<string, string> = {
	projects: ["cloud-project"],
	sessions: ["cloud-session"],
	loading: true,
	error: null,
};

const empty: BoardState<string, string> = {
	projects: [],
	sessions: [],
	loading: false,
	error: null,
};

describe("isBoardConfigured", () => {
	it("makes a ready Cloud source reachable without a Local pairing", () => {
		expect(isBoardConfigured("cloud", "cloud", false)).toBe(true);
	});
	it("does not use retained Local configuration as Cloud readiness", () => {
		expect(isBoardConfigured("cloud", undefined, true)).toBe(false);
		expect(isBoardConfigured("cloud", "local", true)).toBe(false);
	});
	it("preserves Local pairing readiness", () => {
		expect(isBoardConfigured("local", "local", true)).toBe(true);
		expect(isBoardConfigured("local", undefined, false)).toBe(false);
	});
});

describe("selectBoardState", () => {
	it("selects the Cloud slice only for the resolved Cloud source", () => {
		expect(selectBoardState({ environment: "cloud", sourceKind: "cloud", local, cloud, empty })).toEqual({
			kind: "cloud",
			state: cloud,
		});
	});

	it("does not expose retained Local data while Cloud is unresolved", () => {
		expect(selectBoardState({ environment: "cloud", sourceKind: undefined, local, cloud, empty })).toEqual({
			kind: "none",
			state: empty,
		});
	});

	it("keeps the existing Local slice selected outside Cloud", () => {
		expect(selectBoardState({ environment: "local", sourceKind: "local", local, cloud, empty })).toEqual({
			kind: "local",
			state: local,
		});
	});
});

describe("publishCloudBoardResult", () => {
	it("drops a Cloud response after its request generation is invalidated", () => {
		expect(publishCloudBoardResult({
		current: cloud,
		requestGeneration: 4,
		currentGeneration: 5,
		result: { kind: "success", projects: ["stale-project"], sessions: ["stale-session"] },
	})).toBeUndefined();
	});

	it("publishes a current successful response and clears a prior error", () => {
		expect(publishCloudBoardResult({
		current: { ...cloud, loading: true, error: "Temporary failure" },
		requestGeneration: 5,
		currentGeneration: 5,
		result: { kind: "success", projects: ["fresh-project"], sessions: ["fresh-session"] },
	})).toEqual({
		projects: ["fresh-project"],
		sessions: ["fresh-session"],
		loading: false,
		error: null,
	});
	});

	it("retains the last Cloud snapshot on a current transient failure", () => {
		expect(publishCloudBoardResult({
		current: { ...cloud, loading: true },
		requestGeneration: 5,
		currentGeneration: 5,
		result: { kind: "failure", error: "Timed out" },
	})).toEqual({
		projects: ["cloud-project"],
		sessions: ["cloud-session"],
		loading: false,
		error: "Timed out",
	});
	});
});

describe("dispatchCurrentCloudBoardRequest", () => {
	it.each(["success", "failure"] as const)("does not let an older %s replace a newer snapshot or error", async (olderResult) => {
		const current: CloudBoardRequest<string> = { source: "org", generation: 1 };
		let state = { ...cloud };
		let releaseOlder!: () => void;
		const older = dispatchCurrentCloudBoardRequest(() => current, async (request) => {
			await new Promise<void>((resolve) => { releaseOlder = resolve; });
			state = publishCloudBoardResult({
				current: state, requestGeneration: request.generation, currentGeneration: current.generation,
				requestSequence: request.sequence, currentSequence: current.sequence,
				result: olderResult === "success"
					? { kind: "success", projects: ["old"], sessions: ["old"] }
					: { kind: "failure", error: "Old failure" },
			}) ?? state;
		});
		await dispatchCurrentCloudBoardRequest(() => current, async (request) => {
			state = publishCloudBoardResult({
				current: state, requestGeneration: request.generation, currentGeneration: current.generation,
				requestSequence: request.sequence, currentSequence: current.sequence,
				result: { kind: "success", projects: ["new"], sessions: ["new"] },
			}) ?? state;
		});
		releaseOlder();
		await older;
		expect(state).toEqual({ projects: ["new"], sessions: ["new"], loading: false, error: null });
	});

	it("keeps the last good snapshot and newest error when an older success arrives", async () => {
		const current: CloudBoardRequest<string> = { source: "org", generation: 1 };
		let state = { ...cloud };
		let releaseOlder!: () => void;
		const older = dispatchCurrentCloudBoardRequest(() => current, async (request) => {
			await new Promise<void>((resolve) => { releaseOlder = resolve; });
			state = publishCloudBoardResult({
				current: state, requestGeneration: request.generation, currentGeneration: current.generation,
				requestSequence: request.sequence, currentSequence: current.sequence,
				result: { kind: "success", projects: ["old"], sessions: ["old"] },
			}) ?? state;
		});
		await dispatchCurrentCloudBoardRequest(() => current, async (request) => {
			state = publishCloudBoardResult({
				current: state, requestGeneration: request.generation, currentGeneration: current.generation,
				requestSequence: request.sequence, currentSequence: current.sequence,
				result: { kind: "failure", error: "Newest failure" },
			}) ?? state;
		});
		releaseOlder();
		await older;
		expect(state).toEqual({ projects: ["cloud-project"], sessions: ["cloud-session"], loading: false, error: "Newest failure" });
	});

	it("uses the new source when a retained refresh callback runs after a source switch", async () => {
		let current: CloudBoardRequest<string> | undefined = { source: "old-org", generation: 1 };
		const loaded: string[] = [];
		const retainedRefresh = () => dispatchCurrentCloudBoardRequest(
			() => current,
			async (request) => { loaded.push(request.source); },
		);

		current = { source: "new-org", generation: 2 };
		await retainedRefresh();

		expect(loaded).toEqual(["new-org"]);
	});
});

describe("assertLocalEnvironment", () => {
	it("rejects retained Local actions while Cloud is active", () => {
		expect(() => assertLocalEnvironment("cloud")).toThrow("Local actions are unavailable outside the Local environment.");
	});

	it("allows Local actions in the Local environment", () => {
		expect(() => assertLocalEnvironment("local")).not.toThrow();
	});
});
