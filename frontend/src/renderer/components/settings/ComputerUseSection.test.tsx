import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
const bridgeMocks = vi.hoisted(() => ({ openExternal: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: apiMocks.get, POST: apiMocks.post },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		typeof error === "object" && error && "message" in error ? String((error as { message: unknown }).message) : fallback,
}));

vi.mock("../../lib/bridge", () => ({ aoBridge: { app: { openExternal: bridgeMocks.openExternal } } }));

const settingsMocks = vi.hoisted(() => ({ computerUse: false, update: vi.fn(), error: undefined as string | undefined }));

vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { computerUse: settingsMocks.computerUse }, isLoading: false }),
	useUpdateComputerUse: () => ({ update: settingsMocks.update, saving: false, error: settingsMocks.error }),
}));

import { ComputerUseSection } from "./ComputerUseSection";

type Status = Record<string, unknown>;

const notInstalled: Status = { platform: "darwin", installed: false, updateAvailable: false, daemon: "unknown", imageDirectory: "/Users/me/.ao/data/computer-use" };
const installed: Status = {
	platform: "darwin",
	installed: true,
	binaryPath: "/Users/me/.local/bin/cua-driver",
	version: "0.28.2",
	latestVersion: "0.28.2",
	updateAvailable: false,
	daemon: "running",
	permissions: { accessibility: "granted", screenRecording: "granted", granting: false },
	imageDirectory: "/Users/me/.ao/data/computer-use",
};

function daemon({ status, job = { target: "cua-driver", status: "idle", command: "/bin/bash <downloaded from https://cua.ai/driver/install.sh>" } }: { status: () => Status; job?: Record<string, unknown> | (() => Record<string, unknown>) }) {
	apiMocks.get.mockImplementation(async (path: string) => {
		if (path === "/api/v1/computer-use/status") return { data: status(), error: undefined };
		if (path === "/api/v1/system/install/{target}") return { data: typeof job === "function" ? job() : job, error: undefined };
		return { data: undefined, error: { message: `unexpected ${path}` } };
	});
}

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<ComputerUseSection />
		</QueryClientProvider>,
	);
	return client;
}

beforeEach(() => {
	settingsMocks.computerUse = false;
	settingsMocks.update.mockReset();
	settingsMocks.error = undefined;
	apiMocks.get.mockReset();
	apiMocks.post.mockReset();
	bridgeMocks.openExternal.mockReset();
});

describe("ComputerUseSection", () => {
	it("offers the official installer when Cua Driver is missing", async () => {
		daemon({ status: () => notInstalled });
		apiMocks.post.mockResolvedValue({ data: { target: "cua-driver", status: "running" }, error: undefined });
		renderSection();

		const install = await screen.findByRole("button", { name: "Install · Official installer" });
		expect(screen.getByText("Lets agents see the screen and operate desktop apps.")).toBeInTheDocument();
		expect(screen.queryByText("Background service")).not.toBeInTheDocument();

		await userEvent.click(install);
		expect(apiMocks.post).toHaveBeenCalledWith("/api/v1/system/install/{target}", { params: { path: { target: "cua-driver" } } });
		expect(await screen.findByRole("status")).toHaveTextContent("Installing…");
	});

	it("refreshes status when a running install finishes", async () => {
		let current = notInstalled;
		let job: Record<string, unknown> = { target: "cua-driver", status: "running" };
		daemon({ status: () => current, job: () => job });
		renderSection();
		expect(await screen.findByRole("status")).toHaveTextContent("Installing…");

		current = installed;
		job = { target: "cua-driver", status: "succeeded", output: "installed cua-driver" };
		expect(await screen.findByText("0.28.2", {}, { timeout: 4_000 })).toBeInTheDocument();
		expect(screen.getByText("Installed")).toBeInTheDocument();
		expect(screen.queryByText("Background service")).not.toBeInTheDocument();
	});

	it("turns agent access on once a fresh install succeeds", async () => {
		let current = notInstalled;
		let job: Record<string, unknown> = { target: "cua-driver", status: "idle" };
		daemon({ status: () => current, job: () => job });
		apiMocks.post.mockImplementation(async () => {
			job = { target: "cua-driver", status: "running" };
			return { data: job, error: undefined };
		});
		renderSection();

		await userEvent.click(await screen.findByRole("button", { name: "Install · Official installer" }));
		expect(settingsMocks.update).not.toHaveBeenCalled();
		current = installed;
		job = { target: "cua-driver", status: "succeeded" };
		await waitFor(() => expect(settingsMocks.update).toHaveBeenCalledWith(true), { timeout: 4_000 });
	});

	it("leaves agent access alone after an update", async () => {
		let current: Status = { ...installed, latestVersion: "0.37.0", updateAvailable: true };
		let job: Record<string, unknown> = { target: "cua-driver", status: "idle" };
		daemon({ status: () => current, job: () => job });
		apiMocks.post.mockImplementation(async () => {
			job = { target: "cua-driver", status: "running" };
			return { data: job, error: undefined };
		});
		renderSection();

		await userEvent.click(await screen.findByRole("button", { name: "Update to 0.37.0" }));
		current = { ...installed, version: "0.37.0", latestVersion: "0.37.0" };
		job = { target: "cua-driver", status: "succeeded" };
		expect(await screen.findByText("0.37.0", {}, { timeout: 4_000 })).toBeInTheDocument();
		expect(settingsMocks.update).not.toHaveBeenCalled();
	});

	it("shows the driver's state once installed and offers an update", async () => {
		daemon({ status: () => ({ ...installed, latestVersion: "0.37.0", updateAvailable: true, releaseNotesUrl: "https://example.test/notes" }) });
		apiMocks.post.mockResolvedValue({ data: { target: "cua-driver", status: "running" }, error: undefined });
		renderSection();

		const update = await screen.findByRole("button", { name: "Update to 0.37.0" });
		expect(screen.queryByText("Background service")).not.toBeInTheDocument();
		expect(screen.getByText("macOS permissions")).toBeInTheDocument();
		expect(screen.queryByText(/Accessibility: Granted/)).not.toBeInTheDocument();
		expect(screen.queryByText("/Users/me/.local/bin/cua-driver")).not.toBeInTheDocument();
		expect(screen.getByRole("switch", { name: "Let agents use this computer" })).toBeEnabled();

		await userEvent.click(update);
		expect(apiMocks.post).toHaveBeenCalledWith("/api/v1/system/install/{target}", { params: { path: { target: "cua-driver" } } });
	});

	it("starts a stopped background service", async () => {
		daemon({ status: () => ({ ...installed, daemon: "stopped", permissions: { accessibility: "unknown", screenRecording: "unknown", granting: false } }) });
		apiMocks.post.mockResolvedValue({ data: installed, error: undefined });
		renderSection();

		expect(await screen.findByText("The background service isn't running.")).toBeInTheDocument();
		expect(screen.getByText("Start the background service to check them.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Start" }));

		expect(apiMocks.post).toHaveBeenCalledWith("/api/v1/computer-use/daemon/start");
		await waitFor(() => expect(screen.queryByText("Background service")).not.toBeInTheDocument());
	});

	it("asks macOS for missing permissions and follows the request", async () => {
		daemon({ status: () => ({ ...installed, permissions: { accessibility: "granted", screenRecording: "missing", granting: false } }) });
		apiMocks.post.mockResolvedValue({ data: { ...installed, permissions: { accessibility: "granted", screenRecording: "missing", granting: true } }, error: undefined });
		renderSection();

		expect(await screen.findByText("Accessibility: Granted · Screen Recording: Missing")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Grant" }));

		expect(apiMocks.post).toHaveBeenCalledWith("/api/v1/computer-use/permissions/grant");
		expect(await screen.findByRole("status")).toHaveTextContent("Waiting for System Settings…");
	});

	it("hands over the command when AO cannot run the installer here", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
		daemon({ status: () => ({ ...notInstalled, platform: "linux" }), job: { target: "cua-driver", status: "unsupported", command: "curl -fsSL https://cua.ai/driver/install.sh | bash", error: "bash was not found on PATH. Copy the install command and run it in a terminal, or follow https://cua.ai/docs/how-to-guides/driver/install." } });
		renderSection();

		expect(await screen.findByText(/^bash was not found on PATH\. Copy the install command and run it in a terminal, or follow https:\/\/cua\.ai\/docs/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Copy command" }));
		expect(writeText).toHaveBeenCalledWith("curl -fsSL https://cua.ai/driver/install.sh | bash");
	});

	it("hands over the command for an update AO cannot run here", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
		const unsupported = { target: "cua-driver", status: "unsupported", command: "curl -fsSL https://cua.ai/driver/install.sh | bash", error: "bash was not found on PATH." };
		daemon({ status: () => ({ ...installed, platform: "linux", latestVersion: "0.37.0", updateAvailable: true }), job: unsupported });
		renderSection();

		expect(await screen.findByText("bash was not found on PATH.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Update to 0.37.0" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Open installation guide" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Copy command" }));
		expect(writeText).toHaveBeenCalledWith("curl -fsSL https://cua.ai/driver/install.sh | bash");
	});

	it("ignores an unsupported installer once Cua Driver is current", async () => {
		daemon({ status: () => ({ ...installed, platform: "linux" }), job: { target: "cua-driver", status: "unsupported", command: "curl -fsSL https://cua.ai/driver/install.sh | bash", error: "bash was not found on PATH." } });
		renderSection();

		expect(await screen.findByText("Installed")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Copy command" })).not.toBeInTheDocument();
		expect(screen.queryByText("bash was not found on PATH.")).not.toBeInTheDocument();
	});

	it("shows a failed install without its raw output", async () => {
		daemon({ status: () => notInstalled, job: { target: "cua-driver", status: "failed", error: "exit status 1", output: "error: /Applications is not writable", command: "/bin/bash <downloaded from https://cua.ai/driver/install.sh>" } });
		renderSection();

		expect(await screen.findByText("exit status 1")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retry install" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show diagnostics" })).not.toBeInTheDocument();
		expect(screen.queryByText(/\/Applications is not writable/)).not.toBeInTheDocument();
	});

	it("reports an action failure next to the section", async () => {
		daemon({ status: () => ({ ...installed, daemon: "stopped" }) });
		apiMocks.post.mockResolvedValue({ data: undefined, error: { message: "Could not start Cua Driver's background service: no desktop session" } });
		renderSection();

		await userEvent.click(await screen.findByRole("button", { name: "Start" }));
		await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("no desktop session"));
	});

	it("turns on the agent standing instruction once Cua Driver is installed", async () => {
		daemon({ status: () => installed });
		renderSection();

		await screen.findByText("0.28.2");
		const toggle = screen.getByRole("switch", { name: "Let agents use this computer" });
		expect(toggle).not.toBeChecked();
		expect(screen.getByText(/New chat sessions get tools to read the screen and operate desktop apps/)).toBeInTheDocument();
		await userEvent.click(toggle);
		expect(settingsMocks.update).toHaveBeenCalledWith(true);
	});

	it("needs Cua Driver installed to turn agent access on", async () => {
		daemon({ status: () => notInstalled });
		renderSection();

		expect(await screen.findByRole("switch", { name: "Let agents use this computer" })).toBeDisabled();
		expect(screen.getByText("Install Cua Driver to turn this on.")).toBeInTheDocument();
	});

	it("can always turn agent access off, even without the driver", async () => {
		settingsMocks.computerUse = true;
		daemon({ status: () => notInstalled });
		renderSection();

		const toggle = await screen.findByRole("switch", { name: "Let agents use this computer" });
		expect(toggle).toBeChecked();
		expect(toggle).toBeEnabled();
		await userEvent.click(toggle);
		expect(settingsMocks.update).toHaveBeenCalledWith(false);
	});

	it("reports a failed toggle save", async () => {
		settingsMocks.error = "database is locked";
		daemon({ status: () => installed });
		renderSection();

		expect(await screen.findByRole("alert")).toHaveTextContent("database is locked");
	});
});
