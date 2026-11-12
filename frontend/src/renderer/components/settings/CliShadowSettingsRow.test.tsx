import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CliShadowStatus } from "../../../shared/cli-shadow";
import { aoBridge } from "../../lib/bridge";
import { CliShadowSettingsRow } from "./CliShadowSettingsRow";

type Shadowed = Extract<CliShadowStatus, { kind: "shadowed" }>;

const shadowed: Shadowed = {
	kind: "shadowed",
	shadowPath: "/Users/me/.local/bin/ao",
	shadowTarget: "/Users/me/old/ao",
	shadowIdentity: "identity-1",
	versionChecked: true,
	shadowVersion: "0.13.0",
	bundledPath: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao",
	bundledVersion: "0.14.8",
	relation: "older",
	fix: { kind: "replace", replaces: "link" },
};

const OLDER_TITLE = "Your terminal runs an older ao (v0.13.0)";
const DIFFERENT_TITLE = "Your terminal runs a different ao";

function mockStatus(status: CliShadowStatus) {
	return vi.spyOn(aoBridge.cliShadow, "inspect").mockResolvedValue(status);
}

// The row's own button and the confirm dialog's share a label; the dialog's is last.
function confirmReplace() {
	const buttons = screen.getAllByRole("button", { name: "Use this app's ao" });
	fireEvent.click(buttons[buttons.length - 1] as HTMLElement);
}

describe("CliShadowSettingsRow", () => {
	afterEach(() => vi.restoreAllMocks());

	it.each([["newer", "newer ao"], ["older", "older ao"], ["different", "different ao"]] as const)("shows a %s version", async (relation, text) => {
		mockStatus({ ...shadowed, relation });
		render(<CliShadowSettingsRow />);
		expect(await screen.findByText(new RegExp(text))).toBeInTheDocument();
		expect(screen.getByText(/fish/)).toBeInTheDocument();
	});

	it("stays away when the versions match", async () => {
		const inspect = mockStatus({ ...shadowed, relation: "same" });
		const { container } = render(<CliShadowSettingsRow />);
		await waitFor(() => expect(inspect).toHaveBeenCalled());
		await Promise.resolve();
		expect(container).toBeEmptyDOMElement();
	});

	// "unknown" is also what the main process answers in dev and isolated builds.
	it.each(["match", "absent", "unknown"] as const)("renders nothing on %s", async (kind) => {
		const inspect = mockStatus({ kind });
		const { container } = render(<CliShadowSettingsRow />);
		await waitFor(() => expect(inspect).toHaveBeenCalled());
		await Promise.resolve();
		expect(container).toBeEmptyDOMElement();
	});

	it("renders nothing when the check fails", async () => {
		const inspect = vi.spyOn(aoBridge.cliShadow, "inspect").mockRejectedValue(new Error("no handler"));
		const { container } = render(<CliShadowSettingsRow />);
		await waitFor(() => expect(inspect).toHaveBeenCalled());
		await Promise.resolve();
		expect(container).toBeEmptyDOMElement();
	});

	it("only checks the version when requested for the displayed file", async () => {
		const inspect = mockStatus({ ...shadowed, versionChecked: false, shadowVersion: null, relation: "different" });
		render(<CliShadowSettingsRow />);
		await screen.findByText(DIFFERENT_TITLE);
		expect(inspect).toHaveBeenCalledWith(undefined);
		fireEvent.click(screen.getByRole("button", { name: "Run ao version" }));
		await waitFor(() => expect(inspect).toHaveBeenCalledWith("identity-1"));
	});

	it("says older and keeps both paths in the details", async () => {
		mockStatus(shadowed);
		const { container } = render(<CliShadowSettingsRow />);
		await screen.findByText(OLDER_TITLE);
		const details = container.querySelector("details");
		expect(details).not.toHaveAttribute("open");
		expect(details).toHaveTextContent("/Users/me/.local/bin/ao");
		expect(details).toHaveTextContent("/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao");
	});

	it("only lets a path wrap after a slash", async () => {
		mockStatus(shadowed);
		const { container } = render(<CliShadowSettingsRow />);
		await screen.findByText(OLDER_TITLE);
		const path = container.querySelector("dd span.font-mono");
		expect([...(path?.children ?? [])].map((segment) => segment.textContent)).toEqual(["/", "Users/", "me/", ".local/", "bin/", "ao"]);
		expect(path?.textContent).toBe("/Users/me/.local/bin/ao");
	});

	it("reports a version it cannot order without calling it older", async () => {
		mockStatus({ ...shadowed, shadowVersion: "dev", relation: "different" });
		const { container } = render(<CliShadowSettingsRow />);
		await screen.findByText(DIFFERENT_TITLE);
		expect(container).not.toHaveTextContent("older");
	});

	it("says so when the version is unknown", async () => {
		mockStatus({ ...shadowed, shadowVersion: null, relation: "different" });
		const { container } = render(<CliShadowSettingsRow />);
		await screen.findByText(DIFFERENT_TITLE);
		expect(container).toHaveTextContent("version unknown");
	});

	it("replaces only after the confirm, passing what the user saw, then clears the row", async () => {
		const inspect = mockStatus(shadowed);
		const replace = vi.spyOn(aoBridge.cliShadow, "replace").mockResolvedValue({ ok: true });
		render(<CliShadowSettingsRow />);
		fireEvent.click(await screen.findByRole("button", { name: "Use this app's ao" }));
		expect(replace).not.toHaveBeenCalled();
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("/Users/me/.local/bin/ao");

		inspect.mockResolvedValue({ kind: "match" });
		confirmReplace();
		await waitFor(() => expect(replace).toHaveBeenCalledWith("/Users/me/.local/bin/ao", "/Users/me/old/ao", "identity-1"));
		await waitFor(() => expect(screen.queryByText(/fish/)).not.toBeInTheDocument());
	});

	it("ignores a version probe that finishes after a replacement refresh", async () => {
		const unchecked: Shadowed = { ...shadowed, versionChecked: false, shadowVersion: null, relation: "different" };
		let finishProbe: (status: CliShadowStatus) => void = () => { throw new Error("probe not initialized"); };
		const probe = new Promise<CliShadowStatus>((resolve) => { finishProbe = resolve; });
		const inspect = vi.spyOn(aoBridge.cliShadow, "inspect")
			.mockResolvedValueOnce(unchecked)
			.mockReturnValueOnce(probe)
			.mockResolvedValue({ kind: "match" });
		vi.spyOn(aoBridge.cliShadow, "replace").mockResolvedValue({ ok: true });
		render(<CliShadowSettingsRow />);
		await screen.findByText(DIFFERENT_TITLE);
		fireEvent.click(screen.getByRole("button", { name: "Run ao version" }));
		await waitFor(() => expect(inspect).toHaveBeenCalledTimes(2));
		fireEvent.click(screen.getByRole("button", { name: "Use this app's ao" }));
		await screen.findByRole("dialog");
		confirmReplace();
		await waitFor(() => expect(screen.queryByText(/fish/)).not.toBeInTheDocument());
		await act(async () => { finishProbe(unchecked); });
		expect(screen.queryByText(/fish/)).not.toBeInTheDocument();
	});

	it("warns that replacing a regular file cannot be undone", async () => {
		mockStatus({ ...shadowed, fix: { kind: "replace", replaces: "file" } });
		render(<CliShadowSettingsRow />);
		fireEvent.click(await screen.findByRole("button", { name: "Use this app's ao" }));
		expect(await screen.findByRole("dialog")).toHaveTextContent("can't be undone");
	});

	it("keeps the dialog open with the reason when the replace fails", async () => {
		mockStatus(shadowed);
		vi.spyOn(aoBridge.cliShadow, "replace").mockResolvedValue({ ok: false, code: "changed", message: "It changed." });
		render(<CliShadowSettingsRow />);
		fireEvent.click(await screen.findByRole("button", { name: "Use this app's ao" }));
		await screen.findByRole("dialog");
		confirmReplace();
		expect(await screen.findByText("It changed.")).toBeInTheDocument();
	});

	it("offers a reveal and a copyable command instead when AO must not replace it", async () => {
		mockStatus({ ...shadowed, fix: { kind: "manual", command: "brew uninstall ao" } });
		const reveal = vi.spyOn(aoBridge.cliShadow, "reveal").mockResolvedValue(undefined);
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const { container } = render(<CliShadowSettingsRow />);
		await screen.findByText(OLDER_TITLE);
		expect(screen.queryByRole("button", { name: "Use this app's ao" })).not.toBeInTheDocument();
		expect(container.querySelector("details")).toHaveTextContent("brew uninstall ao");
		fireEvent.click(screen.getByRole("button", { name: /Show in (Finder|folder)/ }));
		expect(reveal).toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: "Copy command" }));
		expect(writeText).toHaveBeenCalledWith("brew uninstall ao");
		expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
	});
});
