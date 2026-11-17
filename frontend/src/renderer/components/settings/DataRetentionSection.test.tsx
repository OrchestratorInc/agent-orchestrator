import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { CloudCpAuthError, CloudCpError } from "../../lib/cloud-cp";
import type { CloudCpDataRetention, CloudCpDataRetentionChange } from "../../lib/cloud-cp";
import { DataRetentionSection } from "./DataRetentionSection";

const mocks = vi.hoisted(() => ({
	client: {
		getDataRetention: vi.fn(),
		putDataRetention: vi.fn(),
	},
	cloudEnabled: true,
	ready: true,
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: mocks.cloudEnabled }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: mocks.client,
		ready: mocks.ready,
		baseUrl: "https://cloud.example.test",
		userId: mocks.ready ? "user-1" : "",
	}),
}));

const LABEL = "Keep my cloud session content for";
const retention: CloudCpDataRetention = {
	retentionDays: 180, defaultDays: 180, followsDefault: true, options: [30, 60, 90, 180, 365], updatedAt: null,
};
const changed = (overrides: Partial<CloudCpDataRetentionChange>): CloudCpDataRetentionChange => ({ ...retention, sessionsToDelete: 0, ...overrides });

function renderSection() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<DataRetentionSection />
		</QueryClientProvider>,
	);
}

async function choose(option: string) {
	await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toBeEnabled());
	await userEvent.click(screen.getByRole("button", { name: LABEL }));
	await userEvent.click(screen.getByRole("menuitem", { name: option }));
}

/** The select, also while a modal dialog hides the page behind it. */
function trigger() {
	return screen.getByRole("button", { name: LABEL, hidden: true });
}

function setOnline(online: boolean) {
	Object.defineProperty(window.navigator, "onLine", { configurable: true, get: () => online });
	window.dispatchEvent(new Event(online ? "online" : "offline"));
}

describe("DataRetentionSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.client.getDataRetention.mockReset().mockResolvedValue(retention);
		mocks.client.putDataRetention.mockReset();
		mocks.cloudEnabled = true;
		mocks.ready = true;
	});

	afterEach(() => {
		// Back to jsdom's own (online) navigator.onLine.
		delete (window.navigator as { onLine?: boolean }).onLine;
	});

	it("shows the default first, then the server's options", async () => {
		renderSection();
		expect(await screen.findByRole("heading", { name: "Data retention" })).toBeInTheDocument();
		const trigger = await screen.findByRole("button", { name: LABEL });
		await waitFor(() => expect(trigger).toHaveTextContent("Default (180 days)"));
		expect(trigger).toBeEnabled();
		expect(screen.getByText(
			"The content of your cloud sessions, including team sessions you started, is removed this many days after each session was created: transcripts, prompts and outputs. A session still running has its content removed once it ends.",
		)).toBeInTheDocument();
		await userEvent.click(trigger);
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Default (180 days)", "30 days", "60 days", "90 days", "180 days", "365 days",
		]);
	});

	it("shows a period the user chose as theirs, even one equal to the default", async () => {
		mocks.client.getDataRetention.mockResolvedValue({ ...retention, followsDefault: false, updatedAt: "2026-11-01T00:00:00Z" });
		renderSection();
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent(/^180 days$/));
	});

	it("goes back to the default with null, after a dry run", async () => {
		mocks.client.getDataRetention.mockResolvedValue({ ...retention, retentionDays: 365, followsDefault: false, updatedAt: "2026-11-01T00:00:00Z" });
		mocks.client.putDataRetention
			.mockResolvedValueOnce(changed({ retentionDays: 365, followsDefault: false, sessionsToDelete: 0 }))
			.mockResolvedValueOnce(changed({ updatedAt: "2026-11-16T10:00:00Z" }));
		renderSection();
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("365 days"));
		await choose("Default (180 days)");
		await waitFor(() => expect(mocks.client.putDataRetention).toHaveBeenCalledTimes(2));
		expect(mocks.client.putDataRetention.mock.calls.map(([body]) => body)).toEqual([
			{ retentionDays: null, dryRun: true },
			{ retentionDays: null, dryRun: false },
		]);
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("Default (180 days)"));
		expect(screen.getByRole("status")).toHaveTextContent("Saved.");
	});

	it("confirms going back to a shorter default with the default's period", async () => {
		mocks.client.getDataRetention.mockResolvedValue({ ...retention, retentionDays: 365, followsDefault: false, updatedAt: "2026-11-01T00:00:00Z" });
		mocks.client.putDataRetention
			.mockResolvedValueOnce(changed({ retentionDays: 365, followsDefault: false, sessionsToDelete: 2 }))
			.mockResolvedValueOnce(changed({ updatedAt: "2026-11-16T10:00:00Z", sessionsToDelete: 2 }));
		renderSection();
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("365 days"));
		await choose("Default (180 days)");
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText(
			"The content of 2 of your sessions older than 180 days will be removed: their transcripts, prompts and outputs. This can't be undone.",
		)).toBeInTheDocument();
		await userEvent.click(within(dialog).getByRole("button", { name: "Remove content" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(mocks.client.putDataRetention).toHaveBeenLastCalledWith({ retentionDays: null, dryRun: false });
	});

	it("sends the default's days to a control plane that predates going back to it", async () => {
		const { followsDefault: _omitted, ...older } = retention;
		mocks.client.getDataRetention.mockResolvedValue({ ...older, retentionDays: 30, updatedAt: "2026-11-01T00:00:00Z" });
		mocks.client.putDataRetention
			.mockResolvedValueOnce({ ...older, retentionDays: 30, updatedAt: "2026-11-01T00:00:00Z", sessionsToDelete: 0 })
			.mockResolvedValueOnce({ ...older, updatedAt: "2026-11-16T10:00:00Z", sessionsToDelete: 0 });
		renderSection();
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("30 days"));
		await choose("Default (180 days)");
		await waitFor(() => expect(mocks.client.putDataRetention).toHaveBeenCalledTimes(2));
		expect(mocks.client.putDataRetention.mock.calls.map(([body]) => body)).toEqual([
			{ retentionDays: 180, dryRun: true },
			{ retentionDays: 180, dryRun: false },
		]);
	});

	it("keeps a period the deployment no longer offers visible as itself", async () => {
		mocks.client.getDataRetention.mockResolvedValue({ ...retention, retentionDays: 45, followsDefault: false, updatedAt: "2026-11-01T00:00:00Z" });
		renderSection();
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("45 days"));
	});

	it("is hidden when signed out of Cloud, without asking the control plane", () => {
		mocks.ready = false;
		renderSection();
		expect(screen.queryByRole("heading", { name: "Data retention" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: LABEL })).not.toBeInTheDocument();
		expect(mocks.client.getDataRetention).not.toHaveBeenCalled();
	});

	it("is hidden when Cloud is off", () => {
		mocks.cloudEnabled = false;
		renderSection();
		expect(screen.queryByRole("heading", { name: "Data retention" })).not.toBeInTheDocument();
		expect(mocks.client.getDataRetention).not.toHaveBeenCalled();
	});

	it("is hidden against a control plane without the setting", async () => {
		mocks.client.getDataRetention.mockRejectedValue(new CloudCpError("not found", { status: 404 }));
		renderSection();
		await waitFor(() => expect(mocks.client.getDataRetention).toHaveBeenCalled());
		await waitFor(() => expect(screen.queryByRole("heading", { name: "Data retention" })).not.toBeInTheDocument());
	});

	it("is hidden against a control plane whose store can't keep the setting", async () => {
		mocks.client.getDataRetention.mockRejectedValue(
			new CloudCpError("Data retention is unavailable.", { status: 501, code: "not_implemented" }),
		);
		renderSection();
		await waitFor(() => expect(mocks.client.getDataRetention).toHaveBeenCalled());
		await waitFor(() => expect(screen.queryByRole("heading", { name: "Data retention" })).not.toBeInTheDocument());
		expect(screen.queryByText("Couldn't load data retention.")).not.toBeInTheDocument();
	});

	it("shows a disabled select while the period loads", () => {
		mocks.client.getDataRetention.mockReturnValue(new Promise(() => undefined));
		renderSection();
		const trigger = screen.getByRole("button", { name: LABEL });
		expect(trigger).toBeDisabled();
		expect(trigger).toHaveTextContent("Loading…");
	});

	it("checks a shorter period with a dry run and removes content only once confirmed", async () => {
		mocks.client.putDataRetention
			.mockResolvedValueOnce(changed({ sessionsToDelete: 12 }))
			.mockResolvedValueOnce(changed({ retentionDays: 30, followsDefault: false, updatedAt: "2026-11-15T10:00:00Z", sessionsToDelete: 12 }));
		renderSection();
		await choose("30 days");

		const dialog = await screen.findByRole("dialog");
		expect(mocks.client.putDataRetention).toHaveBeenCalledTimes(1);
		expect(mocks.client.putDataRetention).toHaveBeenLastCalledWith({ retentionDays: 30, dryRun: true });
		expect(within(dialog).getByText("Remove content from older sessions?")).toBeInTheDocument();
		expect(within(dialog).getByText(
			"The content of 12 of your sessions older than 30 days will be removed: their transcripts, prompts and outputs. This can't be undone.",
		)).toBeInTheDocument();
		// Nothing changed while the dialog asks.
		expect(trigger()).toHaveTextContent("Default (180 days)");

		await userEvent.click(within(dialog).getByRole("button", { name: "Remove content" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(mocks.client.putDataRetention).toHaveBeenCalledTimes(2);
		expect(mocks.client.putDataRetention).toHaveBeenLastCalledWith({ retentionDays: 30, dryRun: false });
		expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("30 days");
		expect(screen.getByRole("status")).toHaveTextContent("Saved.");
	});

	it("says one session in the singular", async () => {
		mocks.client.putDataRetention.mockResolvedValueOnce(changed({ sessionsToDelete: 1 }));
		renderSection();
		await choose("90 days");
		expect(await screen.findByText(
			"The content of 1 of your sessions older than 90 days will be removed: its transcript, prompts and outputs. This can't be undone.",
		)).toBeInTheDocument();
	});

	it("puts the period after a Russian preposition its words agree with", async () => {
		// "старше" wants the genitive ("старше 2 дней"), which the period's own
		// words ("2 дня") are not; "через" takes them as they are.
		await appI18n.changeLanguage("ru");
		mocks.client.getDataRetention.mockResolvedValue({ ...retention, options: [1, 2, 30, 180] });
		mocks.client.putDataRetention.mockResolvedValueOnce(changed({ sessionsToDelete: 3 }));
		renderSection();
		const select = await screen.findByRole("button", { name: "Хранить содержимое моих облачных сессий" });
		await waitFor(() => expect(select).toBeEnabled());
		await userEvent.click(select);
		await userEvent.click(screen.getByRole("menuitem", { name: "2 дня" }));
		expect(await screen.findByText(
			"Содержимое сессий будет удаляться через 2 дня после их создания. Под новый срок уже попадают 3 ваши сессии: их транскрипты, промпты и результаты будут удалены. Это действие нельзя отменить.",
		)).toBeInTheDocument();
	});

	it("changes nothing when the confirmation is cancelled", async () => {
		mocks.client.putDataRetention.mockResolvedValueOnce(changed({ sessionsToDelete: 3 }));
		renderSection();
		await choose("60 days");
		await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(mocks.client.putDataRetention).toHaveBeenCalledTimes(1);
		expect(mocks.client.putDataRetention).toHaveBeenCalledWith({ retentionDays: 60, dryRun: true });
		expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("Default (180 days)");
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
	});

	it("applies directly when the new period removes nothing", async () => {
		mocks.client.putDataRetention
			.mockResolvedValueOnce(changed({ sessionsToDelete: 0 }))
			.mockResolvedValueOnce(changed({ retentionDays: 365, followsDefault: false, updatedAt: "2026-11-15T10:00:00Z" }));
		renderSection();
		await choose("365 days");
		await waitFor(() => expect(mocks.client.putDataRetention).toHaveBeenCalledTimes(2));
		expect(mocks.client.putDataRetention.mock.calls.map(([body]) => body)).toEqual([
			{ retentionDays: 365, dryRun: true },
			{ retentionDays: 365, dryRun: false },
		]);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("365 days"));
		expect(screen.getByRole("status")).toHaveTextContent("Saved.");
	});

	it("does nothing when the current period is chosen again", async () => {
		renderSection();
		await choose("Default (180 days)");
		expect(mocks.client.putDataRetention).not.toHaveBeenCalled();
	});

	describe("errors", () => {
		it("shows the server's message when it refuses the period", async () => {
			mocks.client.putDataRetention.mockRejectedValueOnce(
				new CloudCpError("Choose one of 30, 60, 90, 180 or 365 days.", { status: 400, code: "invalid_retention" }),
			);
			renderSection();
			await choose("30 days");
			expect(await screen.findByRole("alert")).toHaveTextContent("Choose one of 30, 60, 90, 180 or 365 days.");
			expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("Default (180 days)");
		});

		it("falls back to its own words for a 400 without an envelope", async () => {
			mocks.client.putDataRetention.mockRejectedValueOnce(new CloudCpError("Cloud control plane request failed with status 400.", { status: 400 }));
			renderSection();
			await choose("30 days");
			expect(await screen.findByRole("alert")).toHaveTextContent("Choose one of the offered periods.");
		});

		it("asks the user to sign in again when the control plane answers 401", async () => {
			mocks.client.putDataRetention.mockRejectedValueOnce(new CloudCpAuthError("Unauthorized", { status: 401, code: "unauthorized" }));
			renderSection();
			await choose("30 days");
			expect(await screen.findByRole("alert")).toHaveTextContent("Sign in to Cloud again to change this.");
		});

		it("says Cloud is unreachable when the dry run never gets an answer", async () => {
			mocks.client.putDataRetention.mockRejectedValueOnce(new TypeError("fetch failed"));
			renderSection();
			await choose("30 days");
			expect(await screen.findByRole("alert")).toHaveTextContent("Can't reach Cloud. Check your connection and try again.");
		});

		it("keeps the confirmation open with the error when the change fails", async () => {
			mocks.client.putDataRetention
				.mockResolvedValueOnce(changed({ sessionsToDelete: 5 }))
				.mockRejectedValueOnce(new CloudCpError("boom", { status: 500 }))
				.mockResolvedValueOnce(changed({ retentionDays: 30, followsDefault: false, sessionsToDelete: 5 }));
			renderSection();
			await choose("30 days");
			const dialog = await screen.findByRole("dialog");
			await userEvent.click(within(dialog).getByRole("button", { name: "Remove content" }));
			expect(await within(dialog).findByRole("alert")).toHaveTextContent("Couldn't save data retention. Try again.");
			expect(trigger()).toHaveTextContent("Default (180 days)");

			await userEvent.click(within(dialog).getByRole("button", { name: "Remove content" }));
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
			expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("30 days");
		});

		it("offers a retry when the period can't be loaded", async () => {
			mocks.client.getDataRetention
				.mockRejectedValueOnce(new CloudCpError("boom", { status: 500 }))
				.mockResolvedValueOnce(retention);
			renderSection();
			expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load data retention.");
			await userEvent.click(screen.getByRole("button", { name: "Retry" }));
			await waitFor(() => expect(screen.getByRole("button", { name: LABEL })).toHaveTextContent("Default (180 days)"));
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		});

		it("says Cloud is unreachable when the period can't be loaded offline", async () => {
			mocks.client.getDataRetention.mockRejectedValueOnce(new TypeError("Failed to fetch"));
			renderSection();
			expect(await screen.findByRole("alert")).toHaveTextContent("Can't reach Cloud. Check your connection and try again.");
			expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
		});

		it("disables the select while the computer is offline", async () => {
			renderSection();
			const trigger = await screen.findByRole("button", { name: LABEL });
			await waitFor(() => expect(trigger).toHaveTextContent("Default (180 days)"));
			act(() => setOnline(false));
			expect(trigger).toBeDisabled();
			expect(screen.getByRole("alert")).toHaveTextContent("Can't reach Cloud. Check your connection and try again.");
			act(() => setOnline(true));
			expect(trigger).toBeEnabled();
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		});
	});
});
