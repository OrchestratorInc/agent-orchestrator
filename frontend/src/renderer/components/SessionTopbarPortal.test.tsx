import { createRef, useState, type Ref } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionFileTabs } from "./SessionFileTabs";
import { SessionTopbarHost, SessionTopbarPortal, SessionTopbarProvider } from "./SessionTopbarPortal";
import { TooltipProvider } from "./ui/tooltip";

function CenterFileComposition({ forwardedRef }: { forwardedRef?: Ref<HTMLDivElement> }) {
	const [path, setPath] = useState<string | null>(null);
	return (
		<SessionTopbarProvider>
			<TooltipProvider>
				<SessionTopbarHost data-testid="session-topbar-host" ref={forwardedRef} />
				<SessionTopbarPortal>
					{path ? (
						<div role="tablist">
							<SessionFileTabs
								onActivateFile={() => {}}
								onCloseFile={() => setPath(null)}
								state={{ activePath: path, openPaths: [path] }}
							/>
						</div>
					) : <span>agent tab</span>}
				</SessionTopbarPortal>
				<button onClick={() => setPath("README.md")} type="button">open tracked file</button>
				<button onClick={() => setPath("AO_UI_AUDIT.md")} type="button">open untracked file</button>
				{path ? <div data-testid="center-file">{path}</div> : null}
			</TooltipProvider>
		</SessionTopbarProvider>
	);
}

function ReplaceableHost({ forwardedRef }: { forwardedRef: Ref<HTMLDivElement> }) {
	const [generation, setGeneration] = useState(1);
	const [visible, setVisible] = useState(true);
	return (
		<SessionTopbarProvider>
			{visible ? <SessionTopbarHost data-generation={generation} data-testid="session-topbar-host" key={generation} ref={forwardedRef} /> : null}
			<SessionTopbarPortal><span>terminal tabs</span></SessionTopbarPortal>
			<button onClick={() => setGeneration((current) => current + 1)} type="button">replace host</button>
			<button onClick={() => setVisible((current) => !current)} type="button">toggle host</button>
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

	it("keeps the host ref attached while tracked and untracked center files mount and a close tooltip opens", async () => {
		const forwardedRef = vi.fn();
		const view = render(<CenterFileComposition forwardedRef={forwardedRef} />);
		const host = screen.getByTestId("session-topbar-host");

		expect(forwardedRef.mock.calls).toEqual([[host]]);
		expect(within(host).getByText("agent tab")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "open tracked file" }));
		expect(screen.getByTestId("center-file")).toHaveTextContent("README.md");
		expect(within(host).getByRole("tab", { name: "README.md" })).toBeInTheDocument();
		await userEvent.hover(within(host).getByRole("button", { name: "Close README.md" }));
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Close README.md");
		expect(forwardedRef.mock.calls).toEqual([[host]]);

		fireEvent.click(screen.getByRole("button", { name: "open untracked file" }));
		expect(screen.getByTestId("center-file")).toHaveTextContent("AO_UI_AUDIT.md");
		expect(within(host).getByRole("tab", { name: "AO_UI_AUDIT.md" })).toBeInTheDocument();
		expect(forwardedRef.mock.calls).toEqual([[host]]);

		view.unmount();
		expect(forwardedRef.mock.calls).toEqual([[host], [null]]);
	});

	it("forwards an object ref across host replacement, removal, and unmount", () => {
		const forwardedRef = createRef<HTMLDivElement>();
		const view = render(<ReplaceableHost forwardedRef={forwardedRef} />);
		const firstHost = screen.getByTestId("session-topbar-host");

		expect(forwardedRef.current).toBe(firstHost);
		expect(within(firstHost).getByText("terminal tabs")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "replace host" }));
		const secondHost = screen.getByTestId("session-topbar-host");
		expect(secondHost).not.toBe(firstHost);
		expect(forwardedRef.current).toBe(secondHost);
		expect(within(secondHost).getByText("terminal tabs")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "toggle host" }));
		expect(screen.queryByTestId("session-topbar-host")).not.toBeInTheDocument();
		expect(screen.queryByText("terminal tabs")).not.toBeInTheDocument();
		expect(forwardedRef.current).toBeNull();

		fireEvent.click(screen.getByRole("button", { name: "toggle host" }));
		const restoredHost = screen.getByTestId("session-topbar-host");
		expect(forwardedRef.current).toBe(restoredHost);
		expect(within(restoredHost).getByText("terminal tabs")).toBeInTheDocument();

		view.unmount();
		expect(forwardedRef.current).toBeNull();
	});
});
