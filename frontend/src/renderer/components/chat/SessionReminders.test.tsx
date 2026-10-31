import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionReminders } from "./SessionReminders";

const api = vi.hoisted(() => ({ GET: vi.fn(), DELETE: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: api,
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

const pending = {
	id: "rem_1",
	sessionId: "s1",
	projectId: "p",
	name: "Nightly",
	message: "Check ao-electron after the nightly build",
	dueAt: "2026-10-31T04:00:00Z",
	createdBy: "agent",
	status: "pending",
	createdAt: "2026-10-30T20:00:00Z",
};

function renderReminders() {
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<SessionReminders sessionId="s1" />
		</QueryClientProvider>,
	);
}

describe("SessionReminders", () => {
	beforeEach(() => {
		api.GET.mockReset();
		api.DELETE.mockReset();
	});

	it("shows nothing when the session has no follow-ups", async () => {
		api.GET.mockResolvedValue({ data: { reminders: [] }, error: undefined });
		const { container } = renderReminders();
		await waitFor(() => expect(api.GET).toHaveBeenCalled());
		expect(container).toBeEmptyDOMElement();
	});

	it("lists a pending follow-up and cancels it", async () => {
		api.GET.mockResolvedValueOnce({ data: { reminders: [pending] }, error: undefined })
			.mockResolvedValue({ data: { reminders: [] }, error: undefined });
		api.DELETE.mockResolvedValue({ data: { reminder: { ...pending, status: "cancelled" } }, error: undefined });
		renderReminders();
		const row = await screen.findByTitle(pending.message);
		expect(row.textContent).toContain("Nightly");
		expect(api.GET).toHaveBeenCalledWith("/api/v1/reminders", { params: { query: { sessionId: "s1" } } });

		fireEvent.click(screen.getByRole("button", { name: "Cancel follow-up" }));
		await waitFor(() =>
			expect(api.DELETE).toHaveBeenCalledWith("/api/v1/reminders/{reminderId}", { params: { path: { reminderId: "rem_1" } } }),
		);
		await waitFor(() => expect(screen.queryByTitle(pending.message)).toBeNull());
	});

	it("says so when a cancel fails", async () => {
		api.GET.mockResolvedValue({ data: { reminders: [{ ...pending, name: undefined }] }, error: undefined });
		api.DELETE.mockResolvedValue({ data: undefined, error: { code: "REMINDER_NOT_PENDING" } });
		renderReminders();
		// With no name, the message is the label.
		expect((await screen.findByTitle(pending.message)).textContent).toContain(pending.message);
		fireEvent.click(screen.getByRole("button", { name: "Cancel follow-up" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not cancel the follow-up.");
	});
});

describe("SessionReminders without a query client", () => {
	it("renders nothing instead of throwing", () => {
		api.GET.mockReset();
		const { container } = render(<SessionReminders sessionId="s1" />);
		expect(container).toBeEmptyDOMElement();
		expect(api.GET).not.toHaveBeenCalled();
	});
});
