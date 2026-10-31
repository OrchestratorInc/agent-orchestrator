import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { InstallCloudflared } from "./InstallCloudflared";

vi.mock("../../lib/api-client", () => ({
	apiClient: {
		POST: vi.fn(),
		GET: vi.fn(),
	},
	apiErrorMessage: () => "failed",
}));

beforeEach(() => {
	vi.clearAllMocks();
	vi.mocked(apiClient.GET).mockResolvedValue({ data: { target: "cloudflared", status: "idle" }, error: undefined } as never);
});

// The daemon starts its own download when remote access needs the connector;
// offering a second install beside it would invite a pointless click.
test("shows the daemon's own download instead of an install button", async () => {
	vi.mocked(apiClient.GET).mockResolvedValue({
		data: { target: "cloudflared", status: "running", method: "download" },
		error: undefined,
	} as never);
	render(<InstallCloudflared onInstalled={vi.fn()} />);

	expect(await screen.findByText(/downloading cloudflared/i)).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: /install cloudflared/i })).toBeNull();
});

test("offers the package manager only after the download failed", async () => {
	vi.mocked(apiClient.GET).mockResolvedValue({
		data: { target: "cloudflared", status: "failed", method: "download", error: "Could not download cloudflared" },
		error: undefined,
	} as never);
	vi.mocked(apiClient.POST).mockResolvedValue({
		data: { target: "cloudflared", status: "unsupported", command: "sudo apt-get install -y cloudflared" },
		error: undefined,
	} as never);
	render(<InstallCloudflared onInstalled={vi.fn()} />);

	expect(await screen.findByText("Could not download cloudflared")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: /package manager/i }));

	expect(apiClient.POST).toHaveBeenCalledOnce();
	expect(await screen.findByText("sudo apt-get install -y cloudflared")).toBeInTheDocument();
});

test("lets a Linux user check again after running the manual command", async () => {
	vi.mocked(apiClient.POST).mockResolvedValue({
		data: {
			target: "cloudflared",
			status: "unsupported",
			command: "sudo apt-get install -y cloudflared",
			error: "Run this command in a terminal.",
		},
		error: undefined,
	} as never);
	const onInstalled = vi.fn();
	render(<InstallCloudflared onInstalled={onInstalled} />);

	await userEvent.click(screen.getByRole("button", { name: /install cloudflared/i }));
	await userEvent.click(await screen.findByRole("button", { name: /check again/i }));

	expect(onInstalled).toHaveBeenCalledOnce();
});
