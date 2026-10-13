import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AoBridge } from "../../../preload";
import { aoBridge } from "../../lib/bridge";
import { BrowserImportDialog } from "./BrowserImportDialog";
import { appI18n } from "../../i18n";
import type { BrowserImportWarning } from "../../../shared/browser-profile-import";

const source = {
	id: "a".repeat(32),
	name: "Google Chrome",
	family: "chromium" as const,
	profiles: [
		{ id: "b".repeat(32), name: "Default", default: true },
		{ id: "e".repeat(32), name: "Personal", default: false },
	],
	cookieSupport: "partial" as const,
	cookieSupportReason: "chromium-encryption-partial" as const,
	historySupport: true as const,
};

const firefoxSource = {
	id: "c".repeat(32),
	name: "Firefox",
	family: "firefox" as const,
	profiles: [{ id: "d".repeat(32), name: "default-release", default: true }],
	cookieSupport: "supported" as const,
	cookieSupportReason: "firefox-plaintext" as const,
	historySupport: true as const,
};

const safariSource = {
	id: "f".repeat(32),
	name: "Safari",
	family: "safari" as const,
	profiles: [{ id: "1".repeat(32), name: "Personal", default: true }],
	cookieSupport: "supported" as const,
	cookieSupportReason: "safari-plaintext" as const,
	historySupport: true as const,
};

describe("BrowserImportDialog", () => {
	const originalBridge = aoBridge.browserProfiles;

	afterEach(() => {
		aoBridge.browserProfiles = originalBridge;
	});

	it("guides a detected profile into a new AO profile and reports completion", async () => {
		const importedProfile = {
			id: "11111111-1111-4111-8111-111111111111",
			name: "Google Chrome",
			createdAt: "2026-01-01T00:00:00.000Z",
			updatedAt: "2026-01-01T00:00:00.000Z",
		};
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [] })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			delete: vi.fn(),
			discoverImportSources: vi.fn(async () => ({ sources: [source, firefoxSource] })),
			import: vi.fn(async () => ({
				sourceName: source.name,
				entries: [{
					sourceProfileNames: ["Default"],
					destinationProfile: importedProfile,
					importedCookies: 12,
					skippedCookies: 1,
					importedHistoryEntries: 34,
					warnings: [{ code: "encrypted-cookies-skipped" as const, count: 1 }],
				}],
			})),
			onImportProgress: vi.fn(() => () => undefined),
		};
		aoBridge.browserProfiles = bridge;
		const onImported = vi.fn();

		render(<BrowserImportDialog onImported={onImported} onOpenChange={() => undefined} open />);
		expect(await screen.findByText("Google Chrome")).toBeInTheDocument();
		expect(screen.queryByText(/Full Disk Access/)).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Start import" })).toBeEnabled();
		const sourcePicker = screen.getByRole("combobox", { name: "From" });
		expect(sourcePicker).toHaveTextContent("Google Chrome");
		await userEvent.click(sourcePicker);
		await userEvent.click(screen.getByRole("option", { name: /Firefox/ }));
		expect(sourcePicker).toHaveTextContent("Firefox");
		await userEvent.click(sourcePicker);
		await userEvent.click(screen.getByRole("option", { name: /Google Chrome/ }));
		expect(screen.getByRole("checkbox", { name: /Main profile/ })).toBeChecked();
		const personalProfile = screen.getByRole("checkbox", { name: /Personal/ });
		expect(personalProfile).not.toBeChecked();
		await userEvent.click(personalProfile);
		expect(screen.getByRole("radio", { name: "Keep profiles separate" })).toBeChecked();
		await userEvent.click(personalProfile);
		expect(screen.getByRole("textbox", { name: "Destination profile name" })).toHaveValue("Google Chrome");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));

		expect(await screen.findByRole("heading", { name: "Import complete" })).toBeInTheDocument();
		const details = screen.getByText(/^Import details/).closest("details")!;
		expect(details).not.toHaveAttribute("open");
		expect(details).toHaveTextContent("1 encrypted cookies");
		expect(screen.queryByText(/completed with warnings/)).not.toBeInTheDocument();
		const status = screen.getByRole("status");
		expect(status).toHaveTextContent(/^12 cookies · 34 history entries$/);
		expect(status).not.toContainElement(details);
		expect(status).not.toContainElement(screen.getByText("Cookies and site sign-ins"));
		expect(status).not.toContainElement(screen.getByText("Select a new profile from the browser toolbar to use the imported data."));
		expect(screen.getAllByText("Import complete")).toHaveLength(1);
		await userEvent.click(screen.getByText(/^Import details/));
		expect(details).toHaveAttribute("open");
		expect(status).toHaveTextContent(/^12 cookies · 34 history entries$/);
		await waitFor(() => expect(onImported).toHaveBeenCalledOnce());
		expect(bridge.import).toHaveBeenCalledWith(expect.objectContaining({
			sourceId: source.id,
			profileIds: [source.profiles[0]!.id],
			includeCookies: true,
			includeHistory: true,
			destination: { mode: "merge", name: "Google Chrome" },
		}));
	});

	it("only probes and warns about Safari after Safari is selected", async () => {
		const safariDeferred = { ...safariSource, profiles: [], profilesDeferred: true as const };
		const discoverImportSources = vi.fn(async (input?: { sourceId?: string }) => (
			input?.sourceId === safariSource.id
				? { sources: [safariSource], warnings: ["safari-access-denied" as const] }
				: { sources: [source, safariDeferred] }
		));
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources,
			import: vi.fn(),
			onImportProgress: vi.fn(() => () => undefined),
		};

		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		const sourcePicker = await screen.findByRole("combobox", { name: "From" });
		expect(sourcePicker).toHaveTextContent("Google Chrome");
		expect(screen.queryByText(/Full Disk Access/)).not.toBeInTheDocument();
		expect(discoverImportSources).toHaveBeenCalledWith();

		await userEvent.click(sourcePicker);
		await userEvent.click(screen.getByRole("option", { name: /Safari/ }));

		await waitFor(() => expect(discoverImportSources).toHaveBeenCalledWith({ sourceId: safariSource.id }));
		expect(await screen.findByText(/Full Disk Access/)).toBeInTheDocument();
		expect(await screen.findByRole("checkbox", { name: /Personal/ })).toBeChecked();
	});

	it("removes a deferred Safari source when targeted discovery finds no profiles", async () => {
		const safariDeferred = { ...safariSource, profiles: [], profilesDeferred: true as const };
		const discoverImportSources = vi.fn(async (input?: { sourceId?: string }) => (
			input?.sourceId === safariSource.id
				? { sources: [] }
				: { sources: [source, safariDeferred] }
		));
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources,
			import: vi.fn(),
			onImportProgress: vi.fn(() => () => undefined),
		};

		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		const sourcePicker = await screen.findByRole("combobox", { name: "From" });
		await userEvent.click(sourcePicker);
		await userEvent.click(screen.getByRole("option", { name: /Safari/ }));

		await waitFor(() => expect(discoverImportSources).toHaveBeenCalledWith({ sourceId: safariSource.id }));
		await waitFor(() => expect(sourcePicker).toHaveTextContent("Google Chrome"));
		expect(screen.queryByText(/Full Disk Access/)).not.toBeInTheDocument();
		await userEvent.click(sourcePicker);
		expect(screen.queryByRole("option", { name: /Safari/ })).not.toBeInTheDocument();
	});

	it("clears a failed import when choosing another browser", async () => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [] })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			delete: vi.fn(),
			discoverImportSources: vi.fn(async () => ({ sources: [source, firefoxSource] })),
			import: vi.fn(async () => { throw new Error("Firefox cookie data is unavailable."); }),
			onImportProgress: vi.fn(() => () => undefined),
		};
		aoBridge.browserProfiles = bridge;
		const onImported = vi.fn();

		render(<BrowserImportDialog onImported={onImported} onOpenChange={() => undefined} open />);
		const sourcePicker = await screen.findByRole("combobox", { name: "From" });
		await userEvent.click(sourcePicker);
		await userEvent.click(screen.getByRole("option", { name: /Firefox/ }));
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Firefox cookie data is unavailable.");
		expect(screen.getByRole("alert")).toHaveFocus();
		expect(onImported).toHaveBeenCalledOnce();

		const retrySourcePicker = screen.getByRole("combobox", { name: "From" });
		await userEvent.click(retrySourcePicker);
		await userEvent.click(screen.getByRole("option", { name: /Google Chrome/ }));
		expect(retrySourcePicker).toHaveTextContent("Google Chrome");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("reports an empty result without a success banner", async () => {
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [source] })),
			import: vi.fn(async () => ({ sourceName: source.name, entries: [] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Google Chrome");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		expect(await screen.findByRole("heading", { name: "Nothing was imported" })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "Import complete" })).not.toBeInTheDocument();
	});

	it("shows completion and a neutral cookie status when Safari only imports history", async () => {
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [safariSource] })),
			import: vi.fn(async () => ({ sourceName: "Safari", entries: [{
				sourceProfileNames: ["Personal"],
				destinationProfile: { id: "11111111-1111-4111-8111-111111111111", name: "Imported Safari", createdAt: "2026-01-01", updatedAt: "2026-01-01" },
				importedCookies: 0, importedHistoryEntries: 1, skippedCookies: 1,
				warnings: [{ code: "expired-cookies-skipped" as const, count: 1 }],
			}] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Safari");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		expect(await screen.findByRole("heading", { name: "Import complete" })).toBeInTheDocument();
		expect(screen.getByText("0 cookies · 1 history entries")).toBeInTheDocument();
		const cookieRow = screen.getByText("Cookies and site sign-ins").parentElement!;
		expect(within(cookieRow).getByText("Nothing was imported")).toHaveClass("sr-only");
		expect(within(cookieRow).queryByText("Finished")).not.toBeInTheDocument();
		expect(cookieRow.querySelector("svg:last-child")).toHaveClass("text-muted-foreground");
		const historyRow = screen.getByText("Browsing history").parentElement!;
		expect(within(historyRow).getByText("Finished")).toHaveClass("sr-only");
		expect(within(historyRow).queryByText("Nothing was imported")).not.toBeInTheDocument();
		expect(historyRow.querySelector("svg:last-child")).toHaveClass("text-success");
	});

	it("does not warn when a requested cookie store is empty", async () => {
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [safariSource] })),
			import: vi.fn(async () => ({ sourceName: "Safari", entries: [{
				sourceProfileNames: ["Personal"],
				destinationProfile: { id: "11111111-1111-4111-8111-111111111111", name: "Imported Safari", createdAt: "2026-01-01", updatedAt: "2026-01-01" },
				importedCookies: 0, importedHistoryEntries: 1, skippedCookies: 0, warnings: [],
			}] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Safari");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		expect(await screen.findByRole("heading", { name: "Import complete" })).toBeInTheDocument();
	});

	it.each([
		{ imported: 12, failure: false, title: "Import complete" },
		{ imported: 12, failure: true, title: "Import complete" },
		{ imported: 0, failure: false, title: "Nothing was imported" },
	])("keeps expected skips informational while reporting $title (cookie-write failure: $failure)", async ({ imported, failure, title }) => {
		const warnings: BrowserImportWarning[] = [
			{ code: "expired-cookies-skipped", count: 2 },
			{ code: "isolated-cookies-skipped", count: 3 },
			...(failure ? [{ code: "cookie-write-failed" as const, count: 1 }] : []),
		];
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [source] })),
			import: vi.fn(async () => ({ sourceName: source.name, entries: [{
				sourceProfileNames: ["Default"],
				destinationProfile: { id: "11111111-1111-4111-8111-111111111111", name: "Imported Chrome", createdAt: "2026-01-01", updatedAt: "2026-01-01" },
				importedCookies: imported, importedHistoryEntries: 0, skippedCookies: failure ? 6 : 5, warnings,
			}] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Google Chrome");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument();
		const summary = screen.getByText(/^Import details/);
		const details = summary.closest("details")!;
		expect(details).not.toHaveAttribute("open");
		expect(details).toHaveTextContent("2 expired cookies were skipped.");
		expect(details).toHaveTextContent("3 cookies tied to isolated browser contexts");
		if (failure) expect(details).toHaveTextContent("AO could not write 1 cookies to the new profile.");
		await userEvent.click(summary);
		expect(details).toHaveAttribute("open");
		expect(details).toHaveTextContent("Some sites may ask you to sign in again.");
	});

	it("keeps history import limits in collapsed details when no cookies were skipped", async () => {
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [source] })),
			import: vi.fn(async () => ({ sourceName: source.name, entries: [{
				sourceProfileNames: ["Default"],
				destinationProfile: { id: "new", name: "Imported Chrome", createdAt: "", updatedAt: "" },
				importedCookies: 0, importedHistoryEntries: 5000, skippedCookies: 0,
				warnings: [{ code: "history-limit-truncated" as const, count: 27650 }],
			}] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Google Chrome");
		await userEvent.click(screen.getByRole("checkbox", { name: /Cookies and site sign-ins/ }));
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		const status = await screen.findByRole("status");
		expect(status).toHaveTextContent(/^5000 history entries$/);
		expect(status).not.toHaveTextContent("cookies");
		const dialog = screen.getByRole("dialog");
		expect(dialog).toHaveTextContent("Browsing history");
		expect(dialog).not.toHaveTextContent("Cookies and site sign-ins");
		const details = screen.getByText("Import details").closest("details")!;
		expect(details).not.toHaveAttribute("open");
		expect(details).toHaveTextContent("27650 history entries");
		expect(status).not.toContainElement(details);
		const resultCard = details.parentElement!;
		expect(resultCard.querySelector('[class*="warning"]')).toBeNull();
		expect(resultCard.querySelector('[class*="destructive"]')).toBeNull();
	});

	it("shows only the selected cookie count for a cookies-only import", async () => {
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [source] })),
			import: vi.fn(async () => ({ sourceName: source.name, entries: [{
				sourceProfileNames: ["Default"],
				destinationProfile: { id: "new", name: "Imported Chrome", createdAt: "", updatedAt: "" },
				importedCookies: 12, importedHistoryEntries: 0, skippedCookies: 0, warnings: [],
			}] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Google Chrome");
		await userEvent.click(screen.getByRole("checkbox", { name: /Browsing history/ }));
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		const status = await screen.findByRole("status");
		expect(status).toHaveTextContent(/^12 cookies$/);
		expect(status).not.toHaveTextContent("history");
		expect(screen.getByText("Cookies and site sign-ins")).toBeInTheDocument();
		expect(screen.queryByText("Browsing history")).not.toBeInTheDocument();
	});

	it("preserves an import failure across translation updates", async () => {
		const discover = vi.fn(async () => ({ sources: [source] }));
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: discover,
			import: vi.fn(async () => { throw new Error("Import failed; please retry."); }),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Google Chrome");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		await screen.findByRole("alert");
		try {
			await act(async () => { await appI18n.changeLanguage("de"); });
			expect(screen.getByRole("alert")).toHaveTextContent("Import failed; please retry.");
			expect(discover).toHaveBeenCalledOnce();
		} finally {
			await act(async () => { await appI18n.changeLanguage("en"); });
		}
	});

	it.each([
		"unable to open database file",
		"database is locked",
		"SQLITE_BUSY: database is locked",
		"AO couldn't create a temporary copy of Brave's profile database. Fully close Brave, then retry the import.",
	])("shows a quiet retry tip above the browser dropdown for %s", async (message) => {
		const braveSource = { ...source, name: "Brave" };
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [] })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			delete: vi.fn(),
			discoverImportSources: vi.fn(async () => ({ sources: [braveSource] })),
			import: vi.fn(async () => {
				throw new Error(`Error invoking remote method 'browserProfiles:import:start': Error: ${message}`);
			}),
			onImportProgress: vi.fn(() => () => undefined),
		};
		aoBridge.browserProfiles = bridge;

		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Brave");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));

		const alert = await screen.findByRole("alert");
		expect(alert).toHaveClass("text-muted-foreground");
		expect(alert).toHaveTextContent(/^Tip: Couldn't read Brave's profile data, so nothing was imported\. Fully close Brave/);
		expect(alert).toHaveTextContent("including background processes, then retry");
		const picker = screen.getByRole("combobox", { name: "From" });
		expect(alert.compareDocumentPosition(picker) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		expect(alert).not.toHaveTextContent("Error invoking remote method");
	});

	it.each([
		"A destination browser profile already exists.",
		"EPERM: operation not permitted",
		"An unexpected import failure.",
	])("keeps non-recoverable import errors actionable: %s", async (message) => {
		aoBridge.browserProfiles = {
			...originalBridge,
			discoverImportSources: vi.fn(async () => ({ sources: [source] })),
			import: vi.fn(async () => { throw new Error(message); }),
			onImportProgress: vi.fn(() => () => undefined),
		};
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Google Chrome");
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent(message);
		expect(alert).toHaveClass("text-destructive");
		expect(alert).not.toHaveTextContent("Tip:");
		expect(screen.getByRole("button", { name: "Start import" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
	});

	it("shows missing import support as an error without offering retry", async () => {
		delete (aoBridge as Partial<AoBridge>).browserProfiles;
		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Browser import is unavailable in this build.");
		expect(alert).toHaveClass("text-destructive");
		expect(alert).not.toHaveTextContent("Tip:");
		expect(screen.getByRole("button", { name: "Start import" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
	});

	it.each([
		{ message: "EPERM: operation not permitted", includeCookies: true, recoverable: false },
		{ message: "EACCES: permission denied", includeCookies: true, recoverable: false },
		{ message: "unable to open database file", includeCookies: false, recoverable: false },
		{ message: "SQLITE_CANTOPEN", includeCookies: false, recoverable: false },
		{ message: "database is locked", includeCookies: false, recoverable: true },
		{ message: "SQLITE_BUSY", includeCookies: false, recoverable: true },
	])("maps Safari $message with cookies=$includeCookies to recoverable=$recoverable", async ({ message, includeCookies, recoverable }) => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [] })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			delete: vi.fn(),
			discoverImportSources: vi.fn(async () => ({ sources: [safariSource] })),
			import: vi.fn(async () => { throw new Error(message); }),
			onImportProgress: vi.fn(() => () => undefined),
		};
		aoBridge.browserProfiles = bridge;

		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		await screen.findByText("Safari");
		if (!includeCookies) {
			await userEvent.click(screen.getByRole("checkbox", { name: /Cookies and site sign-ins/ }));
		}
		await userEvent.click(screen.getByRole("button", { name: "Start import" }));

		const alert = await screen.findByRole("alert");
		expect(bridge.import).toHaveBeenCalledWith(expect.objectContaining({ includeCookies, includeHistory: true }));
		if (recoverable) {
			expect(alert).toHaveTextContent("Tip: Couldn't read Safari's profile data, so nothing was imported.");
			expect(alert).toHaveTextContent("Fully close Safari, including background processes, then retry");
			expect(alert).toHaveClass("text-muted-foreground");
			expect(alert).not.toHaveTextContent("Full Disk Access");
			expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
		} else {
			expect(alert).toHaveTextContent("Privacy & Security > Full Disk Access, allow AO, then restart AO");
			expect(alert).toHaveClass("text-destructive");
			expect(alert).not.toHaveTextContent("Tip:");
			expect(screen.getByRole("button", { name: "Start import" })).toBeEnabled();
			expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
		}
	});

	it("keeps discovery failures visible and disables import", async () => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [] })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			delete: vi.fn(),
			discoverImportSources: vi.fn(async () => { throw new Error("Browser discovery failed."); }),
			import: vi.fn(),
			onImportProgress: vi.fn(() => () => undefined),
		};
		aoBridge.browserProfiles = bridge;

		render(<BrowserImportDialog onImported={() => undefined} onOpenChange={() => undefined} open />);
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Browser discovery failed.");
		expect(alert).toHaveClass("text-destructive");
		expect(alert).not.toHaveTextContent("Tip:");
		expect(screen.getByRole("button", { name: "Start import" })).toBeDisabled();
	});
});
