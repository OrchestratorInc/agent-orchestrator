import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudShareFile, CloudShareGrants, CloudShareInspection, CloudShareLink, CloudShareLinks, CloudShareSubjectGrant } from "../../shared/cloud-share";
import { aoBridge } from "../lib/bridge";
import type { CloudCpShareCandidate } from "../lib/cloud-cp";
import { ShareArtifactButton, resetShareCachesForTests } from "./ShareArtifactPopover";
import { TooltipProvider } from "./ui/tooltip";

const ORG_ID = "0c6e6d0a-7a3b-4c39-9a51-5d0f3c1d2e41";
const TEAM_ID = "1d7f7e1b-8b4c-4d4a-8b62-6e1a4d2e3f52";
const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";

const mocks = vi.hoisted(() => ({
	teams: true,
	org: undefined as { id: string; displayName: string; kind?: string } | undefined,
	candidates: [] as CloudCpShareCandidate[],
	createTeam: vi.fn(),
}));

vi.mock("../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/telemetry")>(),
	captureRendererEvent: vi.fn(async () => true),
}));
vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.test" } }) }));
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ status: "authenticated", session: { user: { id: "u-1", email: "aditi@acme.co", displayName: "Aditi Chauhan" } } }),
}));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { openGlobalSettings: () => void }) => unknown) => select({ openGlobalSettings: () => undefined }),
}));
vi.mock("../hooks/useTeamsEnabled", () => ({ useTeamsEnabled: () => mocks.teams }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: mocks.org, isLoading: false, error: null, ready: true, refetch: async () => ({}) }) }));
vi.mock("../hooks/useCloudCp", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useCloudCp")>(),
	useCloudCp: () => ({ client: { createTeam: mocks.createTeam }, baseUrl: "https://cp.test", ready: true, userId: "u-1" }),
}));
vi.mock("../hooks/useTeams", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useTeams")>(),
	useShareCandidates: () => ({ data: mocks.candidates, isPlaceholderData: false }),
}));

const cloudFile: Omit<CloudShareFile, "baseUrl" | "shareId"> = { source: "cloud", orgId: ORG_ID, sessionId: "cloud-1", path: "docs/report.html", projectId: "" };
const localFile = { sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
const inspection: CloudShareInspection = { name: "report.html", size: 2048, unsupported: false, fingerprint: "fp-1" };
const links: CloudShareLinks = { shares: [], limit: 3, used: 1, unlimited: true, privateLinks: true };
const share: CloudShareLink = {
	id: SHARE_ID, slug: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://viewer.test/artifact/abc",
	contentType: "text/html", size: 2048, sha256: "", createdAt: "", updatedAt: "", access: "restricted", grantCount: 0,
};
const design: CloudShareSubjectGrant = { id: "2e8f8f2c-9c5d-4e5b-9c73-7f2b5e3f4a63", orgId: ORG_ID, teamId: TEAM_ID, name: "Design", memberCount: 3, createdAt: "" };
const everyone: CloudShareSubjectGrant = { id: "3f9a9a3d-ad6e-4f6c-ad84-8a3c6f4a5b74", orgId: ORG_ID, name: "Acme", memberCount: 12, createdAt: "" };
const none: CloudShareGrants = { people: [], teams: [], workspaces: [] };

function renderButton(file: Omit<CloudShareFile, "baseUrl" | "shareId">) {
	render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={file} name="report.html" /></TooltipProvider></QueryClientProvider>);
}

async function pickDesign() {
	await userEvent.type(screen.getByRole("combobox", { name: "Add people or teams" }), "Des");
	await userEvent.click(await screen.findByRole("option", { name: /Design/ }));
}

beforeEach(() => {
	vi.restoreAllMocks();
	resetShareCachesForTests();
	mocks.teams = true;
	mocks.org = { id: ORG_ID, displayName: "Acme", kind: "team" };
	mocks.candidates = [{ kind: "team", teamId: TEAM_ID, name: "Design", memberCount: 3 }];
	mocks.createTeam.mockReset();
});

describe("ShareArtifactButton on a team workspace", () => {
	it("shares the live link with a team, lists it, and removes it", async () => {
		vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue({ ...inspection, shareId: SHARE_ID });
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue(none);
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ ...none, teams: [design] });
		const remove = vi.spyOn(aoBridge.cloudShare, "removeGrant").mockResolvedValue();
		renderButton(cloudFile);
		await userEvent.click(await screen.findByRole("button", { name: "Published: report.html" }));
		await screen.findByRole("combobox", { name: "Add people or teams" });
		// Picking the team shares at once: there is no Add button.
		await pickDesign();
		expect(screen.queryByRole("button", { name: "Add" })).toBeNull();
		expect(invite).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, { orgId: ORG_ID, teamIds: [TEAM_ID] });
		const people = await screen.findByRole("region", { name: "People with access" });
		expect(within(people).getByText("Design")).toBeTruthy();
		expect(within(people).getByText("3 members")).toBeTruthy();
		await userEvent.click(within(people).getByRole("button", { name: "Remove access for Design" }));
		expect(remove).toHaveBeenCalledWith("https://cp.test", SHARE_ID, design.id);
		await waitFor(() => expect(within(people).queryByText("Design")).toBeNull());
	});

	it("offers Everyone at the workspace in General access, and stops it with another choice", async () => {
		vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue({ ...inspection, shareId: SHARE_ID });
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue(none);
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ ...none, workspaces: [everyone] });
		const remove = vi.spyOn(aoBridge.cloudShare, "removeGrant").mockResolvedValue();
		const setAccess = vi.spyOn(aoBridge.cloudShare, "setAccess");
		renderButton(cloudFile);
		await userEvent.click(await screen.findByRole("button", { name: "Published: report.html" }));
		await userEvent.click(await screen.findByRole("button", { name: /Only you/ }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: /Everyone at Acme/ }));
		expect(invite).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, { orgId: ORG_ID, workspace: true });
		expect(await screen.findByText("Everyone at Acme can open it after signing in, and finds it under Shared with you.")).toBeTruthy();
		// Already private: no access change.
		expect(setAccess).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: /Everyone at Acme/ }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: /Only people invited/ }));
		expect(remove).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, everyone.id);
		expect(await screen.findByText("Only you")).toBeTruthy();
		expect(setAccess).not.toHaveBeenCalled();
	});

	it("shares a new link with the teams and workspace set up before Publish, once it is live", async () => {
		vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue(inspection);
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue(links);
		vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue(none);
		const publish = vi.spyOn(aoBridge.cloudShare, "publish").mockResolvedValue({ status: "published", replaced: false, share });
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ ...none, teams: [design], workspaces: [everyone] });
		renderButton(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Publish: report.html" }));
		await screen.findByRole("combobox", { name: "Add people or teams" });
		await pickDesign();
		expect(await screen.findByText("Gets access when you publish")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: /Only people invited/ }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: /Everyone at Acme/ }));
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		// Publishing sends people only, privately; the teams and workspace follow.
		expect(publish).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ access: "restricted" }));
		expect(publish.mock.calls[0]![0]).not.toHaveProperty("teamIds");
		await waitFor(() => expect(invite).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, { orgId: ORG_ID, teamIds: [TEAM_ID], workspace: true }));
		expect(await screen.findByText("3 members")).toBeTruthy();
	});

	it("shares with a typed email on Enter", async () => {
		mocks.candidates = [];
		vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue({ ...inspection, shareId: SHARE_ID });
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue(none);
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ ...none, people: [{ id: "4a0b0b4e-be7f-4a7d-be95-9b4d7a5b6c85", email: "maya@acme.co", createdAt: "" }] });
		renderButton(cloudFile);
		await userEvent.click(await screen.findByRole("button", { name: "Published: report.html" }));
		await userEvent.type(await screen.findByRole("combobox", { name: "Add people or teams" }), "maya@acme.co{Enter}");
		expect(invite).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, { emails: ["maya@acme.co"] });
		expect(await screen.findByText("maya@acme.co")).toBeTruthy();
	});

	it("makes a new team from the field, as session sharing does, and shares the link with it", async () => {
		mocks.candidates = [];
		vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue({ ...inspection, shareId: SHARE_ID });
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue(none);
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ ...none, teams: [{ ...design, name: "Research" }] });
		mocks.createTeam.mockResolvedValue({ team: { id: TEAM_ID, name: "Research", memberCount: 1 } });
		renderButton(cloudFile);
		await userEvent.click(await screen.findByRole("button", { name: "Published: report.html" }));
		const field = await screen.findByRole("combobox", { name: "Add people or teams" });
		await userEvent.type(field, "Research");
		await userEvent.click(await screen.findByRole("option", { name: /New team/ }));
		const dialog = await screen.findByRole("dialog", { name: "New team" });
		expect((within(dialog).getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Research");
		// The popover stays open behind the dialog.
		expect(screen.getByRole("combobox", { name: "Add people or teams", hidden: true })).toBeTruthy();
		await userEvent.click(within(dialog).getByRole("button", { name: "Create team" }));
		expect(mocks.createTeam).toHaveBeenCalledWith(ORG_ID, { name: "Research", members: [] });
		await waitFor(() => expect(invite).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, { orgId: ORG_ID, teamIds: [TEAM_ID] }));
		expect(await screen.findByText("Research")).toBeTruthy();
	});

	it("offers only email and the link's access on a personal workspace", async () => {
		mocks.org = { id: ORG_ID, displayName: "Aditi's workspace", kind: "personal" };
		vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue({ ...inspection, shareId: SHARE_ID });
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue(none);
		renderButton(cloudFile);
		await userEvent.click(await screen.findByRole("button", { name: "Published: report.html" }));
		expect(await screen.findByRole("textbox", { name: "Add people by email" })).toBeTruthy();
		expect(screen.queryByRole("combobox", { name: "Add people or teams" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: /Only you/ }));
		expect(screen.queryByRole("menuitemradio", { name: /Everyone at/ })).toBeNull();
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(2);
	});
});
