import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../../i18n";

const apiClient = vi.hoisted(() => ({
	POST: vi.fn(),
}));
const apiErrorMessage = vi.hoisted(() => (error: unknown) => String(error));
const showGlobalToast = vi.hoisted(() => vi.fn());

vi.mock("../../lib/api-client", () => ({
	apiClient,
	apiErrorMessage,
}));
vi.mock("../../stores/ui-store", () => ({
	useUiStore: { getState: () => ({ showGlobalToast }) },
}));

import { GatewayApplySessionsDialog } from "./GatewayApplySessionsDialog";

const stale = [
	{ sessionId: "mer-1", displayName: "mer-1", mode: "tui", stampBaseUrl: "https://old.example" },
	{ sessionId: "mer-2", displayName: "Chat helper", mode: "chat", stampBaseUrl: "https://old.example" },
];

function renderDialog() {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(
		<QueryClientProvider client={client}>
			<GatewayApplySessionsDialog sessions={stale} onClose={vi.fn()} />
		</QueryClientProvider>,
	);
}

describe("GatewayApplySessionsDialog", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("lists stale sessions and posts their ids on confirm", async () => {
		apiClient.POST.mockResolvedValue({
			data: {
				ok: true,
				results: [
					{ sessionId: "mer-1", state: "applied" },
					{ sessionId: "mer-2", state: "applied" },
				],
			},
			error: undefined,
		});
		const user = userEvent.setup();
		renderDialog();
		expect(screen.getByText(/mer-1 \(tui\)/)).toBeInTheDocument();
		expect(screen.getByText(/Chat helper \(chat\)/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Apply now" }));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledTimes(1));
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/sessions/apply-provider", {
			body: { sessionIds: ["mer-1", "mer-2"] },
		});
		expect(showGlobalToast).toHaveBeenCalledWith(
			"Provider applied to 2 sessions",
			undefined,
			"info",
		);
	});

	it("toasts skipped and failed results with the session name", async () => {
		apiClient.POST.mockResolvedValue({
			data: {
				ok: true,
				results: [
					{ sessionId: "mer-1", state: "applied" },
					{ sessionId: "mer-2", state: "failed", error: "resume boom" },
				],
			},
			error: undefined,
		});
		const user = userEvent.setup();
		renderDialog();
		await user.click(screen.getByRole("button", { name: "Apply now" }));
		await waitFor(() => expect(showGlobalToast).toHaveBeenCalledTimes(2));
		expect(showGlobalToast).toHaveBeenCalledWith(
			"Provider applied to 1 session",
			undefined,
			"info",
		);
		expect(showGlobalToast).toHaveBeenCalledWith(
			"Could not switch Chat helper",
			"resume boom",
			"error",
		);
	});

	it("surfaces an API error inside the dialog and keeps it open", async () => {
		apiClient.POST.mockResolvedValue({ data: undefined, error: "daemon down" });
		const user = userEvent.setup();
		renderDialog();
		await user.click(screen.getByRole("button", { name: "Apply now" }));
		expect(await screen.findByText("daemon down")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Apply now" })).toBeInTheDocument();
		expect(showGlobalToast).not.toHaveBeenCalled();
	});
});
