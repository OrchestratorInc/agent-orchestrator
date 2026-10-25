import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PageTopbar, PageTopbarTitle, SessionTabsTopbar } from "./PageTopbar";

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

	it("omits the action cluster when a page has no actions", () => {
		render(<PageTopbar lead={<PageTopbarTitle>Archive</PageTopbarTitle>} />);

		expect(screen.queryByTestId("workspace-topbar-actions")).not.toBeInTheDocument();
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

	it("drops the action region when the strip is rendered fullscreen", () => {
		render(<SessionTabsTopbar actions={<button type="button">Archive</button>} hideActions tabs={<div role="tablist" />} />);

		expect(screen.queryByTestId("session-action-region")).not.toBeInTheDocument();
	});
});
