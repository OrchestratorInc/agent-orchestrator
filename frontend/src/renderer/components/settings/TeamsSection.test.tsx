import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import type { CloudCpTeam, CloudCpTeamDetail, CloudCpTeamImpact, CloudCpTeamMember } from "../../lib/cloud-cp";
import { lostText, TeamsSection } from "./TeamsSection";

const mocks = vi.hoisted(() => ({
	teams: [] as CloudCpTeam[],
	myTeams: [] as CloudCpTeam[],
	details: {} as Record<string, CloudCpTeamDetail>,
	impact: { people: 0, sessions: 0, projects: 0, keeps: [] } as CloudCpTeamImpact,
	calls: [] as Array<[string, ...unknown[]]>,
}));

vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-north", name: "Northwind" } }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "me",
		client: {
			listTeams: async () => ({ teams: mocks.teams }),
			listMyTeams: async () => ({ teams: mocks.myTeams }),
			getTeam: async (_org: string, teamId: string) => mocks.details[teamId],
			getMyTeam: async (teamId: string) => {
				mocks.calls.push(["getMyTeam", teamId]);
				return mocks.details[teamId];
			},
			teamImpact: async (...args: unknown[]) => {
				mocks.calls.push(["teamImpact", ...args]);
				return mocks.impact;
			},
			teamMemberImpact: async (...args: unknown[]) => {
				mocks.calls.push(["teamMemberImpact", ...args]);
				return mocks.impact;
			},
			leaveTeamImpact: async (...args: unknown[]) => {
				mocks.calls.push(["leaveTeamImpact", ...args]);
				return mocks.impact;
			},
			deleteTeam: async (...args: unknown[]) => {
				mocks.calls.push(["deleteTeam", ...args]);
			},
			removeTeamMember: async (...args: unknown[]) => {
				mocks.calls.push(["removeTeamMember", ...args]);
			},
			leaveTeam: async (...args: unknown[]) => {
				mocks.calls.push(["leaveTeam", ...args]);
			},
			updateTeamMember: async (...args: unknown[]) => {
				mocks.calls.push(["updateTeamMember", ...args]);
			},
			listShareCandidates: async () => ({ candidates: [] }),
		},
	}),
}));

const team = (id: string, name: string, extra: Partial<CloudCpTeam> = {}): CloudCpTeam => ({
	id, orgId: "org-north", name, memberCount: 3, canManage: true, yourRole: "owner", sharedSessions: 0, sharedProjects: 0, createdAt: "2026-10-30T00:00:00Z", updatedAt: "2026-10-30T00:00:00Z", ...extra,
});
const person = (id: string, name: string | undefined, email: string, extra: Partial<CloudCpTeamMember> = {}): CloudCpTeamMember => ({
	id, teamId: "t-platform", name, email, role: "member", invited: false, you: false, workspaceMember: false, addedAt: "2026-10-30T00:00:00Z", ...extra,
});

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><TeamsSection titleHidden={false} /></QueryClientProvider>);
}

describe("TeamsSection", () => {
	beforeEach(() => {
		mocks.calls = [];
		mocks.teams = [];
		mocks.myTeams = [];
		mocks.impact = { people: 0, sessions: 0, projects: 0, keeps: [] };
		mocks.details = {
			"t-platform": {
				team: team("t-platform", "Platform", { memberCount: 4, sharedSessions: 2, sharedProjects: 1 }),
				members: [
					person("m-asha", "Asha Rao", "asha@northwind.dev", { role: "owner", you: true, workspaceMember: true }),
					person("m-ben", "Ben Ortiz", "ben@acme.co"),
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

	it("shows a team's people with roles, and refuses to demote the last owner", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		const page = await screen.findByText((_text, element) => element?.hasAttribute("data-team-page") ?? false);
		expect(within(page).getByText("People · 4")).toBeInTheDocument();
		const rows = [...page.querySelectorAll("[data-team-member]")];
		expect(rows[0]!.textContent).toContain("Asha Rao");
		expect(rows[0]!.textContent).toContain("(you)");
		expect(rows[3]!.textContent).toContain("sam@contractor.io");
		expect(rows[3]!.textContent).toContain("Added by email");
		// You don't remove yourself from here; you leave.
		expect(rows[0]!.querySelector("[data-member-actions]")).toBeNull();
		await userEvent.click(within(rows[0] as HTMLElement).getByRole("button", { name: "Role for Asha Rao" }));
		expect(await screen.findByRole("menuitem", { name: "Member" })).toHaveAttribute("aria-disabled", "true");
	});

	it("only lets people in the workspace become owners", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await userEvent.click(await screen.findByRole("button", { name: "Role for Ben Ortiz" }));
		expect(await screen.findByRole("menuitem", { name: "Owner" })).toHaveAttribute("aria-disabled", "true");
		await userEvent.keyboard("{Escape}");
		await userEvent.click(screen.getByRole("button", { name: "Role for Farah Nasser" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Owner" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "updateTeamMember")).toEqual(["updateTeamMember", "org-north", "t-platform", "m-farah", "owner"]));
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
	});

	it("warns how many people lose access before deleting, then goes back to the list", async () => {
		mocks.teams = [team("t-platform", "Platform")];
		mocks.impact = { people: 3, sessions: 4, projects: 0, keeps: [] };
		renderSection();
		await userEvent.click(await screen.findByText("Platform"));
		await userEvent.click(await screen.findByRole("button", { name: "Team actions" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Delete team" }));
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByText("3 people lose what was shared with Platform: 4 sessions. Anything shared with them another way stays. This can't be undone.")).toBeInTheDocument();
		mocks.teams = [];
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete team" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "deleteTeam")).toEqual(["deleteTeam", "org-north", "t-platform"]));
		expect(await screen.findByText("No teams yet")).toBeInTheDocument();
	});

	it("opens a team from another workspace read-only, with Leave", async () => {
		mocks.myTeams = [team("t-guild", "Design guild", { orgId: "org-acme", orgName: "Acme", canManage: false, yourRole: "member" })];
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
		await userEvent.click(within(dialog).getByRole("button", { name: "Leave team" }));
		await waitFor(() => expect(mocks.calls).toContainEqual(["leaveTeam", "t-guild"]));
	});

	it("words what's lost in the person's language", () => {
		const t = appI18n.t.bind(appI18n);
		expect(lostText(t, { sessions: 1, projects: 0 }, "en")).toBe("1 session");
		expect(lostText(t, { sessions: 0, projects: 2 }, "en")).toBe("2 projects");
		expect(lostText(t, { sessions: 0, projects: 0 }, "en")).toBe("");
	});
});
