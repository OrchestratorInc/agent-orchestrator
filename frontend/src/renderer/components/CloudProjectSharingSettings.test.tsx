import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudProjectSharingSettings } from "./CloudProjectSharingSettings";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	calls: [] as Array<[string, ...unknown[]]>,
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "user_workos",
		client: {
			listProjects: async () => ({ items: [{ id: "proj", orgId: "org", displayName: "storefront" }] }),
			getShareAccess: async (...args: unknown[]) => {
				mocks.calls.push(["getShareAccess", ...args]);
				return { entries: [] };
			},
			getTeamShareAccess: async () => ({ members: [] }),
			listShareCandidates: async () => ({ candidates: [] }),
			listShareLinks: async () => ({ links: [] }),
		},
	}),
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org", displayName: "Northwind" } }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: { user: { email: "asha@northwind.dev" } } }) }));
vi.mock("../hooks/useTeamsEnabled", () => ({ useTeamsEnabled: () => true }));

function wrap(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>{ui}</TooltipProvider>
		</QueryClientProvider>,
	);
}

/**
 * What the open project dialog offers: who the link can let in, and the
 * access someone added gets (picked from the menu beside the people field).
 */
async function projectOptions(): Promise<{ audience: string[]; access: string[] }> {
	const general = await screen.findByRole("region", { name: "General access" });
	await userEvent.click(await within(general).findByRole("button", { name: /Only people with access|Anyone with the link/ }));
	const audience = (await screen.findAllByRole("menuitemradio")).map((item) => item.textContent ?? "");
	await userEvent.keyboard("{Escape}");
	await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
	const input = screen.getByRole("combobox", { name: "Add people or teams" });
	fireEvent.focus(input);
	fireEvent.change(input, { target: { value: "dana@northwind.dev" } });
	fireEvent.keyDown(input, { key: "Enter" });
	const field = input.closest("[data-people-field]") as HTMLElement;
	await userEvent.click(await within(field).findByRole("button", { name: /Change access: Can watch/ }));
	const access = (await screen.findAllByRole("menuitem")).map((item) => item.textContent ?? "");
	await userEvent.keyboard("{Escape}");
	return { audience, access };
}

describe("Project settings › Sharing", () => {
	// Radix's focus scope moves focus from a 0 ms timer when a dialog unmounts.
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
	beforeEach(() => {
		mocks.calls = [];
	});

	it("opens the whole project's share dialog, with no session to go back to", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		expect(screen.getByText("Share this project")).toBeInTheDocument();
		expect(screen.getByText("Includes every session, also ones started later.")).toBeInTheDocument();
		expect(screen.queryByRole("dialog")).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByRole("heading", { name: "Share project “storefront”" })).toBeInTheDocument();
		expect(within(dialog).getByText("Includes every session, also ones started later.")).toBeInTheDocument();
		expect(within(dialog).queryByRole("button", { name: "Share only this session" })).toBeNull();
		// The project dialog's controls: people and teams, General access, Link settings, Copy link, Done.
		expect(await within(dialog).findByRole("combobox", { name: "Add people or teams" })).toBeInTheDocument();
		expect(within(dialog).getByRole("region", { name: "General access" })).toBeInTheDocument();
		expect(within(dialog).getByRole("button", { name: "Link settings" })).toBeInTheDocument();
		expect(within(dialog).getByRole("button", { name: "Copy link" })).toBeInTheDocument();
		expect(within(dialog).getByRole("button", { name: "Done" })).toBeInTheDocument();
		// Who has access to the whole project, never to one session.
		await waitFor(() => expect(mocks.calls).toContainEqual(["getShareAccess", "org", "proj", undefined]));
		expect(mocks.calls.filter((call) => call[0] === "getShareAccess" && call[3] !== undefined)).toEqual([]);
	});

	it("offers exactly what a session's Share the whole project instead offers", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		await screen.findByRole("heading", { name: "Share project “storefront”" });
		const fromSettings = await projectOptions();
		cleanup();

		wrap(<ShareSessionDialog open onOpenChange={() => undefined} orgId="org" projectId="proj" sessionId="sess" sessionName="Fix checkout" projectName="storefront" />);
		await userEvent.click(await screen.findByRole("button", { name: "Share the whole project instead" }));
		await screen.findByRole("heading", { name: "Share project “storefront”" });
		const fromSession = await projectOptions();

		expect(fromSettings).toEqual(fromSession);
		// Today's project options, unchanged: label then description.
		expect(fromSettings).toEqual({
			audience: [
				"Only people with accessOnly people with access can open it",
				"Anyone with the linkAnyone signed in to Orchestrator.inc who has the link",
			],
			access: [
				"Can watchSee the chat, terminal and files",
				"Can start sessionsWatch every session and ask the orchestrator for sessions of their own",
			],
		});
	});
});
