import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	GET: vi.fn(),
	POST: vi.fn(),
	DELETE: vi.fn(),
	writeText: vi.fn(),
	openExternal: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.GET, POST: mocks.POST, DELETE: mocks.DELETE },
	apiErrorMessage: (error: unknown, fallback: string) => (error instanceof Error ? error.message : fallback),
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => undefined,
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: { openExternal: mocks.openExternal },
		clipboard: { writeText: mocks.writeText },
	},
}));

import { GitHubSignInGate } from "./GitHubSignInGate";

function respond(satisfied: boolean) {
	mocks.GET.mockImplementation(async (path: string) => {
		if (path === "/api/v1/system/github-auth") {
			return { data: { id: "github-auth", label: "GitHub access", satisfied, required: false } };
		}
		if (path === "/api/v1/github/login") return { data: { state: "idle" } };
		return { data: undefined, error: new Error(`unexpected GET ${path}`) };
	});
}

function renderGate(ui: ReactNode) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

beforeEach(() => {
	vi.clearAllMocks();
	mocks.writeText.mockResolvedValue(undefined);
});

describe("GitHubSignInGate", () => {
	it("stays out of the way once GitHub is connected", async () => {
		respond(true);
		renderGate(<GitHubSignInGate enabled />);

		await waitFor(() => expect(mocks.GET).toHaveBeenCalledWith("/api/v1/system/github-auth"));
		expect(screen.queryByTestId("github-sign-in-gate")).not.toBeInTheDocument();
	});

	it("blocks the app with a device code sign-in until GitHub is connected", async () => {
		respond(false);
		mocks.POST.mockResolvedValue({
			data: { state: "pending", userCode: "ABCD-1234", verificationUri: "https://github.com/login/device", expiresAt: "2026-07-04T10:15:00Z" },
		});
		const user = userEvent.setup();
		renderGate(<GitHubSignInGate enabled />);

		expect(await screen.findByTestId("github-sign-in-gate")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Continue with GitHub" }));
		expect(mocks.POST).toHaveBeenCalledWith("/api/v1/github/login");
		expect(await screen.findByText("ABCD-1234")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Copy code and open GitHub" }));
		expect(mocks.writeText).toHaveBeenCalledWith("ABCD-1234");
		expect(mocks.openExternal).toHaveBeenCalledWith("https://github.com/login/device");
	});

	it("shows what is possible once GitHub is connected, then lets the user in", async () => {
		let signedIn = false;
		mocks.GET.mockImplementation(async (path: string) => {
			if (path === "/api/v1/system/github-auth") {
				return { data: { id: "github-auth", label: "GitHub access", satisfied: signedIn, required: false } };
			}
			if (path === "/api/v1/github/login") {
				return { data: signedIn ? { state: "succeeded", login: "octocat", avatarUrl: "https://avatars.example/octocat" } : { state: "pending", userCode: "ABCD-1234", verificationUri: "https://github.com/login/device" } };
			}
			return { data: undefined, error: new Error(`unexpected GET ${path}`) };
		});
		const user = userEvent.setup();
		renderGate(<GitHubSignInGate enabled />);
		expect(await screen.findByText("ABCD-1234")).toBeInTheDocument();

		signedIn = true;
		expect(await screen.findByText("You're all set", {}, { timeout: 5000 })).toBeInTheDocument();
		expect(screen.getByText("Signed in as @octocat.")).toBeInTheDocument();
		expect(screen.getByTestId("github-sign-in-gate").querySelector('img[src="https://avatars.example/octocat"]')).not.toBeNull();
		expect(screen.getByText(/disconnect GitHub anytime in Settings/)).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Get started" }));
		await waitFor(() => expect(screen.queryByTestId("github-sign-in-gate")).not.toBeInTheDocument());
	});

	it("shows why sign-in could not start", async () => {
		respond(false);
		mocks.POST.mockResolvedValue({ data: undefined, error: new Error("GitHub sign-in is not configured in this build.") });
		const user = userEvent.setup();
		renderGate(<GitHubSignInGate enabled />);

		await user.click(await screen.findByRole("button", { name: "Continue with GitHub" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("GitHub sign-in is not configured in this build.");
		expect(screen.getByRole("button", { name: "Try again" })).toBeEnabled();
	});
});
