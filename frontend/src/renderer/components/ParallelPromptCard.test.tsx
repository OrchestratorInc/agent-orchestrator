import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ParallelPromptCard } from "./ParallelPromptCard";
import { TooltipProvider } from "./ui/tooltip";
import { resetParallelPromptForTest, shouldOfferParallelPrompt } from "../lib/parallel-prompt";

const { getMock, postMock, capture } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	capture: vi.fn(async (_event: string, _properties?: Record<string, unknown>) => true),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: (error: unknown, fallback?: string) =>
		(error as { message?: string } | undefined)?.message ?? fallback ?? "error",
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: capture }));

function renderCard(onDismiss = vi.fn()) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ParallelPromptCard projectId="ao" onDismiss={onDismiss} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onDismiss;
}

function actions(event: string) {
	return capture.mock.calls.filter(([name]) => name === event).map(([, props]) => props);
}

describe("ParallelPromptCard", () => {
	beforeEach(() => {
		window.localStorage.clear();
		resetParallelPromptForTest();
		capture.mockClear();
		postMock.mockReset();
		getMock.mockReset();
		getMock.mockResolvedValue({
			data: {
				suggestions: [
					{ source: "issue", issueNumber: 12, title: "Fix login", url: "https://github.com/acme/repo/issues/12" },
					{ source: "todo", path: "src/a.go", line: 3, text: "handle retries" },
				],
			},
		});
		let worker = 0;
		postMock.mockImplementation(async () => ({ data: { ok: true, workerId: `ao-${++worker}` } }));
	});

	it("offers issues, notes and a starter task, and reports only counts", async () => {
		renderCard();
		const list = await screen.findByRole("list");
		expect(within(list).getAllByRole("listitem")).toHaveLength(3);
		expect(screen.getByText("Fix login")).toBeInTheDocument();
		expect(screen.getByText("handle retries")).toBeInTheDocument();
		expect(screen.getByText("src/a.go:3")).toBeInTheDocument();
		expect(actions("ao.onboarding.parallel_prompt_shown")).toEqual([{ suggestion_count: 3, source: "mixed" }]);
	});

	it("starts one suggestion as a worker tagged with its source", async () => {
		renderCard();
		const row = (await screen.findByText("Fix login")).closest("li") as HTMLElement;
		await userEvent.click(within(row).getByRole("button", { name: "Start" }));
		await waitFor(() => expect(screen.queryByText("Fix login")).not.toBeInTheDocument());
		expect(postMock).toHaveBeenCalledTimes(1);
		const body = postMock.mock.calls[0][1].body;
		expect(body).toMatchObject({ projectId: "ao", spawnSource: "parallel_prompt" });
		expect(body.brief).toContain("#12");
		expect(body.brief).toContain("https://github.com/acme/repo/issues/12");
		expect(actions("ao.onboarding.parallel_prompt_action")).toEqual([{ action: "spawn_one", count: 1 }]);
	});

	it("starts an edited prompt", async () => {
		renderCard();
		const row = (await screen.findByText("handle retries")).closest("li") as HTMLElement;
		await userEvent.click(within(row).getByRole("button", { name: "Edit prompt" }));
		const prompt = within(row).getByRole("textbox", { name: "Prompt" });
		await userEvent.clear(prompt);
		await userEvent.type(prompt, "Add a retry budget to the client");
		await userEvent.click(within(row).getByRole("button", { name: "Start" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0][1].body.brief).toBe("Add a retry budget to the client");
		expect(actions("ao.onboarding.parallel_prompt_action")).toEqual([
			{ action: "edit", count: 1 },
			{ action: "spawn_one", count: 1 },
		]);
	});

	it("starts all in turn and stops at the first refusal", async () => {
		postMock
			.mockResolvedValueOnce({ data: { ok: true, workerId: "ao-2" } })
			.mockResolvedValueOnce({ error: { message: "Sign in to Codex first" } });
		renderCard();
		await userEvent.click(await screen.findByRole("button", { name: "Start all" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Sign in to Codex first");
		expect(postMock).toHaveBeenCalledTimes(2);
		expect(screen.getAllByRole("listitem")).toHaveLength(2);
		expect(actions("ao.onboarding.parallel_prompt_action")).toEqual([{ action: "spawn_all", count: 1 }]);
	});

	it("falls back to starter tasks when the lookup fails", async () => {
		getMock.mockResolvedValue({ error: { message: "offline" } });
		renderCard();
		expect(await screen.findByText("Find and fix a small bug")).toBeInTheDocument();
		expect(actions("ao.onboarding.parallel_prompt_shown")).toEqual([{ suggestion_count: 3, source: "starter" }]);
	});

	it("dismisses for good", async () => {
		const onDismiss = renderCard();
		await userEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
		expect(onDismiss).toHaveBeenCalled();
		expect(shouldOfferParallelPrompt(1)).toBe(false);
		expect(actions("ao.onboarding.parallel_prompt_action")).toEqual([{ action: "dismiss", count: 3 }]);
	});
});
