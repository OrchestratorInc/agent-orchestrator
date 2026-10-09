import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";

vi.mock("../lib/platform", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/platform")>();
	return {
		...actual,
		isLinuxPlatform: () => true,
		isMacPlatform: () => false,
	};
});

const { PageTopbar, PageTopbarTitle } = await import("./PageTopbar");

describe("PageTopbar on Linux", () => {
	beforeEach(() => {
		useUiStore.setState({ isSidebarOpen: true });
	});

	it("pads the title past the collapse arrows when the sidebar is off-canvas", () => {
		useUiStore.setState({ isSidebarOpen: false });
		render(<PageTopbar lead={<PageTopbarTitle data-testid="title">Board</PageTopbarTitle>} />);

		const header = screen.getByTestId("title").closest("header");
		// Cluster left (26) + cluster width (92) + content gap (12) - panel inline inset (16).
		expect(header).toHaveStyle({ paddingLeft: "114px" });
	});

	it("keeps the default title padding while the sidebar is open", () => {
		render(<PageTopbar lead={<PageTopbarTitle data-testid="title">Board</PageTopbarTitle>} />);

		expect(screen.getByTestId("title").closest("header")).toHaveStyle({ paddingLeft: "18px" });
	});
});
