import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { BrowserChromeSection, openChromeAgentsSettings } from "./BrowserChromeSection";

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<BrowserChromeSection />
		</QueryClientProvider>,
	);
}

const base = {
	extensionId: "eefgahhblbmjpccfcnpkhkadgcccnibg",
	extensionPath: "/Users/me/.ao/data/browser-chrome/extension",
	extensionBundled: true,
	hostPath: "/Users/me/.ao/data/browser-chrome/ao-browser-host",
};
const notSetUp = { ...base, enabled: false, connected: false, channels: [{ name: "Google Chrome", available: true, registered: false, manifestPath: "/m" }] };
const setUp = { ...base, enabled: true, connected: false, channels: [{ name: "Google Chrome", available: true, registered: true, manifestPath: "/m" }] };
const connected = { ...setUp, connected: true, clientVersion: "0.1.0" };

describe("BrowserChromeSection", () => {
	beforeEach(() => useUiStore.setState({ developerMode: true }));
	afterEach(() => {
		vi.restoreAllMocks();
		useUiStore.setState({ developerMode: false });
	});

	it("stays hidden outside Developer mode until Chrome mode is set up", async () => {
		useUiStore.setState({ developerMode: false });
		const get = vi.spyOn(apiClient, "GET").mockResolvedValue({ data: notSetUp } as never);
		const { unmount } = renderSection();
		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(screen.queryByText("Agents in your Chrome")).not.toBeInTheDocument();
		unmount();

		// Once set up, it stays reachable so the user can remove it.
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: connected } as never);
		renderSection();
		expect(await screen.findByText("Connected · extension 0.1.0")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
	});

	it("opens the Browser settings and scrolls to the section", async () => {
		const open = vi.fn();
		const scrollIntoView = vi.fn();
		const target = document.createElement("section");
		target.dataset.section = "browserChrome";
		target.scrollIntoView = scrollIntoView;
		openChromeAgentsSettings(open);
		expect(open).toHaveBeenCalledWith("browserProfiles");
		document.body.append(target);
		await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: "start", behavior: "smooth" }));
		target.remove();
	});

	it("sets up Chrome mode and shows how to load the extension", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: notSetUp } as never);
		const post = vi.spyOn(apiClient, "POST").mockResolvedValue({ data: setUp } as never);
		renderSection();

		expect(await screen.findByText("Not set up")).toBeInTheDocument();
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: setUp } as never);
		fireEvent.click(screen.getByRole("button", { name: "Set up" }));

		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/browser/chrome"));
		expect(await screen.findByText("Set up · extension not connected")).toBeInTheDocument();
		expect(screen.getByText("Google Chrome")).toBeInTheDocument();
		expect(screen.getByText(base.extensionPath)).toBeInTheDocument();
		expect(screen.getByText(base.extensionId)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
	});

	it("reports a connected extension without load instructions", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: connected } as never);
		renderSection();
		expect(await screen.findByText("Connected · extension 0.1.0")).toBeInTheDocument();
		expect(screen.queryByText(base.extensionPath)).not.toBeInTheDocument();
	});

	it("shows setup failures", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: notSetUp } as never);
		vi.spyOn(apiClient, "POST").mockResolvedValue({
			error: { error: { code: "BROWSER_CHROME_SETUP_FAILED", message: "google Chrome user data was not found" } },
		} as never);
		renderSection();
		await screen.findByText("Not set up");
		fireEvent.click(screen.getByRole("button", { name: "Set up" }));
		expect(await screen.findByText(/Setup failed:/)).toBeInTheDocument();
	});
});
