import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { ActivityRow } from "./ChatTimelineItems";
import { ChatActivitySource } from "./FullActivity";
import { conversationQueryOptions } from "../../hooks/useConversation";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

const get = vi.fn();
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get }) }));

const fullOutput = "ok  pkg/a\n" + "line\n".repeat(200) + "THE LAST LINE\n";

const preview: ConversationActivity = {
	kind: "activity",
	id: "act-1",
	sequence: 4,
	revision: 3,
	activityKind: "command",
	status: "completed",
	summary: "go test ./...",
	detail: { command: "go test ./...", output: "ok  pkg/a\n", exitCode: 0 },
	detailOmitted: true,
	detailBytes: 1200,
	createdAt: "2026-10-26T00:00:00Z",
};

const fullWire = {
	kind: "activity",
	id: "act-1",
	sequence: 4,
	revision: 3,
	activityKind: "command",
	status: "completed",
	summary: "go test ./...",
	detail: { command: "go test ./...", rawCommand: "/bin/zsh -lc 'go test ./...'", output: fullOutput, exitCode: 0 },
	createdAt: "2026-10-26T00:00:00Z",
};

function renderRow(ui: ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ChatActivitySource.Provider value={{ sessionId: "s1" }}>{ui}</ChatActivitySource.Provider>
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return { ...view, client };
}

const toggle = () => screen.getByRole("button", { name: /ran command/i });

describe("activity previews", () => {
	beforeEach(() => {
		get.mockReset();
	});

	it("reads the full row only when a preview row opens, then reuses it", async () => {
		get.mockResolvedValue({ data: fullWire });
		renderRow(<ActivityRow activity={preview} />);
		expect(get).not.toHaveBeenCalled();

		fireEvent.click(toggle());
		expect(screen.getByRole("status")).toHaveTextContent("Loading details…");
		expect(await screen.findByText(/THE LAST LINE/)).toBeInTheDocument();
		expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/conversation/activities/{activityId}", {
			params: { path: { sessionId: "s1", activityId: "act-1" } },
		});

		fireEvent.click(toggle());
		fireEvent.click(toggle());
		expect(screen.getByText(/THE LAST LINE/)).toBeInTheDocument();
		expect(get).toHaveBeenCalledTimes(1);
	});

	it("offers a retry when the full row fails to load", async () => {
		get.mockResolvedValueOnce({ error: { code: "INTERNAL", error: "boom" } }).mockResolvedValueOnce({ data: fullWire });
		renderRow(<ActivityRow activity={preview} />);

		fireEvent.click(toggle());
		fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
		expect(await screen.findByText(/THE LAST LINE/)).toBeInTheDocument();
		expect(get).toHaveBeenCalledTimes(2);
	});

	it("never fetches a row that arrived whole", () => {
		const whole = { ...preview, detailOmitted: undefined, detail: { ...preview.detail, output: fullOutput } };
		renderRow(<ActivityRow activity={whole} />);
		fireEvent.click(toggle());
		expect(screen.getByText(/THE LAST LINE/)).toBeInTheDocument();
		expect(get).not.toHaveBeenCalled();
	});

	it("keeps the streamed body on screen while a row that settled open loads its full copy", async () => {
		let resolve: (value: unknown) => void = () => {};
		get.mockReturnValue(new Promise((done) => { resolve = done; }));
		const running = { ...preview, status: "running" as const, revision: 2, detailOmitted: undefined, detail: { command: "go test ./...", output: fullOutput } };
		const { rerender, client } = renderRow(<ActivityRow activity={running} />);
		expect(screen.getByText(/THE LAST LINE/)).toBeInTheDocument();
		fireEvent.click(toggle());
		fireEvent.click(toggle());

		rerender(
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<ChatActivitySource.Provider value={{ sessionId: "s1" }}>
						<ActivityRow activity={preview} />
					</ChatActivitySource.Provider>
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		expect(screen.getByText(/THE LAST LINE/)).toBeInTheDocument();
		resolve({ data: fullWire });
		await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
	});

	it("asks for previews and reads an empty summary as the command", async () => {
		get.mockResolvedValue({
			data: {
				sessionId: "s1", conversationId: "c1", controller: "ready", latestSequence: 4, oldestSequence: 1, hasMoreBefore: false,
				turns: [], messages: [], branchPoints: [], capabilities: [],
				activities: [{ ...fullWire, summary: "", detail: { command: "go test ./...", output: "ok" }, detailOmitted: true, detailBytes: 900 }],
			},
		});
		const options = conversationQueryOptions("s1");
		const page = await options.queryFn!({ pageParam: undefined } as never);
		expect(get.mock.calls[0]![1].params.query).toMatchObject({ activityDetail: "preview" });
		const activity = page.items.find((item) => item.kind === "activity") as ConversationActivity;
		expect(activity).toMatchObject({ summary: "go test ./...", detailOmitted: true, detailBytes: 900 });
	});
});
