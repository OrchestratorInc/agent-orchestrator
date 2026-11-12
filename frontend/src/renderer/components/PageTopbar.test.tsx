import { act, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { PageTopbar, PageTopbarSurface, PageTopbarTitle, PageTopbarTrailingAction, SessionTabsTopbar } from "./PageTopbar";

vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

describe("PageTopbar", () => {
	it("renders the lead and right-aligned actions in one responsive container", () => {
		render(
			<PageTopbar
				actions={<button type="button">New task</button>}
				lead={<PageTopbarTitle as="h1">Board</PageTopbarTitle>}
			/>,
		);

		const header = screen.getByRole("banner");
		expect(header).toHaveClass("workspace-topbar-container", "center-panel-titlebar", "h-toolbar", "pr-(--gap-topbar-control)");
		expect(within(header).getByRole("heading", { name: "Board" })).toBeInTheDocument();
		expect(within(screen.getByTestId("workspace-topbar-actions")).getByRole("button", { name: "New task" })).toBeInTheDocument();
	});

	it("leaves notifications off utility page headers", () => {
		render(<PageTopbar lead={<PageTopbarTitle>Archive</PageTopbarTitle>} />);

		expect(screen.queryByRole("button", { name: "Notifications" })).not.toBeInTheDocument();
	});
});

describe("PageTopbarSurface", () => {
	beforeEach(() => useUiStore.setState({ settingsModal: null }));
	afterEach(() => useUiStore.setState({ settingsModal: null }));

	it("only mounts notifications for workspace panels", () => {
		const view = render(<PageTopbarSurface><PageTopbar /></PageTopbarSurface>);
		expect(screen.queryByRole("button", { name: "Notifications" })).not.toBeInTheDocument();

		view.rerender(<PageTopbarSurface notifications><PageTopbar /></PageTopbarSurface>);
		expect(screen.getAllByRole("button", { name: "Notifications" })).toHaveLength(1);

		view.rerender(<PageTopbarSurface><PageTopbar /></PageTopbarSurface>);
		expect(screen.queryByRole("button", { name: "Notifications" })).not.toBeInTheDocument();
	});

	it("keeps the same bell when the workspace changes from a board to session tabs", () => {
		const view = render(<PageTopbarSurface notifications><PageTopbar /></PageTopbarSurface>);
		const bell = screen.getByRole("button", { name: "Notifications" });

		view.rerender(<PageTopbarSurface notifications><SessionTabsTopbar tabs={<div role="tablist" />} /></PageTopbarSurface>);

		expect(screen.getByRole("button", { name: "Notifications" })).toBe(bell);
		expect(screen.getAllByRole("button", { name: "Notifications" })).toHaveLength(1);
	});

	it("places session controls immediately before the panel-owned bell", () => {
		render(
			<PageTopbarSurface notifications>
				<PageTopbarTrailingAction><button type="button">Toggle inspector</button></PageTopbarTrailingAction>
			</PageTopbarSurface>,
		);

		expect(within(screen.getByTestId("panel-topbar-controls")).getAllByRole("button")).toEqual([
			screen.getByRole("button", { name: "Toggle inspector" }),
			screen.getByRole("button", { name: "Notifications" }),
		]);
	});

	it("keeps the bell but hides session controls when settings replaces a still-mounted session", () => {
		render(
			<PageTopbarSurface notifications>
				<SessionTabsTopbar tabs={<div role="tablist" />} />
				<PageTopbarTrailingAction><button type="button">Toggle inspector</button></PageTopbarTrailingAction>
			</PageTopbarSurface>,
		);
		expect(screen.getByRole("button", { name: "Notifications" })).toBeVisible();
		expect(screen.getByRole("button", { name: "Toggle inspector" })).toBeVisible();

		act(() => useUiStore.getState().openGlobalSettings("harness"));
		expect(screen.getByRole("tablist")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Notifications" })).toBeVisible();
		expect(screen.queryByRole("button", { name: "Toggle inspector" })).not.toBeInTheDocument();

		act(() => useUiStore.getState().closeSettings());
		expect(screen.getByRole("button", { name: "Notifications" })).toBeVisible();
		expect(screen.getByRole("button", { name: "Toggle inspector" })).toBeVisible();
	});
});

describe("SessionTabsTopbar", () => {
	it("frames the tab strip and trailing actions at the shared toolbar height", () => {
		const { container } = render(
			<SessionTabsTopbar
				actions={<button type="button">Archive</button>}
				clearanceClassName="session-topbar-titlebar-clearance-mac"
				regionWidth={320}
				tabs={<div role="tablist" />}
			/>,
		);

		expect(container.firstElementChild).toHaveClass("h-toolbar", "bg-sidebar");
		const region = screen.getByTestId("session-terminal-region");
		expect(region).toHaveClass("session-topbar-titlebar-clearance-mac");
		expect(region).toHaveStyle({ width: "320px" });
		expect(screen.getByTestId("session-action-region")).toHaveClass("session-topbar-actions");
		expect(within(region).getByRole("tablist")).toBeInTheDocument();
	});

	it("keeps global notifications when fullscreen hides session actions", () => {
		render(<SessionTabsTopbar actions={<button type="button">Archive</button>} fullscreen hideActions tabs={<div role="tablist" />} />);

		expect(screen.getByRole("button", { name: "Notifications" })).toBeVisible();
		expect(screen.queryByRole("button", { name: "Archive" })).not.toBeInTheDocument();
	});
});
