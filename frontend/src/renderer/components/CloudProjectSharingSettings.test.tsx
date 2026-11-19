import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { CloudCpShareAccess, CloudCpShareEntry, CloudCpShareLink } from "../lib/cloud-cp";
import { useUiStore } from "../stores/ui-store";
import { CloudProjectSharingSettings } from "./CloudProjectSharingSettings";
import { forgetUrlsMadeHere } from "./share/linkAccess";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TooltipProvider } from "./ui/tooltip";

const LINK = "2e149160-9436-4bd3-855c-9e3c17d9d0a2";
const SHORT = "https://orchestrator.inc/s/k7Hq2x9PmB4wR5tY8uI0oP";

const mocks = vi.hoisted(() => ({
	calls: [] as Array<[string, ...unknown[]]>,
	access: { entries: [] } as CloudCpShareAccess,
	links: [] as CloudCpShareLink[],
}));

const entry = (grantId: string, subject: CloudCpShareEntry["subject"], extra: Partial<CloudCpShareEntry> = {}): CloudCpShareEntry => ({
	grantId, subject, scope: "project", projectId: "proj", projectName: "storefront", role: "viewer", modeCap: "read-only", deniedCommands: [], source: "direct", createdAt: "2026-11-09T00:00:00Z", ...extra,
});
const link = (overrides: Partial<CloudCpShareLink> = {}): CloudCpShareLink => ({
	id: LINK, orgId: "org", projectId: "proj", role: "viewer", status: "active", accessScope: "anyone", recipients: [], interaction: "view", modeCap: "read-only",
	deniedCommands: [], createdAt: "2026-11-10T00:00:00Z", updatedAt: "2026-11-10T00:00:00Z", expiresAt: new Date(Date.now() + 7 * 86_400_000).toISOString(), ...overrides,
});

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "user_workos",
		client: {
			getShareAccess: async (...args: unknown[]) => {
				mocks.calls.push(["getShareAccess", ...args]);
				return structuredClone(mocks.access);
			},
			getTeamShareAccess: async () => ({ members: [] }),
			listShareCandidates: async () => ({ candidates: [] }),
			listShareLinks: async () => ({ links: structuredClone(mocks.links) }),
			listOrgMembers: async () => ({ members: [{ userId: "u-asha" }, { userId: "u-dana" }] }),
			createShareGrants: async (...args: unknown[]) => {
				mocks.calls.push(["createShareGrants", ...args]);
				const request = args[2] as { subjects: Array<{ email: string }>; role: CloudCpShareEntry["role"]; modeCap: CloudCpShareEntry["modeCap"] };
				const created = request.subjects.map((subject, index) => entry(`g-new-${index}`, { kind: "email", email: subject.email }, { role: request.role, modeCap: request.modeCap }));
				mocks.access.entries.push(...created);
				return { entries: structuredClone(created) };
			},
			updateShareEntry: async (...args: unknown[]) => {
				mocks.calls.push(["updateShareEntry", ...args]);
				const target = mocks.access.entries.find((other) => other.grantId === args[2])!;
				Object.assign(target, args[3]);
				return { entry: structuredClone(target) };
			},
			revokeShareGrant: async (...args: unknown[]) => {
				mocks.calls.push(["revokeShareGrant", ...args]);
				mocks.access.entries = mocks.access.entries.filter((other) => other.grantId !== args[2]);
			},
			createShareLink: async (...args: unknown[]) => {
				mocks.calls.push(["createShareLink", ...args]);
				const made = link({ code: "k7Hq2x9PmB4wR5tY8uI0oP", shortUrl: SHORT });
				mocks.links = [made];
				return { link: made };
			},
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

const pane = () => document.querySelector<HTMLElement>('[data-share-layout="inline"]');
const general = () => screen.findByRole("region", { name: "General access" });
const accessList = () => screen.findByText((_text, element) => element?.hasAttribute("data-share-access-list") ?? false);
const rowOf = (list: HTMLElement, name: string) => within(list).getByText(name).closest("[data-share-entry]") as HTMLElement;
const generalSection = () => screen.getByRole("heading", { name: "General access" }).closest("section")!;

async function addPerson(email: string) {
	const input = await screen.findByRole("combobox", { name: "Add people or teams" });
	fireEvent.focus(input);
	fireEvent.change(input, { target: { value: email } });
	fireEvent.keyDown(input, { key: "Enter" });
	return input.closest("[data-people-field]") as HTMLElement;
}

/**
 * What the project's sharing offers: who the link can let in, and the
 * access someone added gets (picked from the menu beside the people field).
 */
async function projectOptions(): Promise<{ audience: string[]; access: string[] }> {
	await userEvent.click(await within(await general()).findByRole("button", { name: /Only people with access|Anyone with the link/ }));
	const audience = (await screen.findAllByRole("menuitemradio")).map((item) => item.textContent ?? "");
	await userEvent.keyboard("{Escape}");
	await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
	const field = await addPerson("dana@northwind.dev");
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
		vi.restoreAllMocks();
	});
	beforeEach(() => {
		mocks.calls = [];
		mocks.access = { entries: [entry("g-data", { kind: "team", teamId: "t-data", teamName: "Data infra", memberCount: 4 }, { source: "team" })] };
		mocks.links = [];
		forgetUrlsMadeHere();
		useUiStore.setState({ globalToasts: [] });
	});

	it("shows the project's sharing on the page, with no Share button, no dialog and no Done", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		const list = await accessList();
		expect(pane()).toHaveAttribute("data-share-panel", "project");
		expect(screen.getByText("Includes every session, also ones started later.")).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Add people or teams" })).toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "People with access" })).toBeInTheDocument();
		expect(within(list).getByText("Everyone in your workspace")).toBeInTheDocument();
		expect(within(list).getByText("Northwind · 2 members")).toBeInTheDocument();
		expect(within(list).getByText("Data infra")).toBeInTheDocument();
		// General access, with Link settings and Copy link beside its heading.
		const section = generalSection();
		expect(within(section).getByRole("region", { name: "General access" })).toBeInTheDocument();
		expect(within(section).getByRole("button", { name: "Copy link" })).toBeInTheDocument();
		expect(within(section).getByRole("button", { name: "Link settings" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
		expect(screen.queryByRole("dialog")).toBeNull();
		// Who has access to the whole project, never to one session.
		expect(mocks.calls).toContainEqual(["getShareAccess", "org", "proj", undefined]);
		expect(mocks.calls.filter((call) => call[0] === "getShareAccess" && call[3] !== undefined)).toEqual([]);
	});

	it("adds a person from the page at the access picked, then lists them", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		await accessList();
		const field = await addPerson("dana@northwind.dev");
		// The confirm step takes the lists' place, with Cancel and Share under it.
		expect(await screen.findByText("They'll find it under Shared with you.")).toBeInTheDocument();
		expect(screen.queryByRole("region", { name: "General access" })).toBeNull();
		await userEvent.click(within(field).getByRole("button", { name: /Change access: Can watch/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can start sessions/ }));
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "createShareGrants")).toEqual([
			"createShareGrants", "org", "proj", { subjects: [{ email: "dana@northwind.dev" }], role: "editor", modeCap: "standard" },
		]));
		const list = await accessList();
		await waitFor(() => expect(rowOf(list, "dana@northwind.dev").textContent).toContain("Can start sessions"));
		expect(screen.queryByRole("dialog")).toBeNull();
	});

	it("changes and removes someone's access from the page, with Undo on the page", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		const list = await accessList();
		await userEvent.click(within(rowOf(list, "Data infra")).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can start sessions/ }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "updateShareEntry")).toEqual(["updateShareEntry", "org", "proj", "g-data", { role: "editor", modeCap: "standard" }]));

		await userEvent.click(within(rowOf(list, "Data infra")).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Remove access" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "revokeShareGrant")).toEqual(["revokeShareGrant", "org", "proj", "g-data"]));
		await waitFor(() => expect(within(list).queryByText("Data infra")).toBeNull());
		// The last share gone, the workspace's people still have access: the list stays, with them on it.
		expect(screen.getByRole("heading", { name: "People with access" })).toBeInTheDocument();
		expect(within(list).getByText("Everyone in your workspace")).toBeInTheDocument();
		const notice = await within(pane()!).findByRole("status");
		expect(notice).toHaveTextContent("Removed Data infra's access");
		expect(within(notice).getByRole("button", { name: "Undo" })).toBeInTheDocument();
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
	});

	it("turns link access on and copies the link from General access, and asks before turning it off", async () => {
		vi.spyOn(aoBridge.sessionShareUrls, "remember").mockResolvedValue(undefined);
		vi.spyOn(aoBridge.sessionShareUrls, "recall").mockResolvedValue({ [LINK]: SHORT });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		await userEvent.click(await within(await general()).findByRole("button", { name: "Only people with access" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /Anyone with the link/ }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "createShareLink")).toEqual([
			"createShareLink", "org", "proj", { role: "viewer", accessScope: "anyone", interaction: "view", modeCap: "read-only", expiresInHours: 168 },
		]));
		await waitFor(async () => expect((await general()).querySelector("[data-general-access-sentence]")?.textContent).toContain("Anyone signed in to Orchestrator.inc with the link can watch every session in this project"));
		const copy = within(generalSection()).getByRole("button", { name: "Copy link" });
		await waitFor(() => expect(copy).toHaveAttribute("data-share-copy-link", "ready"));
		await userEvent.click(copy);
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
		// Turning it off still asks first.
		await userEvent.click(within(await general()).getByRole("button", { name: "Anyone with the link" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /Only people with access/ }));
		expect(await screen.findByRole("dialog", { name: "Turn off link access?" })).toBeInTheDocument();
	});

	it("steps back out of the confirm step with Esc, and marks itself so Settings stays open meanwhile", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		await accessList();
		expect(pane()).not.toHaveAttribute("data-settings-inline-edit");
		await addPerson("dana@northwind.dev");
		await screen.findByText("They'll find it under Shared with you.");
		// Project settings closes on Esc unless the press came from here.
		expect(pane()).toHaveAttribute("data-settings-inline-edit");
		fireEvent.keyDown(pane()!, { key: "Escape" });
		await waitFor(() => expect(screen.queryByText("They'll find it under Shared with you.")).toBeNull());
		expect(await general()).toBeInTheDocument();
		expect(pane()).not.toHaveAttribute("data-settings-inline-edit");
	});

	it("is the panel the share dialog shows for the whole project, offering the same options", async () => {
		wrap(<CloudProjectSharingSettings projectId="proj" cloudOrgId="org" />);
		await accessList();
		const fromSettings = await projectOptions();
		cleanup();

		wrap(<ShareSessionDialog open onOpenChange={() => undefined} orgId="org" projectId="proj" sessionId="sess" sessionName="Fix checkout" projectName="storefront" />);
		await userEvent.click(await screen.findByRole("button", { name: "Share the whole project instead" }));
		await screen.findByRole("heading", { name: "Share project “storefront”" });
		expect(screen.getByRole("dialog").querySelector('[data-share-layout="dialog"]')).toHaveAttribute("data-share-panel", "project");
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
