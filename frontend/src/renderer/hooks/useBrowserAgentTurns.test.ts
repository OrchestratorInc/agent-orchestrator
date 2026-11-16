import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sessionUiKey } from "../lib/hosts";
import type { WorkspaceSession } from "../types/workspace";
import { useBrowserAgentTurns } from "./useBrowserAgentTurns";

type TurnSession = Pick<WorkspaceSession, "id" | "hostId" | "activity">;

const session = (id: string, state?: NonNullable<WorkspaceSession["activity"]>["state"], hostId?: string): TurnSession => ({
	id,
	...(hostId ? { hostId } : {}),
	...(state ? { activity: { state, lastActivityAt: "2026-11-16T00:00:00Z" } } : {}),
});

describe("useBrowserAgentTurns", () => {
	afterEach(() => vi.restoreAllMocks());

	it("reports every session's turn, shown or not, once per change", () => {
		const setAgentTurn = vi.spyOn(window.ao!.browser, "setAgentTurn");
		const { rerender } = renderHook(({ projects }) => useBrowserAgentTurns(projects), {
			initialProps: { projects: [{ sessions: [session("a", "active"), session("b", "idle")] }] },
		});
		expect(setAgentTurn.mock.calls.map(([input]) => input)).toEqual([
			{ sessionId: "a", active: true },
			{ sessionId: "b", active: false },
		]);

		// Session a's turn ends while another session is on screen; b is unchanged.
		setAgentTurn.mockClear();
		rerender({ projects: [{ sessions: [session("a", "idle"), session("b", "idle")] }] });
		expect(setAgentTurn.mock.calls.map(([input]) => input)).toEqual([{ sessionId: "a", active: false }]);
	});

	it("counts a turn waiting on a permission prompt as running, and keys remote sessions like their Browser", () => {
		const setAgentTurn = vi.spyOn(window.ao!.browser, "setAgentTurn");
		renderHook(() => useBrowserAgentTurns([{ sessions: [session("a", "blocked"), session("r", "active", "host-1")] }]));
		expect(setAgentTurn.mock.calls.map(([input]) => input)).toEqual([
			{ sessionId: "a", active: true },
			{ sessionId: sessionUiKey("r", "host-1"), active: true },
		]);
	});
});
