import { act, render, screen, within } from "@testing-library/react";
import { useLayoutEffect, useRef } from "react";
import { describe, expect, it } from "vitest";
import { KeepRecentAlive } from "./KeepRecentAlive";
import { SessionTopbarHost, SessionTopbarPortal, SessionTopbarProvider, SessionTopbarScope } from "./SessionTopbarPortal";

const attachedAtLayout: Record<string, boolean[]> = {};

/** The portaled header, measuring where it is as a tab strip would. */
function MeasuredHeader({ id }: { id: string }) {
	const ref = useRef<HTMLElement>(null);
	// Mount-only: runs when the header first mounts and each time its chat is shown again.
	useLayoutEffect(() => {
		(attachedAtLayout[id] ??= []).push(Boolean(ref.current?.closest('[data-testid="session-topbar-host"]')));
	}, [id]);
	return <header ref={ref}>tabs {id}</header>;
}

function ChatHeader({ id }: { id: string }) {
	return (
		<SessionTopbarPortal>
			<MeasuredHeader id={id} />
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
		// Mounted with the bar itself: the header waits for it instead of mounting detached.
		expect(attachedAtLayout.a).toEqual([true]);
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
		expect(attachedAtLayout.a).toEqual([true, true]);
	});
});
