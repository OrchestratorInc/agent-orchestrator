import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GeneralSettingsSection } from "./GeneralSettingsSection";

function renderGeneral() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<GeneralSettingsSection />
		</QueryClientProvider>,
	);
}

describe("GeneralSettingsSection zoom row", () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	// REGRESSION #6373: a persisted 189% zoom on a 1x display made Settings
	// text look soft, and nothing in the app showed that the window was zoomed.
	it("shows a non-100% zoom with its cause and resets the shell zoom", async () => {
		let emitZoom: (factor: number) => void = () => undefined;
		vi.spyOn(window.ao.window, "getZoomFactor").mockResolvedValue(1.2 ** 3.5);
		vi.spyOn(window.ao.window, "onZoomFactor").mockImplementation((listener) => {
			emitZoom = listener;
			return () => undefined;
		});
		const resetZoom = vi.spyOn(window.ao.window, "resetZoom").mockImplementation(async () => {
			emitZoom(1);
		});
		const menuAction = vi.spyOn(window.ao.menu, "action");

		renderGeneral();

		expect(await screen.findByText("189%")).toBeInTheDocument();
		expect(screen.getByText(/Text can look soft at zoom levels other than 100%/)).toBeInTheDocument();

		await act(async () => {
			fireEvent.click(screen.getByRole("button", { name: "Reset to 100%" }));
		});

		// Reset targets the shell, not the last-focused browser panel.
		expect(resetZoom).toHaveBeenCalledOnce();
		expect(menuAction).not.toHaveBeenCalled();
		expect(screen.getByText("100%")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Reset to 100%" })).not.toBeInTheDocument();
		expect(screen.queryByText(/Text can look soft/)).not.toBeInTheDocument();
	});

	it("shows 100% without a reset action or hint", async () => {
		renderGeneral();

		expect(await screen.findByText("100%")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Reset to 100%" })).not.toBeInTheDocument();
	});
});
