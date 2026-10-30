import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpShareAccess, CloudCpShareCandidate, CloudCpTeamMemberAccess } from "../lib/cloud-cp";
import { useUiStore } from "../stores/ui-store";
import { ShareSessionDialog } from "./ShareSessionDialog";

const mocks = vi.hoisted(() => ({
	access: undefined as unknown as CloudCpShareAccess,
	teamMembers: {} as Record<string, CloudCpTeamMemberAccess[]>,
	candidates: [] as CloudCpShareCandidate[],
	calls: [] as Array<[string, ...unknown[]]>,
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "user_workos",
		client: {
			getShareAccess: async (_org: string, _project: string, sessionId?: string) => {
				mocks.calls.push(["getShareAccess", sessionId]);
				return mocks.access;
			},
			getTeamShareAccess: async (_org: string, _project: string, _session: string | undefined, teamId: string) => ({ members: mocks.teamMembers[teamId] ?? [] }),
			listShareCandidates: async () => ({ candidates: mocks.candidates }),
			listShareLinks: async () => ({ links: [] }),
			createShareGrants: async (...args: unknown[]) => {
				mocks.calls.push(["createShareGrants", ...args]);
				return { entries: [] };
			},
			updateShareEntry: async (...args: unknown[]) => {
				mocks.calls.push(["updateShareEntry", ...args]);
				return { entry: mocks.access.entries[0] };
			},
			revokeShareGrant: async (...args: unknown[]) => {
				mocks.calls.push(["revokeShareGrant", ...args]);
			},
		},
	}),
}));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: { user: { email: "asha@northwind.dev" } } }) }));

const member = (id: string, name: string, access: CloudCpTeamMemberAccess["access"], extra: Partial<CloudCpTeamMemberAccess["member"]> = {}): CloudCpTeamMemberAccess => ({
	member: { id, teamId: "t-platform", name, email: `${name.toLowerCase().split(" ")[0]}@northwind.dev`, role: "member", invited: false, you: false, workspaceMember: false, addedAt: "2026-10-30T00:00:00Z", ...extra },
	access,
});

function renderDialog() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ShareSessionDialog open onOpenChange={() => undefined} orgId="org" projectId="proj" sessionId="sess" />
		</QueryClientProvider>,
	);
}

describe("ShareSessionDialog people and teams", () => {
	beforeEach(() => {
		mocks.calls = [];
		useUiStore.setState({ globalToasts: [] });
		mocks.access = {
			owner: { kind: "user", userId: "u-asha", name: "Asha Rao", email: "asha@northwind.dev", workspaceMember: true },
			entries: [
				{ grantId: "g-platform", subject: { kind: "team", teamId: "t-platform", teamName: "Platform", memberCount: 6 }, scope: "session", sessionId: "sess", projectId: "proj", projectName: "storefront", role: "editor", modeCap: "standard", deniedCommands: [], source: "team", createdAt: "2026-10-30T00:00:00Z" },
				{ grantId: "g-riya", subject: { kind: "user", userId: "u-riya", name: "Riya Sen", email: "riya@acme.co" }, scope: "session", sessionId: "sess", projectId: "proj", projectName: "storefront", role: "viewer", modeCap: "read-only", deniedCommands: [], source: "link", createdAt: "2026-10-30T00:00:00Z" },
				{ grantId: "g-data", subject: { kind: "team", teamId: "t-data", teamName: "Data infra", memberCount: 4 }, scope: "project", projectId: "proj", projectName: "storefront", role: "viewer", modeCap: "read-only", deniedCommands: [], source: "team", createdAt: "2026-10-30T00:00:00Z" },
				{ grantId: "g-sam", subject: { kind: "email", email: "sam@contractor.io" }, scope: "session", sessionId: "sess", projectId: "proj", projectName: "storefront", role: "viewer", modeCap: "read-only", deniedCommands: [], source: "direct", createdAt: "2026-10-30T00:00:00Z" },
			],
		};
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

	it("lists the owner, teams, people and pending emails, and the project's shares as such", async () => {
		renderDialog();
		const list = await screen.findByRole("list");
		await within(list).findByText("Platform");
		expect(within(list).getByText(/Asha Rao/).textContent).toContain("(you)");
		expect(within(list).getByText("Owner")).toBeInTheDocument();
		const rows = [...list.querySelectorAll("[data-share-entry]")];
		expect(rows.map((row) => row.getAttribute("data-share-entry"))).toEqual(["team", "user", "team", "email"]);
		expect(rows[0]!.textContent).toContain("6 people");
		expect(rows[0]!.textContent).toContain("Write to the agent");
		expect(rows[1]!.textContent).toContain("from a link");
		// The project's share applies here but is changed at project level.
		expect(rows[2]!.textContent).toContain("from the whole project");
		expect(rows[2]!.querySelector("[data-access-menu]")).toBeNull();
		expect(rows[3]!.textContent).toContain("Added by email · gets access when they sign in");
	});

	it("expands a team to show what each member ends up with and why", async () => {
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Show the people in Platform" }));
		const members = await screen.findAllByText((_text, element) => element?.hasAttribute("data-share-member") ?? false);
		const lines = members.map((row) => row.textContent);
		expect(lines[0]).toContain("Ben Ortiz");
		expect(lines[0]).toContain("Write to the agent");
		expect(lines[1]).toContain("Trusted · shared directly");
		expect(lines[2]).toContain("Full access · workspace member");
		expect(lines[3]).toContain("after signing in");
		expect(screen.getByText("People with access more than one way get the highest.")).toBeInTheDocument();
	});

	it("confirms a team share by naming who gets what, then shares at the chosen level", async () => {
		renderDialog();
		await screen.findByText("Platform");
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		fireEvent.change(input, { target: { value: "check" } });
		fireEvent.click(await screen.findByText("Checkout"));
		// Default level: watch.
		const summary = await screen.findByText(/Dana Kim and Omar Haddad get Watch\./);
		expect(summary.textContent).toContain("Ben Ortiz already has this or more.");
		expect(summary.textContent).toContain("Farah Nasser already has full access as a workspace member.");
		expect(summary.textContent).toContain("sam@contractor.io gets Watch after signing in.");
		expect(summary.textContent).not.toContain("Asha");
		// Raise to writing; the limit appears.
		const field = input.closest("[data-people-field]") as HTMLElement;
		await userEvent.click(within(field).getByRole("button", { name: /Change access: Watch/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Write to the agent/ }));
		// Ben already writes through Platform at the same limit, so he still keeps what he has.
		const raised = await screen.findByText(/Dana Kim and Omar Haddad get Write to the agent\./);
		expect(raised.textContent).toContain("Ben Ortiz already has this or more.");
		expect(screen.getByRole("button", { name: "Most they can do" })).toHaveTextContent("Standard");
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "createShareGrants")).toBeTruthy());
		expect(mocks.calls.find((call) => call[0] === "createShareGrants")).toEqual([
			"createShareGrants", "org", "proj", { sessionId: "sess", subjects: [{ teamId: "t-checkout" }], role: "editor", modeCap: "standard" },
		]);
	});

	it("changes a row's access in place and stops sharing with Undo", async () => {
		renderDialog();
		const list = await screen.findByRole("list");
		await within(list).findByText("Platform");
		const platformRow = list.querySelector("[data-share-entry=team]") as HTMLElement;
		await userEvent.click(within(platformRow).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Watch/ }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "updateShareEntry")).toEqual(["updateShareEntry", "org", "proj", "g-platform", { role: "viewer", modeCap: "read-only" }]));

		await userEvent.click(within(platformRow).getByRole("button", { name: /Change access/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Stop sharing with Platform" }));
		await waitFor(() => expect(mocks.calls.find((call) => call[0] === "revokeShareGrant")).toEqual(["revokeShareGrant", "org", "proj", "g-platform"]));
		await waitFor(() => expect(useUiStore.getState().globalToasts.at(-1)?.title).toBe("Stopped sharing with Platform"));
		const toast = useUiStore.getState().globalToasts.at(-1)!;
		expect(toast.action?.label).toBe("Undo");
		await act(async () => toast.action!.run());
		await waitFor(() => expect(mocks.calls.filter((call) => call[0] === "createShareGrants").at(-1)).toEqual([
			"createShareGrants", "org", "proj", { sessionId: "sess", subjects: [{ teamId: "t-platform" }], role: "editor", modeCap: "standard" },
		]));
	});

	it("shares the whole project without naming a session", async () => {
		renderDialog();
		await screen.findByText("Platform");
		await userEvent.click(screen.getByRole("radio", { name: "The whole project" }));
		expect(await screen.findByText("Share project")).toBeInTheDocument();
		await waitFor(() => expect(mocks.calls.some((call) => call[0] === "getShareAccess" && call[1] === undefined)).toBe(true));
	});
});
