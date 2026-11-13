import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpShareAccess, CloudCpShareCandidate, CloudCpShareEntry, CloudCpTeamMemberAccess } from "../lib/cloud-cp";
import { useUiStore } from "../stores/ui-store";
import { GlobalToast } from "./GlobalToast";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TooltipProvider } from "./ui/tooltip";

type Gate = { promise: Promise<void>; open: () => void; fail: (error: Error) => void };
function gate(): Gate {
	let open: () => void = () => undefined;
	let fail: (error: Error) => void = () => undefined;
	const promise = new Promise<void>((resolve, reject) => {
		open = resolve;
		fail = reject;
	});
	return { promise, open, fail };
}

const mocks = vi.hoisted(() => ({
	access: undefined as unknown as CloudCpShareAccess,
	/** What the whole project's list returns, when it differs from the session's. */
	projectAccess: undefined as CloudCpShareAccess | undefined,
	teamMembers: {} as Record<string, CloudCpTeamMemberAccess[]>,
	candidates: [] as CloudCpShareCandidate[],
	calls: [] as Array<[string, ...unknown[]]>,
	/** Hold a call until the test opens its gate. */
	gates: {} as Record<string, Array<{ promise: Promise<void> }>>,
	/** What the next createShareGrants answers with. */
	created: [] as CloudCpShareEntry[],
	failCreate: false,
	failUpdate: false,
	holdProjectReads: false,
}));

async function held(name: string) {
	const next = mocks.gates[name]?.shift();
	if (next) await next.promise;
}

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "user_workos",
		client: {
			getShareAccess: async (_org: string, _project: string, sessionId?: string) => {
				mocks.calls.push(["getShareAccess", sessionId]);
				if (!sessionId && mocks.projectAccess) {
					if (mocks.holdProjectReads) await new Promise(() => undefined);
					return structuredClone(mocks.projectAccess);
				}
				return structuredClone(mocks.access);
			},
			getTeamShareAccess: async (_org: string, _project: string, _session: string | undefined, teamId: string) => ({ members: mocks.teamMembers[teamId] ?? [] }),
			listShareCandidates: async () => ({ candidates: mocks.candidates }),
			listShareLinks: async () => ({ links: [] }),
			createShareGrants: async (...args: unknown[]) => {
				mocks.calls.push(["createShareGrants", ...args]);
				await held("createShareGrants");
				if (mocks.failCreate) throw new Error("The control plane is unavailable.");
				const created = structuredClone(mocks.created);
				mocks.access.entries.push(...created);
				return { entries: created };
			},
			updateShareEntry: async (...args: unknown[]) => {
				mocks.calls.push(["updateShareEntry", ...args]);
				await held("updateShareEntry");
				if (mocks.failUpdate) throw new Error("Couldn't change access.");
				const target = mocks.access.entries.find((entry) => entry.grantId === args[2])!;
				Object.assign(target, args[3]);
				return { entry: structuredClone(target) };
			},
			revokeShareGrant: async (...args: unknown[]) => {
				mocks.calls.push(["revokeShareGrant", ...args]);
				await held("revokeShareGrant");
				mocks.access.entries = mocks.access.entries.filter((entry) => entry.grantId !== args[2]);
			},
		},
	}),
}));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: { user: { email: "asha@northwind.dev" } } }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org", displayName: "Northwind" } }) }));

const member = (id: string, name: string, access: CloudCpTeamMemberAccess["access"], extra: Partial<CloudCpTeamMemberAccess["member"]> = {}): CloudCpTeamMemberAccess => ({
	member: { id, teamId: "t-platform", name, email: `${name.toLowerCase().split(" ")[0]}@northwind.dev`, role: "member", invited: false, you: false, workspaceMember: false, addedAt: "2026-10-30T00:00:00Z", ...extra },
	access,
});

const entry = (grantId: string, subject: CloudCpShareEntry["subject"], extra: Partial<CloudCpShareEntry> = {}): CloudCpShareEntry => ({
	grantId, subject, scope: "session", sessionId: "sess", projectId: "proj", projectName: "storefront", role: "viewer", modeCap: "read-only", deniedCommands: [], source: "direct", createdAt: "2026-10-30T00:00:00Z", ...extra,
});

function renderDialog(props: { onOpenChange?: (open: boolean) => void; standaloneAgent?: boolean } = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = (open: boolean) => (
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ShareSessionDialog open={open} onOpenChange={props.onOpenChange ?? (() => undefined)} orgId="org" projectId="proj" sessionId="sess" standaloneAgent={props.standaloneAgent} />
				<GlobalToast />
			</TooltipProvider>
		</QueryClientProvider>
	);
	const result = render(view(true));
	return { ...result, client, reopen: (open: boolean) => result.rerender(view(open)) };
}

const accessList = () => screen.findByText((_text, element) => element?.hasAttribute("data-share-access-list") ?? false);
const row = (list: HTMLElement, name: string) => within(list).getByText(name).closest("[data-share-entry]") as HTMLElement;
const menuOf = (element: HTMLElement) => within(element).getByRole("button", { name: /Change access/ });

async function stopSharing(list: HTMLElement, name: string) {
	await userEvent.click(menuOf(row(list, name)));
	await userEvent.click(await screen.findByRole("menuitem", { name: "Remove access" }));
}

async function stageCheckout() {
	const input = screen.getByRole("combobox", { name: "Add people or teams" });
	fireEvent.focus(input);
	fireEvent.change(input, { target: { value: "check" } });
	fireEvent.click(await screen.findByText("Checkout"));
	return input;
}

describe("ShareSessionDialog people and teams", () => {
	beforeEach(() => {
		mocks.calls = [];
		mocks.gates = {};
		mocks.failCreate = false;
		mocks.failUpdate = false;
		mocks.holdProjectReads = false;
		mocks.projectAccess = undefined;
		useUiStore.setState({ globalToasts: [] });
		mocks.access = {
			owner: { kind: "user", userId: "u-asha", name: "Asha Rao", email: "asha@northwind.dev", workspaceMember: true },
			entries: [
				entry("g-platform", { kind: "team", teamId: "t-platform", teamName: "Platform", memberCount: 6 }, { role: "editor", modeCap: "standard", source: "team" }),
				entry("g-riya", { kind: "user", userId: "u-riya", name: "Riya Sen", email: "riya@acme.co" }, { source: "link" }),
				entry("g-data", { kind: "team", teamId: "t-data", teamName: "Data infra", memberCount: 4 }, { scope: "project", sessionId: undefined, source: "team" }),
				entry("g-sam", { kind: "email", email: "sam@contractor.io" }),
			],
		};
		mocks.created = [entry("g-checkout", { kind: "team", teamId: "t-checkout", teamName: "Checkout", memberCount: 6 }, { source: "team" })];
		mocks.teamMembers = {
			"t-platform": [
				member("m1", "Ben Ortiz", { role: "editor", modeCap: "standard", source: "team", teamId: "t-platform", teamName: "Platform" }),
				member("m2", "Chen Wei", { role: "editor", modeCap: "trusted", source: "link" }),
				member("m3", "Farah Nasser", { role: "member", source: "workspace" }, { workspaceMember: true }),
				member("m4", "", { role: "editor", source: "team" }, { name: undefined, email: "eli@northwind.dev", invited: true }),
			],
			"t-checkout": [
				member("c1", "Ben Ortiz", { role: "editor", modeCap: "standard", source: "team", teamId: "t-platform", teamName: "Platform" }),
				member("c2", "Dana Kim", {}),
				member("c3", "Omar Haddad", {}),
				member("c4", "Farah Nasser", { role: "member", source: "workspace" }, { workspaceMember: true }),
				member("c5", "Asha Rao", { role: "member", source: "workspace" }, { you: true, workspaceMember: true }),
				member("c6", "", {}, { name: undefined, email: "sam@contractor.io", invited: true }),
			],
		};
		mocks.candidates = [{ kind: "team", teamId: "t-checkout", name: "Checkout", memberCount: 6, memberNames: ["Ben", "Dana"] }];
	});

	it("lists access broadest first: owner, workspace, the project's shares, teams, people, pending emails", async () => {
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		expect(within(list).getByText(/Asha Rao/).textContent).toContain("(you)");
		expect(within(list).getByText("Owner")).toBeInTheDocument();
		const workspace = list.querySelector("[data-share-workspace-row]")!;
		expect(workspace.textContent).toContain("People in Northwind");
		expect(workspace.textContent).toContain("Full access");
		const rows = [...list.querySelectorAll("[data-share-entry]")];
		expect(rows.map((element) => element.getAttribute("data-share-row"))).toEqual([
			"project::team:t-data", "session:sess:team:t-platform", "session:sess:email:riya@acme.co", "session:sess:email:sam@contractor.io",
		]);
		// The project's share applies here but is changed at project level.
		expect(rows[0]!.textContent).toContain("from project");
		expect(rows[0]!.querySelector("[data-access-menu]")).toBeNull();
		expect(rows[0]!.querySelector("[data-share-locked]")).toHaveAttribute("aria-disabled", "true");
		expect(rows[1]!.textContent).toContain("6 people");
		expect(rows[1]!.textContent).toContain("Can write");
		expect(rows[2]!.textContent).toContain("via link");
		expect(rows[3]!.textContent).toContain("Added by email · gets access when they sign in");
	});

	it("keeps 'from project' whole, so it can be clicked however long the email", async () => {
		mocks.access.entries[2] = entry("g-long", { kind: "user", userId: "u-long", name: "Someone With A Long Name", email: `${"a".repeat(80)}@northwind.dev` }, { scope: "project", sessionId: undefined });
		renderDialog();
		const list = await accessList();
		const button = await within(list).findByRole("button", { name: "from project" });
		// The email shortens; the link never sits in the part that is cut off.
		expect(button.closest(".truncate")).toBeNull();
		expect(within(row(list, "Someone With A Long Name")).getByText(/a{80}@northwind\.dev/).closest(".truncate")).not.toBeNull();
		await userEvent.click(button);
		expect(await screen.findByRole("heading", { name: "Share project" })).toBeInTheDocument();
	});

	it("puts the workspace on top of a project's list, as a session's owner is on top of its own", async () => {
		mocks.projectAccess = { entries: [entry("g-data", { kind: "team", teamId: "t-data", teamName: "Data infra", memberCount: 4 }, { scope: "project", sessionId: undefined, source: "team" })] };
		renderDialog();
		await screen.findByText("Platform");
		await userEvent.click(screen.getByRole("button", { name: "Share the whole project instead" }));
		const workspace = await screen.findByText("People in Northwind");
		const first = workspace.closest("li")!;
		expect(first.hasAttribute("data-share-workspace-row")).toBe(true);
		expect(first.textContent).toContain("Full access");
		expect(first.parentElement!.firstElementChild).toBe(first);
	});

	it("shows a team's people its new access at once, before the change is saved", async () => {
		const saved = gate();
		mocks.gates.updateShareEntry = [saved];
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Show the people in Platform" }));
		const line = async (name: string) => (await screen.findAllByText((_text, element) => element?.hasAttribute("data-share-member") ?? false))
			.find((element) => element.textContent?.includes(name))!.textContent;
		expect(await line("Ben Ortiz")).toContain("Can write");
		const list = await accessList();
		await userEvent.click(menuOf(row(list, "Platform")));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can watch/ }));
		// Still saving: Ben had his access through Platform, so he now watches;
		// Chen has more through a link and keeps it.
		await waitFor(async () => expect(await line("Ben Ortiz")).toContain("Can watch"));
		expect(await line("Chen Wei")).toContain("Trusted");
		await act(async () => saved.open());
	});

	it("opens with focus in the people field", async () => {
		renderDialog();
		const input = await screen.findByRole("combobox", { name: "Add people or teams" });
		await waitFor(() => expect(document.activeElement).toBe(input));
	});

	it("expands a team to show what each member ends up with and why", async () => {
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Show the people in Platform" }));
		const members = await screen.findAllByText((_text, element) => element?.hasAttribute("data-share-member") ?? false);
		const lines = members.map((element) => element.textContent);
		expect(lines[0]).toContain("Ben Ortiz");
		expect(lines[0]).toContain("Can write");
		expect(lines[1]).toContain("Trusted · shared directly");
		expect(lines[2]).toContain("Full access · workspace member");
		expect(lines[3]).toContain("after signing in");
		expect(screen.getByText("People with access more than one way get the highest.")).toBeInTheDocument();
	});

	it("confirms a team share by naming who gets what, then shares at the chosen level", async () => {
		renderDialog();
		await screen.findByText("Platform");
		const input = await stageCheckout();
		// Default level: watch.
		const summary = await screen.findByText(/Dana Kim and Omar Haddad get Can watch\./);
		expect(summary.textContent).toContain("Ben Ortiz already has this or more.");
		expect(summary.textContent).toContain("Farah Nasser already has full access as a workspace member.");
		expect(summary.textContent).toContain("sam@contractor.io gets Can watch after signing in.");
		expect(summary.textContent).not.toContain("Asha");
		// Raise to writing; the limit appears.
		const field = input.closest("[data-people-field]") as HTMLElement;
		await userEvent.click(within(field).getByRole("button", { name: /Change access: Can watch/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can write/ }));
		// Ben already writes through Platform at the same limit, so he still keeps what he has.
		const raised = await screen.findByText(/Dana Kim and Omar Haddad get Can write\./);
		expect(raised.textContent).toContain("Ben Ortiz already has this or more.");
		expect(screen.getByRole("button", { name: "Agent permissions" })).toHaveTextContent("Standard");
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "createShareGrants")).toBeTruthy());
		expect(mocks.calls.find((call) => call[0] === "createShareGrants")).toEqual([
			"createShareGrants", "org", "proj", { sessionId: "sess", subjects: [{ teamId: "t-checkout" }], role: "editor", modeCap: "standard" },
		]);
	});

	it("names workspace members a team share of a Cloud standalone agent reaches as getting access", async () => {
		// The control plane weighs a workspace member by what is shared with
		// them in another person's agent, so Farah has nothing yet.
		mocks.teamMembers["t-checkout"] = mocks.teamMembers["t-checkout"]!.map((entry) =>
			entry.member.name === "Farah Nasser" ? { ...entry, access: {} } : entry);
		renderDialog({ standaloneAgent: true });
		await screen.findByText("Platform");
		await stageCheckout();
		const summary = await screen.findByText(/Farah Nasser get Can watch\./);
		expect(summary.textContent).toContain("Dana Kim");
		expect(summary.textContent).not.toContain("workspace member");
	});

	it("shows a share in the list at once, then as the grant the control plane made", async () => {
		const answer = gate();
		mocks.gates.createShareGrants = [answer];
		renderDialog();
		await screen.findByText("Platform");
		const input = await stageCheckout();
		await screen.findByText(/get Can watch/);
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		// Before the answer: the row is there, waiting, and focus is back in the field.
		const list = await accessList();
		const pending = row(list, "Checkout");
		expect(pending).toHaveAttribute("aria-busy", "true");
		expect(menuOf(pending)).toBeDisabled();
		expect(document.activeElement).toBe(input);
		expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
		await act(async () => answer.open());
		await waitFor(() => expect(row(list, "Checkout")).not.toHaveAttribute("aria-busy"));
		expect(menuOf(row(list, "Checkout"))).toBeEnabled();
	});

	it("keeps people picked while a share is on its way, and sends a double Enter once", async () => {
		const answer = gate();
		mocks.gates.createShareGrants = [answer];
		renderDialog();
		await screen.findByText("Platform");
		const input = await stageCheckout();
		fireEvent.keyDown(input, { key: "Enter" });
		fireEvent.keyDown(input, { key: "Enter" });
		await waitFor(() => expect(mocks.calls.filter((call) => call[0] === "createShareGrants")).toHaveLength(1));
		const field = input.closest("[data-people-field]") as HTMLElement;
		fireEvent.change(input, { target: { value: "lee@acme.co" } });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(field.textContent).toContain("lee@acme.co");
		await act(async () => answer.open());
		await waitFor(() => expect(mocks.calls.filter((call) => call[0] === "getShareAccess").length).toBeGreaterThan(1));
		expect(field.textContent).toContain("lee@acme.co");
		expect(mocks.calls.filter((call) => call[0] === "createShareGrants")).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		const list = await accessList();
		expect(row(list, "Checkout")).not.toHaveAttribute("aria-busy");
	});

	it("puts the picks back with the error when sharing fails", async () => {
		mocks.failCreate = true;
		renderDialog();
		await screen.findByText("Platform");
		const input = await stageCheckout();
		const field = input.closest("[data-people-field]") as HTMLElement;
		await userEvent.click(within(field).getByRole("button", { name: /Change access: Can watch/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can write/ }));
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("The control plane is unavailable.");
		expect(field.textContent).toContain("Checkout");
		expect(within(field).getByRole("button", { name: /Change access/ })).toHaveTextContent("Can write");
		expect(document.querySelector("[data-share-row$='team:t-checkout']")).toBeNull();
	});

	it("starts the next share at Watch, without old errors, after Cancel or closing", async () => {
		mocks.failUpdate = true;
		const view = renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await userEvent.click(menuOf(row(list, "Platform")));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can watch/ }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't change access.");
		// The failed change is undone in place.
		expect(row(list, "Platform").textContent).toContain("Can write");
		const input = await stageCheckout();
		const field = input.closest("[data-people-field]") as HTMLElement;
		await userEvent.click(within(field).getByRole("button", { name: /Change access: Can watch/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can write/ }));
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(document.activeElement).toBe(input);
		await stageCheckout();
		expect(within(field).getByRole("button", { name: /Change access/ })).toHaveTextContent("Can watch");
		view.reopen(false);
		view.reopen(true);
		await screen.findByText("Platform");
		expect(screen.queryByRole("alert")).toBeNull();
		expect(screen.queryByText("Checkout")).toBeNull();
	});

	it("rolls a failed change back in the list it changed, not the one now shown", async () => {
		mocks.failUpdate = true;
		mocks.projectAccess = { entries: [entry("g-data", { kind: "team", teamId: "t-data", teamName: "Data infra", memberCount: 4 }, { scope: "project", sessionId: undefined, source: "team" })] };
		const answer = gate();
		mocks.gates.updateShareEntry = [answer];
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await userEvent.click(menuOf(row(list, "Platform")));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can watch/ }));
		await userEvent.click(screen.getByRole("button", { name: "Share the whole project instead" }));
		const projectList = await accessList();
		await within(projectList).findByText("Data infra");
		// The project's list is never read again, so what it shows is what the cache holds.
		mocks.holdProjectReads = true;
		await act(async () => answer.open());
		await screen.findByRole("alert");
		expect(within(projectList).queryByText("Platform")).toBeNull();
		expect(within(projectList).getByText("Data infra")).toBeInTheDocument();
	});

	it("doesn't send a change that picks what is already set", async () => {
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await userEvent.click(menuOf(row(list, "Platform")));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can write/ }));
		await userEvent.click(menuOf(row(list, "Platform")));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Standard" }));
		expect(mocks.calls.filter((call) => call[0] === "updateShareEntry")).toEqual([]);
	});

	it("changes a row's access in place and stops sharing with Undo inside the dialog", async () => {
		mocks.access.entries[0]!.deniedCommands = ["rm -rf"];
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await userEvent.click(menuOf(row(list, "Platform")));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can watch/ }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "updateShareEntry")).toEqual(["updateShareEntry", "org", "proj", "g-platform", { role: "viewer", modeCap: "read-only" }]));

		await stopSharing(list, "Platform");
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "revokeShareGrant")).toEqual(["revokeShareGrant", "org", "proj", "g-platform"]));
		// The offer is in the dialog, reachable by keyboard and screen reader, not behind it.
		const dialog = screen.getByRole("dialog");
		const notice = await within(dialog).findByRole("status");
		expect(notice).toHaveTextContent("Removed Platform's access");
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
		mocks.created = [entry("g-platform-2", { kind: "team", teamId: "t-platform", teamName: "Platform", memberCount: 6 }, { source: "team" })];
		await userEvent.click(within(notice).getByRole("button", { name: "Undo" }));
		// Back at once, then as the new grant, with the same access, limit and denied commands.
		expect(within(list).getByText("Platform")).toBeInTheDocument();
		await waitFor(() => expect(mocks.calls.filter((call) => call[0] === "createShareGrants").at(-1)).toEqual([
			"createShareGrants", "org", "proj", { sessionId: "sess", subjects: [{ teamId: "t-platform" }], role: "viewer", modeCap: "read-only", deniedCommands: ["rm -rf"] },
		]));
		expect(within(dialog).queryByRole("status")).toBeNull();
		expect(row(list, "Platform").textContent).toContain("Can watch");
	});

	it("says so when Undo fails, and takes the row away again", async () => {
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await stopSharing(list, "Platform");
		const notice = await within(screen.getByRole("dialog")).findByRole("status");
		mocks.failCreate = true;
		await userEvent.click(within(notice).getByRole("button", { name: "Undo" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't restore access. Check your connection and share again.");
		await waitFor(() => expect(within(list).queryByText("Platform")).toBeNull());
	});

	it("moves focus to the next row after stopping one", async () => {
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await stopSharing(list, "Platform");
		await waitFor(() => expect(within(list).queryByText("Platform")).toBeNull());
		await waitFor(() => expect(document.activeElement).toBe(menuOf(row(list, "Riya Sen"))));
	});

	it("keeps both rows gone when two are stopped in quick succession", async () => {
		const first = gate();
		const second = gate();
		mocks.gates.revokeShareGrant = [first, second];
		renderDialog();
		const list = await accessList();
		await within(list).findByText("Platform");
		await stopSharing(list, "Platform");
		await stopSharing(list, "sam@contractor.io");
		const reads = mocks.calls.filter((call) => call[0] === "getShareAccess").length;
		await act(async () => first.open());
		// Nothing is read back while the second stop is on its way, so neither row returns.
		await act(async () => new Promise((resolve) => setTimeout(resolve, 20)));
		expect(within(list).queryByText("Platform")).toBeNull();
		expect(within(list).queryByText("sam@contractor.io")).toBeNull();
		expect(mocks.calls.filter((call) => call[0] === "getShareAccess")).toHaveLength(reads);
		await act(async () => second.open());
		await waitFor(() => expect(mocks.calls.filter((call) => call[0] === "getShareAccess").length).toBeGreaterThan(reads));
		expect(within(list).queryByText("Platform")).toBeNull();
		expect(within(list).queryByText("sam@contractor.io")).toBeNull();
	});

	it("hands Undo to the global toast when the dialog closes", async () => {
		const onOpenChange = vi.fn();
		const view = renderDialog({ onOpenChange });
		const list = await accessList();
		await within(list).findByText("Platform");
		await stopSharing(list, "Platform");
		await within(screen.getByRole("dialog")).findByRole("status");
		view.reopen(false);
		await waitFor(() => expect(useUiStore.getState().globalToasts.at(-1)?.title).toBe("Removed Platform's access"));
		await userEvent.click(await screen.findByRole("button", { name: "Undo" }));
		await waitFor(() => expect(mocks.calls.filter((call) => call[0] === "createShareGrants")).toHaveLength(1));
		expect(onOpenChange).not.toHaveBeenCalledWith(true);
	});

	it("names a new team after what was typed when New team is picked", async () => {
		renderDialog();
		await screen.findByText("Platform");
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		fireEvent.change(input, { target: { value: "Payments" } });
		await screen.findByText("New team…");
		fireEvent.keyDown(input, { key: "ArrowUp" });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(await screen.findByRole("textbox", { name: "Name" })).toHaveValue("Payments");
	});

	it("shares the whole project without naming a session, and comes back to the session", async () => {
		renderDialog();
		await screen.findByText("Platform");
		await userEvent.click(screen.getByRole("button", { name: "Share the whole project instead" }));
		expect(await screen.findByRole("heading", { name: "Share project" })).toBeInTheDocument();
		expect(screen.getByText("Includes every session, also ones started later.")).toBeInTheDocument();
		await waitFor(() => expect(mocks.calls.some((call) => call[0] === "getShareAccess" && call[1] === undefined)).toBe(true));
		await userEvent.click(screen.getByRole("button", { name: "Share only this session" }));
		expect(await screen.findByRole("heading", { name: "Share session" })).toBeInTheDocument();
	});

	it("closes the confirm step with Esc before it closes the dialog", async () => {
		const onOpenChange = vi.fn();
		renderDialog({ onOpenChange });
		await screen.findByText("Platform");
		await stageCheckout();
		expect(await screen.findByText("They'll find it under Shared with you.")).toBeInTheDocument();
		fireEvent.keyDown(document.activeElement!, { key: "Escape" });
		await waitFor(() => expect(screen.queryByRole("button", { name: "Share" })).toBeNull());
		expect(onOpenChange).not.toHaveBeenCalled();
		expect(await screen.findByRole("region", { name: "General access" })).toBeInTheDocument();
	});
});
