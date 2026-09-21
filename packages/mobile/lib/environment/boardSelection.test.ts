import { describe, expect, it } from "vitest";
import { publishCloudBoardResult, selectBoardState, type BoardState } from "./boardSelection";

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
