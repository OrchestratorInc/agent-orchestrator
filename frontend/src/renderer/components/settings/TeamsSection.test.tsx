import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { CloudCpError, type CloudCpShareCandidate, type CloudCpShareEntry, type CloudCpTeam, type CloudCpTeamDetail, type CloudCpTeamImpact, type CloudCpTeamMember } from "../../lib/cloud-cp";
import { useUiStore } from "../../stores/ui-store";
import { lostText, TeamsSection } from "./TeamsSection";

type Gate = { promise: Promise<void>; open: () => void };
function gate(): Gate {
	let open: () => void = () => undefined;
	const promise = new Promise<void>((resolve) => {
		open = resolve;
	});
	return { promise, open };
}

const mocks = vi.hoisted(() => ({
	ready: true,
	org: { id: "org-north", name: "Northwind" } as { id: string; name: string } | undefined,
	orgError: undefined as unknown,
	orgRefetch: vi.fn(),
	teams: [] as CloudCpTeam[],
	myTeams: [] as CloudCpTeam[],
	teamsError: undefined as unknown,
	details: {} as Record<string, CloudCpTeamDetail>,
	impact: { people: 0, sessions: 0, projects: 0, keeps: [] } as CloudCpTeamImpact,
	candidates: [] as CloudCpShareCandidate[],
	calls: [] as Array<[string, ...unknown[]]>,
	gates: {} as Record<string, Array<{ promise: Promise<void> }>>,
	fail: {} as Record<string, Error | undefined>,
}));

async function step(name: string, ...args: unknown[]) {
	mocks.calls.push([name, ...args]);
	const next = mocks.gates[name]?.shift();
	if (next) await next.promise;
	const failure = mocks.fail[name];
	if (failure) throw failure;
}

vi.mock("../../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: mocks.org, error: mocks.orgError, refetch: mocks.orgRefetch, isLoading: false, ready: mocks.ready }),
}));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: mocks.ready,
		baseUrl: "https://cp.test",
		userId: "me",
		client: {
			listTeams: async () => {
				await step("listTeams");
				if (mocks.teamsError) throw mocks.teamsError;
				return { teams: structuredClone(mocks.teams) };
			},
			listMyTeams: async () => {
				await step("listMyTeams");
				if (mocks.teamsError) throw mocks.teamsError;
				return { teams: structuredClone(mocks.myTeams) };
			},
			getTeam: async (_org: string, teamId: string) => {
				await step("getTeam", teamId);
				return structuredClone(mocks.details[teamId]);
			},
			getMyTeam: async (teamId: string) => {
				await step("getMyTeam", teamId);
				return structuredClone(mocks.details[teamId]);
			},
			teamImpact: async (...args: unknown[]) => {
				await step("teamImpact", ...args);
				return mocks.impact;
			},
			teamMemberImpact: async (...args: unknown[]) => {
				await step("teamMemberImpact", ...args);
				return mocks.impact;
			},
			leaveTeamImpact: async (...args: unknown[]) => {
				await step("leaveTeamImpact", ...args);
				return mocks.impact;
			},
			deleteTeam: async (...args: unknown[]) => step("deleteTeam", ...args),
			removeTeamMember: async (...args: unknown[]) => {
				await step("removeTeamMember", ...args);
				const detail = mocks.details[args[1] as string]!;
				detail.members = detail.members.filter((other) => other.id !== args[2]);
			},
			leaveTeam: async (...args: unknown[]) => step("leaveTeam", ...args),
			updateTeamMember: async (...args: unknown[]) => {
				await step("updateTeamMember", ...args);
				const detail = mocks.details[args[1] as string]!;
				detail.members = detail.members.map((other) => (other.id === args[2] ? { ...other, role: args[3] as "owner" | "member" } : other));
				return structuredClone(detail);
			},
			renameTeam: async (...args: unknown[]) => {
				await step("renameTeam", ...args);
				const detail = mocks.details[args[1] as string]!;
				detail.team.name = args[2] as string;
				return structuredClone(detail);
			},
			addTeamMembers: async (...args: unknown[]) => {
				await step("addTeamMembers", ...args);
				const detail = mocks.details[args[1] as string]!;
				for (const { email } of args[2] as Array<{ email: string }>) detail.members.push(person(`m-${email}`, undefined, email, { invited: true }));
				detail.team.memberCount = detail.members.length;
				return structuredClone(detail);
			},
			createTeam: async (...args: unknown[]) => {
				await step("createTeam", ...args);
				const created: CloudCpTeamDetail = { team: team("t-new", (args[1] as { name: string }).name, { memberCount: 1 }), members: [person("m-me", "Asha Rao", "asha@northwind.dev", { role: "owner", you: true, teamId: "t-new" })], shares: [] };
				return created;
			},
			updateShareEntry: async (...args: unknown[]) => {
				await step("updateShareEntry", ...args);
				return { entry: {} };
			},
			revokeShareGrant: async (...args: unknown[]) => {
				await step("revokeShareGrant", ...args);
				const detail = mocks.details["t-platform"]!;
				detail.shares = detail.shares.filter((other) => other.grantId !== args[2]);
			},
			createShareGrants: async (...args: unknown[]) => {
				await step("createShareGrants", ...args);
				const made = share("g-new", { role: "editor", modeCap: "trusted" });
				mocks.details["t-platform"]!.shares.push(made);
				return { entries: [made] };
			},
			listShareCandidates: async () => ({ candidates: mocks.candidates }),
		},
	}),
}));

const team = (id: string, name: string, extra: Partial<CloudCpTeam> = {}): CloudCpTeam => ({
	id, orgId: "org-north", name, memberCount: 3, canManage: true, yourRole: "owner", sharedSessions: 0, sharedProjects: 0, createdAt: "2026-10-30T00:00:00Z", updatedAt: "2026-10-30T00:00:00Z", ...extra,
});
const person = (id: string, name: string | undefined, email: string, extra: Partial<CloudCpTeamMember> = {}): CloudCpTeamMember => ({
	id, teamId: "t-platform", name, email, role: "member", invited: false, you: false, workspaceMember: false, addedAt: "2026-10-30T00:00:00Z", ...extra,
});
const share = (grantId: string, extra: Partial<CloudCpShareEntry> = {}): CloudCpShareEntry => ({
	grantId, subject: { kind: "team", teamId: "t-platform", teamName: "Platform" }, scope: "session", sessionId: "s-fix", sessionName: "Fix checkout", projectId: "p-store", projectName: "storefront",
	role: "editor", modeCap: "trusted", deniedCommands: [], source: "team", sharedByName: "Asha Rao", createdAt: "2026-10-30T00:00:00Z", ...extra,
});

function renderSection(props: { manage?: boolean } = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><TeamsSection titleHidden={false} {...props} /></QueryClientProvider>);
}

const teamPage = () => screen.findByText((_text, element) => element?.hasAttribute("data-team-page") ?? false);
const teamName = () => screen.getByText((_text, element) => element?.hasAttribute("data-team-name") ?? false);
const shareRow = async (name: string) => (await screen.findByText(name)).closest("[data-team-share]") as HTMLElement;

describe("TeamsSection", () => {
	beforeEach(() => {
		mocks.ready = true;
		mocks.org = { id: "org-north", name: "Northwind" };
		mocks.orgError = undefined;
		mocks.orgRefetch.mockReset();
		mocks.calls = [];
		mocks.gates = {};
		mocks.fail = {};
		mocks.teams = [];
		mocks.myTeams = [];
		mocks.teamsError = undefined;
		mocks.candidates = [];
		mocks.impact = { people: 0, sessions: 0, projects: 0, keeps: [] };
		useUiStore.setState({ globalToasts: [] });
		mocks.details = {
			"t-platform": {
				team: team("t-platform", "Platform", { memberCount: 4, sharedSessions: 2, sharedProjects: 1 }),
				members: [
					person("m-asha", "Asha Rao", "asha@northwind.dev", { role: "owner", you: true, workspaceMember: true }),
					person("m-ben", "Ben Ortiz", "Ben@Acme.co"),
					person("m-farah", "Farah Nasser", "farah@northwind.dev", { workspaceMember: true }),
					person("m-sam", undefined, "sam@contractor.io", { invited: true }),
				],
				shares: [],
			},
			"t-guild": {
				team: team("t-guild", "Design guild", { orgId: "org-acme", orgName: "Acme", canManage: false, yourRole: "member" }),
				members: [person("g-me", "Asha Rao", "asha@northwind.dev", { teamId: "t-guild", you: true })],
				shares: [],
			},
		};
	});

	it("invites the first team when there are none", async () => {
		renderSection();
		expect(await screen.findByText("No teams yet")).toBeInTheDocument();
		expect(screen.getByText("Share sessions and projects with a group in one step.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "New team" })).toBeInTheDocument();
	});

	it("waits for both lists rather than flashing No teams yet", async () => {
		const mine = gate();
		mocks.gates.listMyTeams = [mine];
		mocks.myTeams = [team("t-guild", "Design guild", { orgId: "org-acme", orgName: "Acme", memberCount: 9, canManage: false, yourRole: "member" })];
		renderSection();
		await waitFor(() => expect(mocks.calls.some((call) => call[0] === "listTeams")).toBe(true));
		await act(async () => new Promise((resolve) => setTimeout(resolve, 20)));
		expect(screen.queryByText("No teams yet")).toBeNull();
		await act(async () => mine.open());
		expect(await screen.findByText("Design guild")).toBeInTheDocument();
		expect(screen.queryByText("No teams yet")).toBeNull();
	});

	it("asks to sign in when signed out, instead of loading forever", () => {
		mocks.ready = false;
		mocks.org = undefined;
		renderSection();
		expect(screen.getByText("Sign in to Orchestrator Cloud to use teams.")).toBeInTheDocument();
		expect(document.querySelector("[aria-busy]")).toBeNull();
	});

	it("says when the workspace couldn't load, with a way to try again", async () => {
		mocks.org = undefined;
		mocks.orgError = new Error("Couldn't reach AO Cloud.");
		renderSection();
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't reach AO Cloud.");
		await userEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(mocks.orgRefetch).toHaveBeenCalled();
	});

	it("is quiet against a control plane from before teams", async () => {
		mocks.teamsError = new CloudCpError("404 page not found", { status: 404 });
		renderSection();
		expect(await screen.findByText("Teams aren't available on this Orchestrator Cloud yet. Share links still work.")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).toBeNull();
		expect(mocks.calls.filter((call) => call[0] === "listTeams")).toHaveLength(1);
	});

	it("shows only the teams you're in elsewhere where the workspace can't share", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		mocks.myTeams = [mocks.teams[0]!, team("t-guild", "Design guild", { orgId: "org-acme", orgName: "Acme", memberCount: 9, canManage: false, yourRole: "member" })];
		renderSection({ manage: false });
		expect(await screen.findByText("Design guild")).toBeInTheDocument();
		expect(screen.queryByText("Platform")).toBeNull();
		expect(screen.queryByRole("button", { name: "New team" })).toBeNull();
		expect(mocks.calls.some((call) => call[0] === "listTeams")).toBe(false);
	});

	it("lists this workspace's teams and the ones you're in elsewhere, apart", async () => {
		mocks.teams = [team("t-platform", "Platform", { memberCount: 4, sharedSessions: 2, sharedProjects: 1 }), team("t-data", "Data infra", { yourRole: undefined })];
		mocks.myTeams = [mocks.teams[0]!, team("t-guild", "Design guild", { orgId: "org-acme", orgName: "Acme", memberCount: 9, canManage: false, yourRole: "member" })];
		renderSection();
		const platform = await screen.findByText("Platform");
		const platformRow = platform.closest("[data-team-row]") as HTMLElement;
		expect(platformRow.textContent).toContain("4 people · 2 sessions and 1 project shared");
		expect(platformRow.textContent).toContain("Owner");
		expect(screen.getByText("Data infra").closest("[data-team-row]")!.textContent).not.toContain("Owner");
		// The workspace's own team isn't repeated under the other workspaces.
		expect(screen.getAllByText("Platform")).toHaveLength(1);
		expect(screen.getByText("Teams you're in · other workspaces")).toBeInTheDocument();
		expect(screen.getByText("Acme · 9 people")).toBeInTheDocument();
	});

	it("focuses a team page's way back on opening it, and the team's row on going back", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await teamPage();
		const back = screen.getByRole("button", { name: "Teams" });
		expect(document.activeElement).toBe(back);
		await userEvent.click(back);
		const row = (await screen.findByText("Platform")).closest("[data-team-row]");
		await waitFor(() => expect(document.activeElement).toBe(row));
	});

	it("shows a team's people with roles, and refuses to demote the last owner", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const page = await teamPage();
		expect(await within(page).findByText("People · 4")).toBeInTheDocument();
		const rows = [...page.querySelectorAll("[data-team-member]")];
		expect(rows[0]!.textContent).toContain("Asha Rao");
		expect(rows[0]!.textContent).toContain("(you)");
		expect(rows[3]!.textContent).toContain("sam@contractor.io");
		expect(rows[3]!.textContent).toContain("Added by email");
		// You don't remove yourself from here; you leave.
		expect(rows[0]!.querySelector("[data-member-actions]")).toBeNull();
		await userEvent.click(within(rows[0] as HTMLElement).getByRole("button", { name: "Role for Asha Rao" }));
		expect(await screen.findByRole("menuitem", { name: /^Member/ })).toHaveAttribute("aria-disabled", "true");
	});

	it("says why a role can't be chosen", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const page = await teamPage();
		const rows = () => [...page.querySelectorAll<HTMLElement>("[data-team-member]")];
		await within(page).findByText("People · 4");
		await userEvent.click(within(rows()[0]!).getByRole("button", { name: "Role for Asha Rao" }));
		const member = await screen.findByRole("menuitem", { name: /^Member/ });
		expect(member).toHaveAttribute("aria-disabled", "true");
		expect(member.textContent).toContain("A team needs an owner. Make someone else an owner first.");
		await userEvent.keyboard("{Escape}");
		await userEvent.click(within(rows()[3]!).getByRole("button", { name: "Role for sam@contractor.io" }));
		const owner = await screen.findByRole("menuitem", { name: /^Owner/ });
		expect(owner).toHaveAttribute("aria-disabled", "true");
		expect(owner.textContent).toContain("Only people in this workspace can be owners");
	});

	it("doesn't offer the only owner a Leave that can only fail, and says why", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const page = await teamPage();
		await within(page).findByText("People · 4");
		await userEvent.click(within(page).getByRole("button", { name: "Team actions" }));
		const leave = await screen.findByRole("menuitem", { name: /^Leave team/ });
		expect(leave).toHaveAttribute("aria-disabled", "true");
		expect(leave.textContent).toContain("You're its only owner. Make someone else an owner first, or delete the team.");
		await userEvent.click(leave);
		expect(screen.queryByText("Leave Platform?")).toBeNull();
	});

	it("lets an owner leave once someone else is an owner too", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		mocks.details["t-platform"]!.members[2]!.role = "owner";
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const page = await teamPage();
		await within(page).findByText("People · 4");
		await userEvent.click(within(page).getByRole("button", { name: "Team actions" }));
		const leave = await screen.findByRole("menuitem", { name: /^Leave team/ });
		expect(leave).not.toHaveAttribute("aria-disabled");
		expect(leave.textContent).not.toContain("only owner");
	});

	it("only lets people in the workspace become owners, and shows the new role at once", async () => {
		const answer = gate();
		mocks.gates.updateTeamMember = [answer];
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await userEvent.click(await screen.findByRole("button", { name: "Role for Ben Ortiz" }));
		expect(await screen.findByRole("menuitem", { name: /^Owner/ })).toHaveAttribute("aria-disabled", "true");
		await userEvent.keyboard("{Escape}");
		await userEvent.click(screen.getByRole("button", { name: "Role for Farah Nasser" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Owner" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "updateTeamMember")).toEqual(["updateTeamMember", "org-north", "t-platform", "m-farah", "owner"]));
		// Before the answer.
		expect(screen.getByRole("button", { name: "Role for Farah Nasser" })).toHaveTextContent("Owner");
		await act(async () => answer.open());
		expect(screen.getByRole("button", { name: "Role for Farah Nasser" })).toHaveTextContent("Owner");
	});

	it("renames at once, and puts the name back with the error if it fails", async () => {
		const answer = gate();
		mocks.gates.renameTeam = [answer];
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await userEvent.click(await screen.findByRole("button", { name: "Team actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
		const field = screen.getByRole("textbox", { name: "Name" });
		await userEvent.clear(field);
		await userEvent.type(field, "Platform core{Enter}");
		expect(teamName()).toHaveTextContent("Platform core");
		await act(async () => answer.open());
		expect(teamName()).toHaveTextContent("Platform core");

		mocks.fail.renameTeam = new Error("That name is taken.");
		await userEvent.click(screen.getByRole("button", { name: "Team actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
		await userEvent.clear(screen.getByRole("textbox", { name: "Name" }));
		await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "Data{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("That name is taken.");
		expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Data");
	});

	it("keeps people picked while Add is on its way, and treats emails in the team as in it whatever their case", async () => {
		const answer = gate();
		mocks.gates.addTeamMembers = [answer];
		mocks.candidates = [{ kind: "user", name: "Ben Ortiz", email: "ben@acme.co" }];
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const input = await screen.findByRole("combobox", { name: "Add people" });
		fireEvent.change(input, { target: { value: "ben" } });
		const ben = await screen.findByRole("option", { name: /Ben Ortiz/ });
		expect(ben).toHaveAttribute("aria-disabled", "true");
		expect(ben).toHaveTextContent("In the team");
		fireEvent.change(input, { target: { value: "lee@acme.co" } });
		fireEvent.keyDown(input, { key: "Enter" });
		await userEvent.click(screen.getByRole("button", { name: "Add" }));
		fireEvent.change(input, { target: { value: "kim@acme.co" } });
		fireEvent.keyDown(input, { key: "Enter" });
		await act(async () => answer.open());
		const field = input.closest("[data-people-field]") as HTMLElement;
		await waitFor(() => expect(field.textContent).not.toContain("lee@acme.co"));
		expect(field.textContent).toContain("kim@acme.co");
		expect(await screen.findByText("People · 5")).toBeInTheDocument();
	});

	it("says what removing someone takes away and what they keep", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		mocks.impact = { people: 1, sessions: 2, projects: 1, keeps: ["storefront"] };
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await userEvent.click(await screen.findByRole("button", { name: "Actions for Ben Ortiz" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Remove from team" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText("Remove Ben Ortiz from Platform?")).toBeInTheDocument();
		expect(await within(dialog).findByText("Ben Ortiz loses 2 sessions and 1 project shared with Platform. Ben Ortiz keeps storefront, shared with them another way.")).toBeInTheDocument();
		await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "removeTeamMember")).toEqual(["removeTeamMember", "org-north", "t-platform", "m-ben"]));
		await waitFor(() => expect(screen.queryByText("Ben Ortiz")).toBeNull());
	});

	it("warns how many people lose access before deleting, then goes back to a list without the team", async () => {
		mocks.teams = [team("t-platform", "Platform"), team("t-data", "Data infra")];
		mocks.impact = { people: 3, sessions: 4, projects: 0, keeps: [] };
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await userEvent.click(await screen.findByRole("button", { name: "Team actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Delete team" }));
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByText("3 people lose what was shared with Platform: 4 sessions. Anything shared with them another way stays. This can't be undone.")).toBeInTheDocument();
		// The lists aren't read again, so what shows is what the cache holds.
		mocks.gates.listTeams = [gate()];
		mocks.gates.listMyTeams = [gate()];
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete team" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "deleteTeam")).toEqual(["deleteTeam", "org-north", "t-platform"]));
		expect(await screen.findByText("Data infra")).toBeInTheDocument();
		expect(screen.queryByText("Platform")).toBeNull();
	});

	it("pins the New team dialog's close button to its corner, and it closes the dialog", async () => {
		renderSection();
		await userEvent.click(await screen.findByRole("button", { name: "New team" }));
		const dialog = document.querySelector<HTMLElement>("[data-new-team-dialog]")!;
		const close = within(dialog).getByRole("button", { name: "Close dialog" });
		// The settings close-button utilities it used are gone from the stylesheet.
		expect(close).toHaveClass("absolute", "top-3", "right-3");
		await userEvent.click(close);
		await waitFor(() => expect(document.querySelector("[data-new-team-dialog]")).toBeNull());
	});

	it("opens a new team's page at once from what creating it returned", async () => {
		renderSection();
		await userEvent.click(await screen.findByRole("button", { name: "New team" }));
		await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "Release");
		// Its own read never answers: the page shows what creating it returned.
		mocks.gates.getTeam = [gate()];
		await userEvent.click(screen.getByRole("button", { name: "Create team" }));
		const page = await teamPage();
		expect(page.getAttribute("data-team-page")).toBe("t-new");
		expect(teamName()).toHaveTextContent("Release");
		expect(page.querySelector("[aria-busy]")).toBeNull();
	});

	it("shows why a change to a share failed, offers Undo in Settings when one stops, and says if Undo fails", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		mocks.details["t-platform"]!.shares = [share("g-fix", { deniedCommands: ["git push"] })];
		mocks.fail.updateShareEntry = new Error("Couldn't change access.");
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const row = await shareRow("Fix checkout");
		await userEvent.click(within(row).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can watch/ }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't change access.");
		// Back to what it was.
		expect(within(await shareRow("Fix checkout")).getByRole("button", { name: /Change access/ })).toHaveTextContent("Can write");

		await userEvent.click(within(await shareRow("Fix checkout")).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Stop sharing with Platform" }));
		const notice = await screen.findByRole("status");
		expect(notice).toHaveTextContent("Stopped sharing Fix checkout with Platform");
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
		expect(screen.queryByText("Fix checkout")).toBeNull();
		await userEvent.click(within(notice).getByRole("button", { name: "Undo" }));
		expect(screen.getByText("Fix checkout")).toBeInTheDocument();
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "createShareGrants")).toEqual([
			"createShareGrants", "org-north", "p-store", { sessionId: "s-fix", subjects: [{ teamId: "t-platform" }], role: "editor", modeCap: "trusted", deniedCommands: ["git push"] },
		]));

		mocks.fail.revokeShareGrant = new Error("Couldn't stop sharing.");
		await userEvent.click(within(await shareRow("Fix checkout")).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Stop sharing with Platform" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't stop sharing.");
		expect(await screen.findByText("Fix checkout")).toBeInTheDocument();

		mocks.fail.revokeShareGrant = undefined;
		mocks.fail.createShareGrants = new Error("offline");
		await userEvent.click(within(await shareRow("Fix checkout")).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Stop sharing with Platform" }));
		await userEvent.click(within(await screen.findByRole("status")).getByRole("button", { name: "Undo" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't restore access. Check your connection and share again.");
		await waitFor(() => expect(screen.queryByText("Fix checkout")).toBeNull());
	});

	it("opens a team from another workspace read-only, with Leave, and drops it from the list", async () => {
		mocks.myTeams = [team("t-guild", "Design guild", { orgId: "org-acme", orgName: "Acme", canManage: false, yourRole: "member" }), team("t-ops", "Ops", { orgId: "org-acme", orgName: "Acme", canManage: false, yourRole: "member" })];
		mocks.impact = { people: 1, sessions: 1, projects: 0, keeps: [] };
		renderSection();
		await userEvent.click(await screen.findByText("Design guild"));
		expect(await screen.findByText("Acme workspace")).toBeInTheDocument();
		expect(mocks.calls).toContainEqual(["getMyTeam", "t-guild"]);
		expect(screen.queryByRole("button", { name: "Team actions" })).toBeNull();
		expect(screen.queryByRole("combobox", { name: "Add people" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Leave team" }));
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByText("You lose 1 session shared with Design guild. An owner can add you back.")).toBeInTheDocument();
		mocks.gates.listTeams = [gate()];
		mocks.gates.listMyTeams = [gate()];
		await userEvent.click(within(dialog).getByRole("button", { name: "Leave team" }));
		await waitFor(() => expect(mocks.calls).toContainEqual(["leaveTeam", "t-guild"]));
		expect(await screen.findByText("Ops")).toBeInTheDocument();
		expect(screen.queryByText("Design guild")).toBeNull();
	});

	it("words what's lost in the person's language", () => {
		const t = appI18n.t.bind(appI18n);
		expect(lostText(t, { sessions: 1, projects: 0 }, "en")).toBe("1 session");
		expect(lostText(t, { sessions: 0, projects: 2 }, "en")).toBe("2 projects");
		expect(lostText(t, { sessions: 0, projects: 0 }, "en")).toBe("");
	});
});
