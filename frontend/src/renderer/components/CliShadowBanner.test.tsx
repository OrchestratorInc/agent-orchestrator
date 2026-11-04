import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CliShadowStatus } from "../../shared/cli-shadow";
import { aoBridge } from "../lib/bridge";
import { CliShadowBanner } from "./CliShadowBanner";

const shadowed: CliShadowStatus = {
	kind: "shadowed",
	shadowPath: "/Users/me/.local/bin/ao",
	shadowVersion: "ao version dev",
	bundledPath: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao",
};

function mockStatus(status: CliShadowStatus) {
	return vi.spyOn(aoBridge.cliShadow, "inspect").mockResolvedValue(status);
}

describe("CliShadowBanner", () => {
	beforeEach(() => window.localStorage.clear());
	afterEach(() => vi.restoreAllMocks());

	it("names both paths and the other ao's version", async () => {
		mockStatus(shadowed);
		render(<CliShadowBanner />);
		const banner = await screen.findByRole("status");
		expect(banner).toHaveTextContent("Terminals outside AO run a different ao");
		expect(banner).toHaveTextContent("/Users/me/.local/bin/ao (ao version dev)");
		expect(banner).toHaveTextContent("/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao");
	});

	it("says so when the version is unknown", async () => {
		mockStatus({ ...shadowed, shadowVersion: null } as CliShadowStatus);
		render(<CliShadowBanner />);
		expect(await screen.findByRole("status")).toHaveTextContent("(version unknown)");
	});

	it.each(["match", "absent", "unknown"] as const)("renders nothing on %s", async (kind) => {
		const inspect = mockStatus({ kind });
		const { container } = render(<CliShadowBanner />);
		await waitFor(() => expect(inspect).toHaveBeenCalled());
		await Promise.resolve();
		expect(container).toBeEmptyDOMElement();
	});

	it("renders nothing when the check fails", async () => {
		const inspect = vi.spyOn(aoBridge.cliShadow, "inspect").mockRejectedValue(new Error("no handler"));
		const { container } = render(<CliShadowBanner />);
		await waitFor(() => expect(inspect).toHaveBeenCalled());
		await Promise.resolve();
		expect(container).toBeEmptyDOMElement();
	});

	it("stays dismissed for the same path across launches", async () => {
		mockStatus(shadowed);
		const first = render(<CliShadowBanner />);
		fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		first.unmount();

		const inspect = mockStatus(shadowed);
		render(<CliShadowBanner />);
		await waitFor(() => expect(inspect).toHaveBeenCalled());
		await Promise.resolve();
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
	});

	it("warns again when a different ao appears after a dismissal", async () => {
		mockStatus(shadowed);
		const first = render(<CliShadowBanner />);
		fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
		first.unmount();

		mockStatus({ ...shadowed, shadowPath: "/opt/homebrew/bin/ao" } as CliShadowStatus);
		render(<CliShadowBanner />);
		expect(await screen.findByRole("status")).toHaveTextContent("/opt/homebrew/bin/ao");
	});
});
