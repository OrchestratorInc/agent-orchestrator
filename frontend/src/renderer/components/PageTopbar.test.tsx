import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PageTopbar, PageTopbarTitle, SessionTabsTopbar } from "./PageTopbar";

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

	it("keeps notifications visible when a page has no actions", () => {
		render(<PageTopbar lead={<PageTopbarTitle>Archive</PageTopbarTitle>} />);

		expect(within(screen.getByTestId("workspace-topbar-actions")).getByRole("button", { name: "Notifications" })).toBeVisible();
		expect(screen.getAllByRole("button", { name: "Notifications" })).toHaveLength(1);
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
		render(<SessionTabsTopbar actions={<button type="button">Archive</button>} hideActions tabs={<div role="tablist" />} />);

		expect(screen.getByRole("button", { name: "Notifications" })).toBeVisible();
		expect(screen.queryByRole("button", { name: "Archive" })).not.toBeInTheDocument();
	});
});
