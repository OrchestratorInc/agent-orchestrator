import { describe, expect, it } from "vitest";
import { sessionTopbarActionIds } from "./sessionTopbarModel";

// The desktop session topbar's actions (SessionTopbarActions.tsx), on the phone.
describe("sessionTopbarActionIds", () => {
	it("leads a worker with its project's orchestrator", () => {
		expect(sessionTopbarActionIds({ orchestrator: false, projectId: "app" })).toEqual(["orchestrator"]);
	});

	it("gives an orchestrator its board, then New task", () => {
		expect(sessionTopbarActionIds({ orchestrator: true, projectId: "app" })).toEqual(["board", "new-task"]);
	});

	// A standalone agent has no project, so no orchestrator and no board.
	it("adds nothing for a session with no project", () => {
		expect(sessionTopbarActionIds({ orchestrator: false })).toEqual([]);
		expect(sessionTopbarActionIds({ orchestrator: true, projectId: "" })).toEqual([]);
	});
});
