import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserProfileUsage } from "../../../shared/browser-profiles";
import type { AoBridge } from "../../../preload";
import { BrowserProfilesSection } from "./BrowserProfilesSection";

const profile = {
	id: "11111111-1111-4111-8111-111111111111",
	name: "Work",
	createdAt: "2026-01-01T00:00:00.000Z",
	updatedAt: "2026-01-01T00:00:00.000Z",
};

describe("BrowserProfilesSection", () => {
	let originalBridge: AoBridge["browserProfiles"];

	afterEach(() => {
		if (window.ao) window.ao.browserProfiles = originalBridge;
	});

	it("loads profiles and wires create, rename, clear, and delete actions", async () => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [profile], defaultProfileId: null })),
			create: vi.fn(async (name: string) => ({ ...profile, id: "22222222-2222-4222-8222-222222222222", name })),
			rename: vi.fn(async (input: { id: string; name: string }) => ({ ...profile, ...input })),
			clear: vi.fn(async () => undefined),
			usage: vi.fn(async () => ({ sessions: [] })),
			delete: vi.fn(async () => ({})),
			setDefault: vi.fn(async () => undefined),
			discoverImportSources: vi.fn(async () => ({ sources: [] })),
			import: vi.fn(async () => ({ sourceName: "", entries: [] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		originalBridge = window.ao!.browserProfiles;
		window.ao!.browserProfiles = bridge;

		render(<BrowserProfilesSection />);
		expect(await screen.findByText("Work")).toBeInTheDocument();
		expect(bridge.list).toHaveBeenCalledOnce();

		const createInput = screen.getByRole("textbox", { name: "Profile name" });
		await userEvent.type(createInput, "Personal");
		await userEvent.click(screen.getByRole("button", { name: "Create profile" }));
		await waitFor(() => expect(bridge.create).toHaveBeenCalledWith("Personal"));
		expect(await screen.findByText("Personal")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Actions for Work" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
		const renameInput = await screen.findByRole("textbox", { name: "New name for Work" });
		await waitFor(() => expect(renameInput).toHaveFocus());
		await userEvent.clear(renameInput);
		await userEvent.type(renameInput, "Office{Enter}");
		await waitFor(() => expect(bridge.rename).toHaveBeenCalledWith({ id: profile.id, name: "Office" }));
		expect(await screen.findByText("Office")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Actions for Office" })).toHaveFocus();

		await userEvent.click(screen.getByRole("button", { name: "Actions for Office" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Clear data" }));
		expect(bridge.clear).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Clear data" }));
		await waitFor(() => expect(bridge.clear).toHaveBeenCalledWith(profile.id));
		await userEvent.click(screen.getByRole("button", { name: "Actions for Office" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Delete profile" }));
		expect(bridge.delete).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Delete profile" }));
		await waitFor(() => expect(bridge.delete).toHaveBeenCalledWith(profile.id, []));
	});

	it("lists affected sessions, explains blockers, and enables deletion when activity ends", async () => {
		originalBridge = window.ao!.browserProfiles;
		let sessions: BrowserProfileUsage[] = [
			{ viewId: "1:idle", sessionId: "idle-worker", blocker: null },
			{ viewId: "1:busy", sessionId: "busy-worker", blocker: "agent-active" },
		];
		const bridge = { ...originalBridge,
			list: vi.fn(async () => ({ profiles: [profile], defaultProfileId: null })),
			usage: vi.fn(async () => ({ sessions })),
			delete: vi.fn(async () => ({})),
		};
		window.ao!.browserProfiles = bridge;
		render(<BrowserProfilesSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Actions for Work" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Delete profile" }));
		expect(await screen.findByText("idle-worker")).toBeInTheDocument();
		expect(screen.getByText("busy-worker")).toBeInTheDocument();
		expect(screen.getByText(/An agent is using this browser/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Delete and switch sessions" })).toBeDisabled();
		expect(bridge.delete).not.toHaveBeenCalled();
		sessions = sessions.map((session) => ({ ...session, blocker: null }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Delete and switch sessions" })).toBeEnabled(), { timeout: 4000 });
		await userEvent.click(screen.getByRole("button", { name: "Delete and switch sessions" }));
		await waitFor(() => expect(bridge.delete).toHaveBeenCalledWith(profile.id, ["1:idle", "1:busy"]));
	});

	it("does not delete on cancel and hides raw IPC errors on failure", async () => {
		originalBridge = window.ao!.browserProfiles;
		const bridge = { ...originalBridge,
			list: vi.fn(async () => ({ profiles: [profile], defaultProfileId: null })),
			usage: vi.fn(async () => ({ sessions: [] })),
			delete: vi.fn(async () => { throw new Error("Error invoking remote method 'browserProfiles:delete': Error: private details"); }),
		};
		window.ao!.browserProfiles = bridge;
		render(<BrowserProfilesSection />);
		const open = async () => {
			await userEvent.click(await screen.findByRole("button", { name: "Actions for Work" }));
			await userEvent.click(await screen.findByRole("menuitem", { name: "Delete profile" }));
		};
		await open();
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(bridge.delete).not.toHaveBeenCalled();
		await open();
		await waitFor(() => expect(screen.getByRole("button", { name: "Delete profile" })).toBeEnabled());
		await userEvent.click(screen.getByRole("button", { name: "Delete profile" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not delete browser profile.");
		expect(screen.queryByText(/private details/)).not.toBeInTheDocument();
		expect(screen.getByText("Work")).toBeInTheDocument();
	});

	it("keeps confirmation disabled if affected sessions cannot be checked", async () => {
		originalBridge = window.ao!.browserProfiles;
		window.ao!.browserProfiles = { ...originalBridge,
			list: vi.fn(async () => ({ profiles: [profile], defaultProfileId: null })),
			usage: vi.fn(async () => ({ sessions: [], error: "Could not check browsers." })),
		};
		render(<BrowserProfilesSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Actions for Work" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Delete profile" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not check browsers.");
		expect(screen.getByRole("button", { name: "Delete profile" })).toBeDisabled();
	});

	it("sets and unsets a profile as the default", async () => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [profile], defaultProfileId: null })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			usage: vi.fn(async () => ({ sessions: [] })),
			delete: vi.fn(),
			setDefault: vi.fn(async () => undefined),
			discoverImportSources: vi.fn(async () => ({ sources: [] })),
			import: vi.fn(async () => ({ sourceName: "", entries: [] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		originalBridge = window.ao!.browserProfiles;
		window.ao!.browserProfiles = bridge;

		render(<BrowserProfilesSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Actions for Work" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Set Work as default" }));
		await waitFor(() => expect(bridge.setDefault).toHaveBeenCalledWith(profile.id));
		expect(await screen.findByText("Work (Default)")).toBeInTheDocument();

		await userEvent.click(await screen.findByRole("button", { name: "Actions for Work" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Unset Work as default" }));
		await waitFor(() => expect(bridge.setDefault).toHaveBeenCalledWith(null));
	});

	it("cancels an inline rename with Escape and keeps the saved name", async () => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => ({ profiles: [profile], defaultProfileId: null })),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			usage: vi.fn(async () => ({ sessions: [] })),
			delete: vi.fn(),
			setDefault: vi.fn(async () => undefined),
			discoverImportSources: vi.fn(async () => ({ sources: [] })),
			import: vi.fn(async () => ({ sourceName: "", entries: [] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		originalBridge = window.ao!.browserProfiles;
		window.ao!.browserProfiles = bridge;

		render(<BrowserProfilesSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Actions for Work" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
		const renameInput = await screen.findByRole("textbox", { name: "New name for Work" });
		await waitFor(() => expect(renameInput).toHaveFocus());
		await userEvent.type(renameInput, " draft{Escape}");

		expect(screen.queryByRole("textbox", { name: "New name for Work" })).not.toBeInTheDocument();
		expect(screen.getByText("Work")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Actions for Work" })).toHaveFocus();
		expect(bridge.rename).not.toHaveBeenCalled();
	});

	it("surfaces a recoverable load error", async () => {
		const bridge: AoBridge["browserProfiles"] = {
			list: vi.fn(async () => {
				throw new Error("Registry is corrupt");
			}),
			create: vi.fn(),
			rename: vi.fn(),
			clear: vi.fn(),
			usage: vi.fn(async () => ({ sessions: [] })),
			delete: vi.fn(),
			setDefault: vi.fn(),
			discoverImportSources: vi.fn(async () => ({ sources: [] })),
			import: vi.fn(async () => ({ sourceName: "", entries: [] })),
			onImportProgress: vi.fn(() => () => undefined),
		};
		originalBridge = window.ao!.browserProfiles;
		window.ao!.browserProfiles = bridge;

		render(<BrowserProfilesSection />);
		expect(await screen.findByRole("alert")).toHaveTextContent("Registry is corrupt");
	});
});
