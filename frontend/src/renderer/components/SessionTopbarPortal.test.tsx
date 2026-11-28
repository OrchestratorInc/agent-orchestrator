import { act, render, screen, within } from "@testing-library/react";
import { useLayoutEffect, useRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { finishReplayedAnimationsBeforePaint } from "../lib/quiet-reveal";
import { KeepRecentAlive } from "./KeepRecentAlive";
import { SessionTopbarHost, SessionTopbarPortal, SessionTopbarProvider, SessionTopbarScope } from "./SessionTopbarPortal";

vi.mock("../lib/quiet-reveal", () => ({ finishReplayedAnimationsBeforePaint: vi.fn() }));

const attachedAtLayout: Record<string, boolean> = {};

function ChatHeader({ id }: { id: string }) {
	const ref = useRef<HTMLElement>(null);
	useLayoutEffect(() => {
		attachedAtLayout[id] = Boolean(ref.current?.closest('[data-testid="session-topbar-host"]'));
	});
	return (
		<SessionTopbarPortal>
			<header ref={ref}>tabs {id}</header>
		</SessionTopbarPortal>
	);
}

function KeptChats({ id }: { id: string }) {
	return (
		<SessionTopbarProvider>
			<SessionTopbarHost data-testid="session-topbar-host" />
			<KeepRecentAlive activeKey={id}>
				<SessionTopbarScope>
					<ChatHeader key={id} id={id} />
				</SessionTopbarScope>
			</KeepRecentAlive>
		</SessionTopbarProvider>
	);
}

describe("SessionTopbarPortal", () => {
	it("renders session chrome in the shell-owned host", () => {
		render(
			<SessionTopbarProvider>
				<SessionTopbarHost data-testid="session-topbar-host" />
				<SessionTopbarPortal>
					<span>terminal tabs</span>
				</SessionTopbarPortal>
			</SessionTopbarProvider>,
		);

		const host = screen.getByTestId("session-topbar-host");
		expect(host.classList.contains("workspace-topbar-container")).toBe(true);
		expect(within(host).getByText("terminal tabs")).not.toBeNull();
	});

	it("renders inline when a component test has no shell provider", () => {
		render(
			<SessionTopbarPortal>
				<span>terminal tabs</span>
			</SessionTopbarPortal>,
		);

		expect(screen.getByText("terminal tabs")).toBeInTheDocument();
	});

	it("keeps a hidden kept-alive chat's header out of the top bar", () => {
		const { rerender } = render(<KeptChats id="a" />);
		const host = screen.getByTestId("session-topbar-host");
		expect(host).toHaveTextContent("tabs a");

		act(() => rerender(<KeptChats id="b" />));
		// Only the shown chat's tabs are in the bar; the hidden one keeps its DOM, detached.
		expect(host.querySelectorAll("header")).toHaveLength(1);
		expect(host).toHaveTextContent("tabs b");
		expect(host).not.toHaveTextContent("tabs a");

		act(() => rerender(<KeptChats id="a" />));
		expect(host.querySelectorAll("header")).toHaveLength(1);
		expect(host).toHaveTextContent("tabs a");
		// In the bar before the chat's own layout effects run, so they can measure it.
		expect(attachedAtLayout.a).toBe(true);
	});

	it("finishes the header's entrance when a kept chat's header returns to the bar, not when it first arrives", () => {
		vi.mocked(finishReplayedAnimationsBeforePaint).mockClear();
		const { rerender } = render(<KeptChats id="a" />);
		expect(finishReplayedAnimationsBeforePaint).not.toHaveBeenCalled();

		act(() => rerender(<KeptChats id="b" />));
		expect(finishReplayedAnimationsBeforePaint).not.toHaveBeenCalled();

		act(() => rerender(<KeptChats id="a" />));
		expect(finishReplayedAnimationsBeforePaint).toHaveBeenCalledOnce();
		expect(vi.mocked(finishReplayedAnimationsBeforePaint).mock.calls[0]?.[0]).toHaveTextContent("tabs a");
	});
});
