import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { aoBridge } from "../../lib/bridge";
import { CliLinkSettings } from "./CliLinkSettingsRow";

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		cliLink: {
			setEnabled: vi.fn(async () => undefined),
			inspect: vi.fn(),
			link: vi.fn(),
			unlink: vi.fn(),
		},
	},
}));

const inspect = aoBridge.cliLink.inspect as ReturnType<typeof vi.fn>;
const link = aoBridge.cliLink.link as ReturnType<typeof vi.fn>;

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	inspect.mockReset();
	link.mockReset();
	inspect.mockResolvedValue({
		ok: true,
		status: {
			linkPath: "/tmp/bin/ao",
			proposedTarget: "/Apps/Agent Orchestrator.app/Contents/Resources/daemon/ao",
			proposedVersion: "9.1.0",
			current: { kind: "stale", target: "/Apps/Old.app/Contents/Resources/daemon/ao" },
		},
	});
	link.mockResolvedValue({ ok: true, changed: true, linkPath: "/tmp/bin/ao", targetPath: "/Apps/Agent Orchestrator.app/Contents/Resources/daemon/ao", version: "9.1.0" });
});

describe("CliLinkSettings", () => {
	it("hides the action when developer mode is off", () => {
		render(<CliLinkSettings enabled={false} />);
		expect(screen.queryByText("Global ao command")).not.toBeInTheDocument();
		expect(inspect).not.toHaveBeenCalled();
	});

	it("shows the current and proposed targets and confirms before relinking", async () => {
		const user = userEvent.setup();
		render(<CliLinkSettings enabled={true} />);
		expect(await screen.findByText(/Current target: \/Apps\/Old.app/)).toBeInTheDocument();
		expect(screen.getByText(/Proposed target: \/Apps\/Agent Orchestrator.app/)).toBeInTheDocument();
		expect(screen.getByText(/9.1.0/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Relink" }));
		expect(link).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Confirm" }));
		expect(link).toHaveBeenCalledWith(true);
	});

	it("offers no replacement when the current command is not an AO link", async () => {
		inspect.mockResolvedValue({
			ok: true,
			status: {
				linkPath: "/tmp/bin/ao",
				proposedTarget: "/Apps/Agent Orchestrator.app/Contents/Resources/daemon/ao",
				proposedVersion: "9.1.0",
				current: { kind: "foreign", target: null },
			},
		});
		render(<CliLinkSettings enabled={true} />);
		expect(await screen.findByText(/left unchanged/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Link" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Relink" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Unlink" })).not.toBeInTheDocument();
	});
});
