import { describe, expect, it } from "vitest";
import { CloudCpError } from "./cloud-cp";
import { moveErrorText, moveStateText, withMoveStateText } from "./cloud-move-state";

describe("withMoveStateText", () => {
	it("says why a moving session refuses, in the app's words, keeping the error's code", () => {
		const moving = new CloudCpError("This session is moving to another machine (CP text).", { status: 409, code: "SESSION_MOVING", requestId: "r-1" });
		const shown = withMoveStateText(moving);
		expect(shown).toBeInstanceOf(CloudCpError);
		expect(shown).toMatchObject({
			status: 409, code: "SESSION_MOVING", requestId: "r-1",
			message: "This session is moving to another computer, so it takes no messages and its agent stays stopped. Restart its agent to keep it in Orchestrator Cloud.",
		});
		expect(withMoveStateText(new CloudCpError("old worker (CP text)", { status: 409, code: "MOVE_WORKER_TOO_OLD" }))).toMatchObject({
			message: "This session's Orchestrator Cloud worker is too old to move it. Restart the session to update it, then move it.",
		});
		expect(withMoveStateText(new CloudCpError("still running (CP text)", { status: 409, code: "MOVE_AGENT_STILL_RUNNING" }))).toMatchObject({
			code: "MOVE_AGENT_STILL_RUNNING",
			message: "Orchestrator Cloud could not confirm the agent had stopped: some of its processes were still running, so nothing was committed or pushed. Try again.",
		});
		expect(withMoveStateText(new CloudCpError("turns queued (CP text)", { status: 409, code: "MOVE_TURNS_QUEUED" }))).toMatchObject({
			status: 409,
			code: "MOVE_TURNS_QUEUED",
			message: "Messages are still queued for this session in Orchestrator Cloud. Let them run or remove them, then move it.",
		});
		expect(withMoveStateText(new CloudCpError("move in progress (CP text)", { status: 409, code: "MOVE_IN_PROGRESS" }))).toMatchObject({
			status: 409,
			code: "MOVE_IN_PROGRESS",
			message: "This session is moving to another computer right now. Restart its agent once the move has finished or failed.",
		});
	});

	it("leaves every other error as it is", () => {
		const other = new CloudCpError("busy", { status: 409, code: "STEER_IN_PROGRESS" });
		expect(withMoveStateText(other)).toBe(other);
		const plain = new Error("offline");
		expect(withMoveStateText(plain)).toBe(plain);
		expect(moveStateText(undefined)).toBeUndefined();
	});

	it("keeps a switch in progress in its own words outside the move dialog", () => {
		const switching = new CloudCpError("A switch is already in progress for this session.", { status: 409, code: "INTERFACE_TRANSITION_IN_PROGRESS" });
		expect(withMoveStateText(switching)).toBe(switching);
		expect(moveErrorText("INTERFACE_TRANSITION_IN_PROGRESS")).toBe("A switch is in progress for this session. Try again when it finishes.");
		expect(moveErrorText("MOVE_TURNS_QUEUED")).toBe(moveStateText("MOVE_TURNS_QUEUED"));
		expect(moveErrorText("STEER_IN_PROGRESS")).toBeUndefined();
	});
});
