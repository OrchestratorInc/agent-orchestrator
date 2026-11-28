import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError, type CloudCpShareAccess } from "../lib/cloud-cp";
import { workspaceDisplayName } from "./share/linkAccess";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TooltipProvider } from "./ui/tooltip";

// A team workspace provisioned for one person can be named after their
// email, so "People in asha.k77@example.com" read as if an address were a
// group, and never said the workspace's people have full access.
const EMAIL = "asha.k77@example.com";

const mocks = vi.hoisted(() => ({
	org: undefined as Record<string, unknown> | undefined,
	members: 1,
	membersError: undefined as Error | undefined,
	access: { entries: [] } as CloudCpShareAccess,
	calls: [] as string[],
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "u-me",
		client: {
			getShareAccess: async () => structuredClone(mocks.access),
			getTeamShareAccess: async () => ({ members: [] }),
			listShareCandidates: async () => ({ candidates: [] }),
			listShareLinks: async () => ({ links: [] }),
			listOrgMembers: async (orgId: string) => {
				mocks.calls.push(`listOrgMembers:${orgId}`);
				if (mocks.membersError) throw mocks.membersError;
				return {
					members: Array.from({ length: mocks.members }, (_, index) => ({
						userId: `u-${index}`, email: index === 0 ? "asha.k77@example.com" : `member${index}@acme.dev`, displayName: "", role: index === 0 ? "admin" : "member", createdAt: "2026-11-01T00:00:00Z",
					})),
				};
			},
		},
	}),
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: mocks.org }) }));
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ session: { user: { id: "u-0", email: "asha.k77@example.com", displayName: "Asha Rao" } } }),
}));

function renderDialog(props: { sessionId?: string; standaloneAgent?: boolean } = { sessionId: "sess" }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ShareSessionDialog open onOpenChange={() => undefined} orgId="org" projectId="proj" projectName="storefront" {...props} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

const accessList = () => screen.findByText((_text, element) => element?.hasAttribute("data-share-access-list") ?? false);
const workspaceRow = () => document.querySelector<HTMLElement>("[data-share-workspace-row]");
async function sentence(): Promise<string> {
	const region = await screen.findByRole("region", { name: "General access" });
	await waitFor(() => expect(region.querySelector("[data-general-access-sentence]")).not.toBeNull());
	return region.querySelector("[data-general-access-sentence]")?.textContent ?? "";
}

describe("the workspace in People with access", () => {
	// Radix's focus scope moves focus from a 0 ms timer when a dialog unmounts.
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
	beforeEach(() => {
		mocks.org = { id: "org", displayName: "Acme", kind: "team" };
		mocks.members = 4;
		mocks.membersError = undefined;
		mocks.access = { owner: { kind: "user", userId: "u-0", name: "Asha Rao", email: EMAIL, workspaceMember: true }, entries: [] };
		mocks.calls = [];
	});

	it.each([
		["a plain name", "Acme", "Acme"],
		["an email", EMAIL, undefined],
		["an email in a name", `${EMAIL}'s organization`, undefined],
		["nothing", "  ", undefined],
		["unknown", undefined, undefined],
		["a person's name", "Asha's organization", "Asha's organization"],
	])("reads a workspace named %s as a name only when it is one", (_case, name, shown) => {
		expect(workspaceDisplayName(name)).toBe(shown);
	});

	it("says everyone in your workspace has full access, with its name, how many they are and what that means", async () => {
		renderDialog();
		const list = await accessList();
		await waitFor(() => expect(within(list).getByText("Acme · 4 members")).toBeInTheDocument());
		const row = workspaceRow()!;
		expect(within(row).getByText("Everyone in your workspace")).toBeInTheDocument();
		const access = within(row).getByText("Full access");
		await userEvent.hover(access);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Everyone in your workspace can open and work in every session in this project. Workspace admins manage who is a member.");
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session.");
		expect(mocks.calls).toEqual(["listOrgMembers:org"]);
	});

	it("never shows a workspace named after an email as its name, in the row or in General access", async () => {
		mocks.org = { id: "org", displayName: EMAIL, kind: "team" };
		mocks.members = 3;
		renderDialog();
		const list = await accessList();
		await waitFor(() => expect(within(workspaceRow()!).getByText("3 members")).toBeInTheDocument());
		expect(within(workspaceRow()!).getByText("Everyone in your workspace")).toBeInTheDocument();
		expect(workspaceRow()!.textContent).not.toContain("@");
		// The owner row is the only place the address shows, as a person's.
		expect(within(list).getAllByText(EMAIL)).toHaveLength(1);
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session.");
	});

	it("leaves the workspace out when nobody but you is in it: it grants nothing", async () => {
		mocks.org = { id: "org", displayName: EMAIL, kind: "team" };
		mocks.members = 1;
		renderDialog();
		const list = await accessList();
		expect(within(list).getByText("Owner")).toBeInTheDocument();
		expect(workspaceRow()).toBeNull();
		expect(list.textContent).not.toContain("Everyone in your workspace");
		expect(await sentence()).toBe("Only you and people added above can open this session.");
	});

	it("on the project's sharing, lists you instead of a workspace with nobody else in it", async () => {
		mocks.org = { id: "org", displayName: EMAIL, kind: "team" };
		mocks.members = 1;
		mocks.access = { entries: [] };
		renderDialog({ sessionId: undefined });
		const list = await accessList();
		expect(workspaceRow()).toBeNull();
		const you = list.querySelector<HTMLElement>("[data-share-you-row]")!;
		expect(you).toHaveTextContent("Asha Rao (you)");
		expect(you).toHaveTextContent(EMAIL);
		expect(you).toHaveTextContent("Full access");
		expect(await sentence()).toBe("Only you and people added above can open this project.");
	});

	it("on the project's sharing, names the workspace and its members on top", async () => {
		mocks.access = { entries: [] };
		renderDialog({ sessionId: undefined });
		const list = await accessList();
		await waitFor(() => expect(within(list).getByText("Acme · 4 members")).toBeInTheDocument());
		expect(list.firstElementChild).toBe(workspaceRow());
		expect(list.querySelector("[data-share-you-row]")).toBeNull();
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this project.");
	});

	it("still shows the workspace, by name only, when its members can't be read", async () => {
		mocks.membersError = new CloudCpError("You do not have access to this organization.", { status: 403, code: "forbidden" });
		renderDialog();
		const list = await accessList();
		await waitFor(() => expect(within(list).getByText("Acme")).toBeInTheDocument());
		expect(within(workspaceRow()!).getByText("Everyone in your workspace")).toBeInTheDocument();
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session.");
	});

	it("shows only the title when the workspace has no name to show and its members can't be read", async () => {
		mocks.org = { id: "org", displayName: EMAIL, kind: "team" };
		mocks.membersError = new CloudCpError("Not found", { status: 404, code: "not_found" });
		renderDialog();
		await accessList();
		await waitFor(() => expect(workspaceRow()).not.toBeNull());
		expect(workspaceRow()!.querySelector("[data-share-workspace-detail]")).toBeNull();
		expect(workspaceRow()!.textContent).not.toContain("@");
	});

	it("doesn't count a personal workspace's members: it has nobody else", async () => {
		mocks.org = { id: "org", displayName: "Asha Rao's organization", kind: "personal" };
		renderDialog();
		await accessList();
		expect(await sentence()).toBe("Only you and people added above can open this session.");
		expect(workspaceRow()).toBeNull();
		expect(mocks.calls).toEqual([]);
	});
});
