import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { CoderWorkspaceTopbarButton, coderWorkspaceNameFromUrl } from "./CoderWorkspaceLinkButton";
import { TooltipProvider } from "./ui/tooltip";

const opened = vi.hoisted(() => vi.fn());
vi.mock("../lib/external-link-policy", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/external-link-policy")>()),
	openLinkInSystemBrowser: opened,
}));

describe("coderWorkspaceNameFromUrl", () => {
	it("reads the workspace name from a Coder workspace page", () => {
		expect(coderWorkspaceNameFromUrl("https://coder.example.com/@ahmad/alice-929c07b4")).toBe("alice-929c07b4");
		expect(coderWorkspaceNameFromUrl("https://coder.example.com/base/@ahmad/ao-0123abcd/")).toBe("ao-0123abcd");
	});

	it("returns nothing for a URL without an owner segment", () => {
		expect(coderWorkspaceNameFromUrl("https://coder.example.com/workspaces")).toBe("");
		expect(coderWorkspaceNameFromUrl("not a url")).toBe("");
	});
});

describe("CoderWorkspaceTopbarButton", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		opened.mockReset();
	});

	it("names the workspace and opens it in the browser", async () => {
		const user = userEvent.setup();
		render(<TooltipProvider><CoderWorkspaceTopbarButton url="https://coder.example.com/@ahmad/alice-929c07b4" /></TooltipProvider>);
		const button = screen.getByRole("button", { name: "Open alice-929c07b4 in Coder" });
		expect(button).toHaveTextContent("alice-929c07b4");
		await user.click(button);
		expect(opened).toHaveBeenCalledWith("https://coder.example.com/@ahmad/alice-929c07b4");
	});

	it("renders nothing without a web URL", () => {
		render(<TooltipProvider><CoderWorkspaceTopbarButton url={undefined} /></TooltipProvider>);
		expect(screen.queryByTestId("coder-workspace-topbar-link")).toBeNull();
	});
});
