import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { WorkspaceSession } from "../types/workspace";
import { SessionSwitcher } from "./SessionSwitcher";

const handlers = {
	step: undefined as ((direction: -1 | 1) => void) | undefined,
	release: undefined as (() => void) | undefined,
	cancel: undefined as (() => void) | undefined,
};

function session(id: string, lastInteractionAt: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		workspaceId: "proj",
		workspaceName: "proj",
		title: id,
		provider: "codex",
		branch: "main",
		status: "working",
		updatedAt: lastInteractionAt,
		lastInteractionAt,
		prs: [],
		kind: "worker",
		...extra,
	};
}

describe("SessionSwitcher", () => {
	beforeEach(() => {
		handlers.step = undefined;
		handlers.release = undefined;
		handlers.cancel = undefined;
		aoBridge.app.onSessionSwitcherStep = (listener) => {
			handlers.step = listener;
			return () => undefined;
		};
		aoBridge.app.onSessionSwitcherRelease = (listener) => {
			handlers.release = listener;
			return () => undefined;
		};
		aoBridge.app.onSessionSwitcherCancel = (listener) => {
			handlers.cancel = listener;
			return () => undefined;
		};
	});

	it("opens on the current session, advances on a later press, and commits on release without taking focus", () => {
		const onCommit = vi.fn();
		const previous = document.activeElement;
		render(
			<SessionSwitcher
				currentSessionId="A"
				kanban={false}
				onCommit={onCommit}
				projectKey="local:proj"
				sessions={[
					session("orch", "2026-01-01T00:00:00Z", { kind: "orchestrator", title: "Orchestrator" }),
					session("A", "2026-01-03T00:00:00Z"),
					session("B", "2026-01-02T00:00:00Z"),
				]}
			/>,
		);
		act(() => handlers.step?.(1));
		expect(screen.getByRole("option", { name: "A" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("listbox")).toHaveAttribute("tabindex", "-1");
		expect(document.activeElement).toBe(previous);
		act(() => handlers.step?.(1));
		expect(screen.getByRole("option", { name: "B" })).toHaveAttribute("aria-selected", "true");
		act(() => handlers.release?.());
		expect(onCommit).toHaveBeenCalledWith("B");
		expect(screen.queryByTestId("session-switcher")).not.toBeInTheDocument();
	});

	it("highlights the orchestrator from the kanban and ignores release after escape", () => {
		const onCommit = vi.fn();
		render(
			<SessionSwitcher
				kanban
				onCommit={onCommit}
				projectKey="local:proj"
				sessions={[
					session("orch", "2026-01-01T00:00:00Z", { kind: "orchestrator", title: "Orchestrator" }),
					session("A", "2026-01-03T00:00:00Z"),
				]}
			/>,
		);
		act(() => handlers.step?.(1));
		expect(screen.getByRole("option", { name: /Orchestrator/ })).toHaveAttribute("aria-selected", "true");
		act(() => handlers.cancel?.());
		act(() => handlers.release?.());
		expect(onCommit).not.toHaveBeenCalled();
		expect(screen.queryByTestId("session-switcher")).not.toBeInTheDocument();
	});

	it("does not commit a session that terminated while the popup was open", () => {
		const onCommit = vi.fn();
		const { rerender } = render(
			<SessionSwitcher
				currentSessionId="A"
				kanban={false}
				onCommit={onCommit}
				projectKey="local:proj"
				sessions={[session("A", "2026-01-03T00:00:00Z"), session("B", "2026-01-02T00:00:00Z")]}
			/>,
		);
		act(() => handlers.step?.(1));
		act(() => handlers.step?.(1));
		rerender(
			<SessionSwitcher
				currentSessionId="A"
				kanban={false}
				onCommit={onCommit}
				projectKey="local:proj"
				sessions={[session("A", "2026-01-03T00:00:00Z"), session("B", "2026-01-02T00:00:00Z", { isTerminated: true })]}
			/>,
		);
		act(() => handlers.release?.());
		expect(onCommit).not.toHaveBeenCalled();
	});
});
