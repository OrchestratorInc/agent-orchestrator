import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AcquisitionChannelState } from "../../../shared/acquisition-channel";

const mocks = vi.hoisted(() => ({
	state: "pending" as unknown,
	record: vi.fn(async (_answer: string) => true),
	capture: vi.fn(async (_event: string, _props?: unknown) => true),
	steps: [] as Array<[string, string]>,
}));

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		appState: {
			getAcquisitionChannel: async () => mocks.state,
			recordAcquisitionChannel: mocks.record,
		},
	},
}));
vi.mock("../../lib/telemetry", () => ({ captureRendererEvent: mocks.capture }));
vi.mock("../../lib/onboarding-telemetry", () => ({
	reportOnboardingStep: (step: string, phase: string) => mocks.steps.push([step, phase]),
	useOnboardingStep: (step: string, active: boolean) => {
		if (active) mocks.steps.push([step, "active"]);
		return () => mocks.steps.push([step, "completed"]);
	},
}));

import { AttributionQuestion } from "./AttributionQuestion";

function renderQuestion(state: AcquisitionChannelState) {
	mocks.state = state;
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<AttributionQuestion />
		</QueryClientProvider>,
	);
}

describe("AttributionQuestion", () => {
	beforeEach(() => {
		mocks.record.mockReset();
		mocks.record.mockResolvedValue(true);
		mocks.capture.mockClear();
		mocks.steps = [];
	});

	it("offers every channel and a skip, with no free-text field", async () => {
		renderQuestion("pending");
		expect(await screen.findByText("How did you hear about Orchestrator?")).toBeInTheDocument();
		const group = screen.getByRole("group", { name: "How did you hear about Orchestrator?" });
		expect(group.querySelectorAll("button")).toHaveLength(11);
		expect(screen.getByRole("button", { name: "X (Twitter)" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Conference or event" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Skip" })).toBeInTheDocument();
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		expect(mocks.steps).toContainEqual(["attribution", "active"]);
	});

	it.each<AcquisitionChannelState>([null, "skipped", "github"])("stays hidden when the state is %s", async (state) => {
		renderQuestion(state);
		await waitFor(() => expect(mocks.steps).toEqual([]));
		expect(screen.queryByTestId("attribution-question")).not.toBeInTheDocument();
	});

	it("records a channel, sends one event and disappears", async () => {
		renderQuestion("pending");
		fireEvent.click(await screen.findByRole("button", { name: "Hacker News" }));
		await waitFor(() => expect(screen.queryByTestId("attribution-question")).not.toBeInTheDocument());
		expect(mocks.record).toHaveBeenCalledWith("hacker_news");
		await waitFor(() =>
			expect(mocks.capture).toHaveBeenCalledWith("ao.onboarding.attribution_answered", { channel: "hacker_news", skipped: false }),
		);
		expect(mocks.steps).not.toContainEqual(["attribution", "skipped"]);
	});

	it("records a skip as a skipped step without a channel", async () => {
		renderQuestion("pending");
		fireEvent.click(await screen.findByRole("button", { name: "Skip" }));
		await waitFor(() => expect(screen.queryByTestId("attribution-question")).not.toBeInTheDocument());
		expect(mocks.record).toHaveBeenCalledWith("skipped");
		expect(mocks.steps).toContainEqual(["attribution", "skipped"]);
		await waitFor(() => expect(mocks.capture).toHaveBeenCalledWith("ao.onboarding.attribution_answered", { skipped: true }));
	});

	it("sends nothing when the marker was already answered elsewhere", async () => {
		mocks.record.mockResolvedValue(false);
		renderQuestion("pending");
		fireEvent.click(await screen.findByRole("button", { name: "Reddit" }));
		await waitFor(() => expect(mocks.record).toHaveBeenCalledOnce());
		await Promise.resolve();
		expect(mocks.capture).not.toHaveBeenCalled();
	});
});
