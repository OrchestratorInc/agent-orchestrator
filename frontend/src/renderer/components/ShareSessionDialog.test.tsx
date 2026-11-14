import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { CloudCpError, type CloudCpShareLink } from "../lib/cloud-cp";
import { createdShareLinkUrl } from "../lib/share-links";
import { broadestLink, forgetUrlsMadeHere, isPersonalWorkspace, remainingHours, shareLinkRequest, urlForBroadest } from "./share/linkAccess";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	client: {
		listShareLinks: vi.fn(),
		createShareLink: vi.fn(),
		revokeShareLink: vi.fn(),
		// The people half of the dialog: nobody has access yet, no teams.
		getShareAccess: vi.fn(async (): Promise<unknown> => ({ entries: [] })),
		listTeams: vi.fn(async () => ({ teams: [] })),
		listShareCandidates: vi.fn(async () => ({ candidates: [] })),
		// Three people in the workspace.
		listOrgMembers: vi.fn(async () => ({ members: [{ userId: "a" }, { userId: "b" }, { userId: "c" }] })),
	},
	org: { id: "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", displayName: "Northwind", offering: { kind: "b2b" } } as Record<string, unknown>,
}));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.example.test", userId: "me" }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: mocks.org }) }));

const ORG = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
const CODE = "k7Hq2x9PmB4wR5tY8uI0oP";
const LINK_A = "0c927f48-7214-4fb1-833a-7c1af5b7bf80";
const LINK_B = "1d038059-8325-4ac2-944b-8d2b06c8cf91";
const NEW_LINK = "2e149160-9436-4bd3-855c-9e3c17d9d0a2";
const SHORT = `https://orchestrator.inc/s/${CODE}`;
const DAY = 86_400_000;

function shareLink(overrides: Partial<CloudCpShareLink>): CloudCpShareLink {
	return {
		id: LINK_A, orgId: ORG, projectId: "project", role: "viewer", status: "active", accessScope: "anyone",
		recipients: [], interaction: "view", modeCap: "read-only", deniedCommands: [], createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
		...overrides,
	};
}

function renderDialog(props: Partial<React.ComponentProps<typeof ShareSessionDialog>> = {}) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s1" sessionName="Fix flaky auth test" projectName="acme/web" {...props} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

const card = () => screen.findByRole("region", { name: "General access" });
async function sentence(): Promise<string> {
	const region = await card();
	await waitFor(() => expect(region.querySelector("[data-general-access-sentence]")).not.toBeNull());
	return region.querySelector("[data-general-access-sentence]")?.textContent ?? "";
}
const copyButton = () => document.querySelector<HTMLButtonElement>("[data-share-copy-link]")!;

async function chooseAudience(name: string) {
	await userEvent.click(await within(await card()).findByRole("button", { name: /Only people with access|Anyone with the link/ }));
	await userEvent.click(await screen.findByRole("menuitemradio", { name: new RegExp(name) }));
}

/** A confirmation's button, once the links it counts from have been read again. */
async function confirmAction(confirm: HTMLElement, name: string): Promise<HTMLElement> {
	const button = within(confirm).getByRole("button", { name });
	await waitFor(() => expect(button).toBeEnabled());
	return button;
}

describe("general access", () => {
	// Radix's focus scope moves focus from a 0 ms timer when a dialog unmounts.
	// Unmount, then wait one tick so that timer fires while this file's DOM
	// still exists; left pending, it fires into the next test file's jsdom
	// window on a shared CI worker and fails the run as an unhandled error.
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
		vi.restoreAllMocks();
	});
	beforeEach(() => {
		mocks.org = { id: ORG, displayName: "Northwind", offering: { kind: "b2b" } };
		mocks.client.listShareLinks.mockReset().mockResolvedValue({ links: [] });
		mocks.client.createShareLink.mockReset().mockResolvedValue({ link: shareLink({ id: NEW_LINK, sessionId: "s1", code: CODE, shortUrl: SHORT }) });
		mocks.client.revokeShareLink.mockReset().mockResolvedValue({});
		mocks.client.getShareAccess.mockReset().mockImplementation(async () => ({ entries: [] }));
		forgetUrlsMadeHere();
	});

	it("names the session and its project, and starts at Only people with access with nothing to copy", async () => {
		renderDialog();
		expect(await screen.findByRole("heading", { name: "Share “Fix flaky auth test”" })).toBeInTheDocument();
		expect(screen.getByText("Session in acme/web")).toBeInTheDocument();
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session.");
		// Disabled for screen readers too, with the reason, but still focusable.
		expect(copyButton()).toHaveAttribute("aria-disabled", "true");
		expect(copyButton()).not.toBeDisabled();
		expect(copyButton()).toHaveAccessibleDescription("Turn on link access to copy a link. People you add above find this session under Shared with you.");
		expect(copyButton()).toHaveTextContent("Copy link");
		fireEvent.click(copyButton());
		expect(mocks.client.createShareLink).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
	});

	it.each([
		["a team workspace", { displayName: "Northwind", kind: "team" }, false],
		["an enterprise workspace", { displayName: "Northwind", kind: "enterprise" }, false],
		["a personal workspace", { displayName: "Northwind", kind: "personal" }, true],
		["a team workspace named like someone's", { displayName: "Pritom's organization", kind: "team" }, false],
		// An older control plane names no kind: a name or an offering never makes one personal.
		["one named \"<name>'s organization\" by an older control plane", { displayName: "Pritom's organization", offering: { kind: "both" } }, false],
		["one named after the account by an older control plane", { displayName: "asha@northwind.dev", offering: { kind: "b2c" } }, false],
		["one still loading", undefined, false],
	])("knows %s is personal or not", (_name, org, personal) => {
		expect(isPersonalWorkspace(org as { kind?: string } | undefined)).toBe(personal);
	});

	it("says only you and people added above, with no workspace row, when the control plane says the workspace is personal", async () => {
		mocks.org = { id: ORG, displayName: "Asha's organization", kind: "personal" };
		renderDialog();
		expect(await sentence()).toBe("Only you and people added above can open this session.");
		expect(document.querySelector("[data-share-workspace-row]")).toBeNull();
	});

	it("lists the workspace's people when the control plane says it is a team's, whatever its name", async () => {
		mocks.org = { id: ORG, displayName: "Pritom's organization", kind: "team" };
		renderDialog();
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session.");
		await waitFor(() => expect(document.querySelector("[data-share-workspace-row]")).toHaveTextContent("Everyone in your workspacePritom's organization · 3 members"));
	});

	it("lists the workspace's people against an older control plane, even with a name like someone's", async () => {
		mocks.org = { id: ORG, displayName: "Pritom's organization", offering: { kind: "b2c" } };
		renderDialog();
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session.");
		await waitFor(() => expect(document.querySelector("[data-share-workspace-row]")).toHaveTextContent("Everyone in your workspacePritom's organization · 3 members"));
	});

	it.each([
		["still loading", undefined],
		["unreadable or another organization's", { id: "another-org", displayName: "Elsewhere", kind: "personal" }],
	])("never says \"only you\" while the workspace is %s", async (_state, org) => {
		mocks.org = org as Record<string, unknown>;
		renderDialog();
		expect(await sentence()).toBe("Only people with access can open this session.");
		// The workspace's people still have access, so they still show.
		await waitFor(() => expect(document.querySelector("[data-share-workspace-row]")).not.toBeNull());
	});

	it("turns link access on with a Can watch link for 7 days, then copies the link it made", async () => {
		const remember = vi.spyOn(aoBridge.sessionShareUrls, "remember");
		const recall = vi.spyOn(aoBridge.sessionShareUrls, "recall");
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderDialog();
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ id: NEW_LINK, sessionId: "s1", expiresAt: new Date(Date.now() + 7 * DAY).toISOString() })] });
		recall.mockResolvedValue({ [NEW_LINK]: SHORT });
		await chooseAudience("Anyone with the link");
		await waitFor(() => expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", {
			sessionId: "s1", role: "viewer", accessScope: "anyone", interaction: "view", modeCap: "read-only", expiresInHours: 168,
		}));
		expect(remember).toHaveBeenCalledWith("https://cp.example.test", NEW_LINK, SHORT);
		await waitFor(async () => expect(await sentence()).toBe("Anyone signed in to Orchestrator.inc with the link can watch this session: the chat, terminal and files."));
		await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "ready"));
		fireEvent.click(copyButton());
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
		expect(await screen.findByRole("button", { name: "Link copied" })).toBeInTheDocument();
		expect(document.querySelector("[data-share-announcement]")).toHaveTextContent("Link copied. Anyone signed in to Orchestrator.inc with the link can watch this session");
		// Copying never makes a second link.
		expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1);
	});

	it("makes a new link with the same access when this device never saw the link's URL", async () => {
		const expiresAt = new Date(Date.now() + 2 * DAY).toISOString();
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard", expiresAt })] });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderDialog();
		await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "new"));
		expect(copyButton()).toHaveTextContent("Copy new link");
		fireEvent.click(copyButton());
		// Another link that can write: it asks, as turning writing on does.
		const confirm = await screen.findByRole("dialog", { name: "Let anyone with the link write?" });
		expect(confirm).toHaveTextContent("your repository access and its secrets");
		expect(mocks.client.createShareLink).not.toHaveBeenCalled();
		fireEvent.click(await confirmAction(confirm, "Allow writing"));
		// Rounded down: the copy never outlasts the link it stands in for.
		await waitFor(() => expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", {
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard", expiresInHours: 47,
		}));
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
	});

	it("describes the broadest of several links and counts the rest", async () => {
		mocks.client.listShareLinks.mockResolvedValue({
			links: [
				shareLink({ id: LINK_A, sessionId: "s1" }),
				shareLink({ id: LINK_B, sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard" }),
				shareLink({ id: "revoked", sessionId: "s1", status: "revoked", role: "editor" }),
				shareLink({ id: "expired", sessionId: "s1", expiresAt: "2020-01-01T00:00:00Z" }),
				shareLink({ id: "other-session", sessionId: "s2", role: "editor" }),
			],
		});
		renderDialog();
		await waitFor(async () => expect(await sentence()).toBe("Anyone signed in to Orchestrator.inc with the link can write to the agent, which runs in your sandbox with your repository access and its secrets."));
		const region = await card();
		expect(region).toHaveTextContent("Never expires");
		expect(region).toHaveTextContent("and 1 more link");
		// Standard is what writing means, so the trigger names only the level.
		expect(within(region).getByRole("button", { name: "Change access: Can write" })).toHaveTextContent(/^Can write$/);
	});

	it("never offers Trusted on a link", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard" })] });
		renderDialog();
		await userEvent.click(await within(await card()).findByRole("button", { name: /Change access/ }));
		expect(await screen.findByRole("menuitem", { name: "Standard" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Read only" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Trusted" })).toBeNull();
		expect(screen.queryByRole("menuitem", { name: /Remove access/ })).toBeNull();
	});

	it("asks before letting anyone with the link write, then replaces the link", async () => {
		const expiresAt = new Date(Date.now() + 3 * DAY).toISOString();
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", expiresAt })] });
		renderDialog();
		await userEvent.click(await within(await card()).findByRole("button", { name: /Change access: Can watch/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can write/ }));
		const confirm = await screen.findByRole("dialog", { name: "Let anyone with the link write?" });
		expect(confirm).toHaveTextContent("your repository access and its secrets");
		expect(confirm).toHaveTextContent("this makes a new link and the current one stops working");
		expect(mocks.client.createShareLink).not.toHaveBeenCalled();
		fireEvent.click(await confirmAction(confirm, "Allow writing"));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined));
		// The new link is made first, so a failure leaves the old one working.
		expect(mocks.client.createShareLink.mock.invocationCallOrder[0]).toBeLessThan(mocks.client.revokeShareLink.mock.invocationCallOrder[0]!);
		expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", {
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard", expiresInHours: 71,
		});
	});

	it("turns link access off after saying how many links stop, and can remove the people who joined by them", async () => {
		const forget = vi.spyOn(aoBridge.sessionShareUrls, "forget");
		mocks.client.listShareLinks.mockResolvedValue({
			links: [shareLink({ id: LINK_A, sessionId: "s1", joinedCount: 2 }), shareLink({ id: LINK_B, sessionId: "s1", joinedCount: 1 })],
		});
		// The control plane's own count, which may differ from the list's.
		mocks.client.revokeShareLink.mockImplementation(async (_org: string, _project: string, id: string) => ({ removedGrants: id === LINK_A ? 3 : 1 }));
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		expect(confirm).toHaveTextContent("2 links stop working.");
		const turnOff = await confirmAction(confirm, "Turn off");
		// Never ticked for the owner: until they tick it, the people who joined keep access.
		expect(within(confirm).getByRole("checkbox")).not.toBeChecked();
		expect(confirm).toHaveTextContent("People who already opened them keep their access.");
		fireEvent.click(within(confirm).getByRole("checkbox"));
		expect(confirm).not.toHaveTextContent("keep their access");
		// Several links: the people joined by one of them or another.
		expect(confirm).toHaveTextContent("Also remove the 3 people who joined via link");
		fireEvent.click(turnOff);
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_B, { removeJoined: true }));
		expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, { removeJoined: true });
		await waitFor(() => expect(forget).toHaveBeenCalledWith("https://cp.example.test", [LINK_A, LINK_B]));
		expect(await screen.findByText("Link access turned off, and the 4 people who joined by it lost access.")).toBeInTheDocument();
	});

	it("says people who opened a link keep access when the control plane can't remove them", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		expect(confirm).toHaveTextContent("The link stops working. People who already opened it keep their access.");
		expect(within(confirm).queryByRole("checkbox")).toBeNull();
		fireEvent.click(await confirmAction(confirm, "Turn off"));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined));
	});

	it("lists every live link under Link settings, each revocable", async () => {
		mocks.client.listShareLinks.mockResolvedValue({
			links: [shareLink({ id: LINK_A, sessionId: "s1", joinedCount: 0 }), shareLink({ id: LINK_B, sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard", joinedCount: 0 })],
		});
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Link settings" }));
		expect(await screen.findByRole("heading", { name: "Link settings" })).toBeInTheDocument();
		const rows = within(document.querySelector<HTMLElement>("[data-share-active-links]")!).getAllByRole("listitem");
		expect(rows).toHaveLength(2);
		expect(rows[1]).toHaveTextContent("Can write");
		expect(rows[1]).toHaveTextContent("0 joined");
		fireEvent.click(within(rows[0]!).getByRole("button", { name: "Revoke" }));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined));
		await userEvent.click(screen.getByRole("button", { name: "Back" }));
		expect(await screen.findByRole("heading", { name: "Share “Fix flaky auth test”" })).toBeInTheDocument();
	});

	it("shares the whole project instead from the line under the title", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ id: LINK_B, role: "editor", interaction: "interact", modeCap: "standard" })] });
		renderDialog();
		// The project's link opens this session too.
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this session. A link to the whole project opens it too.");
		fireEvent.click(screen.getByRole("button", { name: "Share the whole project instead" }));
		expect(await screen.findByRole("heading", { name: "Share project “acme/web”" })).toBeInTheDocument();
		await waitFor(async () => expect(await sentence()).toBe("Anyone signed in to Orchestrator.inc with the link can watch every session in this project and start their own, which run in your sandbox with your repository access and its secrets."));
		await waitFor(() => expect(mocks.client.getShareAccess).toHaveBeenCalledWith(ORG, "project", undefined));
	});

	it("shows Share the whole project instead as an action: a button with a dotted underline that the keyboard reaches", async () => {
		renderDialog();
		const cta = await screen.findByRole("button", { name: "Share the whole project instead" });
		expect(cta.tagName).toBe("BUTTON");
		expect(cta).toHaveAttribute("type", "button");
		// Underlined at rest, dotted, so it reads as something to press, not as plain text.
		expect(cta.className).not.toContain("no-underline");
		expect(cta.className).toMatch(/(^|\s)underline(\s|$)/);
		expect(cta.className).toContain("decoration-dotted");
		// Hover and keyboard focus brighten it (the app draws no focus rings).
		expect(cta.className).toContain("hover:text-foreground");
		expect(cta.className).toContain("focus-visible:text-foreground");
		cta.focus();
		expect(cta).toHaveFocus();
		await userEvent.keyboard("{Enter}");
		expect(await screen.findByRole("heading", { name: "Share project “acme/web”" })).toBeInTheDocument();
		// The way back reads the same way.
		expect((await screen.findByRole("button", { name: "Share only this session" })).className).toContain("decoration-dotted");
	});

	it("shares a Cloud standalone agent on its own: no whole project to switch to, and only its owner and people added can open it", async () => {
		renderDialog({ standaloneAgent: true, projectName: "Fix flaky auth test" });
		expect(await screen.findByRole("heading", { name: "Share “Fix flaky auth test”" })).toBeInTheDocument();
		expect(screen.getByText("Standalone agent")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Share the whole project instead" })).toBeNull();
		// Nobody else in the workspace can open it until added.
		expect(await sentence()).toBe("Only you and people added above can open this session.");
		expect(document.querySelector("[data-share-workspace-row]")).toBeNull();
		await waitFor(() => expect(mocks.client.getShareAccess).toHaveBeenCalledWith(ORG, "project", "s1"));
	});

	it("shares only the whole project when opened without a session, as Project settings does", async () => {
		renderDialog({ sessionId: undefined, sessionName: undefined });
		expect(await screen.findByRole("heading", { name: "Share project “acme/web”" })).toBeInTheDocument();
		expect(screen.getByText("Includes every session, also ones started later.")).toBeInTheDocument();
		// No session to go back to.
		expect(screen.queryByRole("button", { name: "Share only this session" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Share the whole project instead" })).toBeNull();
		expect(await sentence()).toBe("Only members of your workspace and people added above can open this project.");
		await waitFor(() => expect(mocks.client.getShareAccess).toHaveBeenCalledWith(ORG, "project", undefined));
		expect(mocks.client.getShareAccess).not.toHaveBeenCalledWith(ORG, "project", expect.any(String));
		// Turning link access on makes a link to the whole project, naming no session.
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: NEW_LINK, code: CODE, shortUrl: SHORT }) });
		await chooseAudience("Anyone with the link");
		await waitFor(() => expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1));
		expect(mocks.client.createShareLink.mock.calls[0]?.[2]).not.toHaveProperty("sessionId");
	});

	it("is General access and Copy link only in a build without Teams", async () => {
		renderDialog({ people: false });
		expect(await sentence()).toBe("Only people with access can open this session.");
		expect(screen.queryByRole("combobox", { name: "Add people or teams" })).toBeNull();
		expect(screen.queryByText("People with access")).toBeNull();
		expect(mocks.client.getShareAccess).not.toHaveBeenCalled();
	});

	it("offers links only, with no error, against a control plane from before teams", async () => {
		mocks.client.getShareAccess.mockReset().mockRejectedValue(new CloudCpError("404 page not found", { status: 404 }));
		renderDialog();
		await waitFor(() => expect(screen.queryByRole("combobox", { name: "Add people or teams" })).toBeNull());
		expect(screen.queryByRole("alert")).toBeNull();
		expect(await card()).toBeInTheDocument();
		// A 404 is the answer, not a failure to retry.
		expect(mocks.client.getShareAccess).toHaveBeenCalledTimes(1);
	});

	it("shows a project or session the control plane can't find as an error, and keeps people for every other session", async () => {
		// A control plane with teams says "not_found" for an archived project or
		// a session not in the project; that is no reason to drop to links only.
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		mocks.client.getShareAccess.mockReset().mockRejectedValueOnce(new CloudCpError("The requested resource was not found.", { status: 404, code: "not_found" }));
		const first = render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s1" />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByRole("alert")).toHaveTextContent("The requested resource was not found.");
		expect(screen.getByRole("combobox", { name: "Add people or teams" })).toBeInTheDocument();
		first.unmount();

		// Another session's dialog in the same app run offers people from the start.
		mocks.client.getShareAccess.mockImplementation(() => new Promise(() => undefined));
		render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s2" />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		expect(screen.getByRole("combobox", { name: "Add people or teams" })).toBeInTheDocument();
	});

	it("says the link is missing when the response has neither a usable code nor a token", async () => {
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: NEW_LINK, code: "not-a-code" }) });
		renderDialog();
		await chooseAudience("Anyone with the link");
		expect((await screen.findByRole("alert")).textContent).toBe("The control plane did not return the link.");
	});

	it("shows the link to select by hand when the clipboard refuses it", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
		vi.spyOn(aoBridge.sessionShareUrls, "recall").mockResolvedValue({ [LINK_A]: SHORT });
		vi.spyOn(aoBridge.clipboard, "writeText").mockRejectedValue(new Error("denied"));
		renderDialog();
		await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "ready"));
		fireEvent.click(copyButton());
		expect(await screen.findByRole("textbox", { name: "Share link" })).toHaveValue(SHORT);
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't copy.");
	});
});

describe("link access, made safe", () => {
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
		vi.restoreAllMocks();
		document.querySelector("[data-testid=terminal]")?.remove();
	});
	beforeEach(() => {
		mocks.org = { id: ORG, displayName: "Northwind", kind: "team" };
		mocks.client.listShareLinks.mockReset().mockResolvedValue({ links: [] });
		mocks.client.createShareLink.mockReset().mockResolvedValue({ link: shareLink({ id: NEW_LINK, sessionId: "s1", code: CODE, shortUrl: SHORT }) });
		mocks.client.revokeShareLink.mockReset().mockResolvedValue({});
		mocks.client.getShareAccess.mockReset().mockImplementation(async () => ({ entries: [] }));
		forgetUrlsMadeHere();
	});
	const dialog = () => screen.getByRole("dialog");
	/** Something outside the dialog that had focus first, as the agent terminal does. */
	function focusTerminal() {
		const terminal = document.createElement("textarea");
		terminal.dataset.testid = "terminal";
		document.body.append(terminal);
		terminal.focus();
		return terminal;
	}

	describe("focus in a links-only build", () => {
		it("lands in the dialog while the links load, never on the page behind", async () => {
			const terminal = focusTerminal();
			mocks.client.listShareLinks.mockImplementation(() => new Promise(() => undefined));
			renderDialog({ people: false });
			await waitFor(() => expect(dialog().contains(document.activeElement)).toBe(true));
			expect(document.activeElement).not.toBe(terminal);
		});

		it("lands on General access when link access is off", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [] });
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			// Opened a second time, with the links already read.
			await queryClient.fetchQuery({ queryKey: ["cloud-share-links", "https://cp.example.test", ORG, "project"], queryFn: async () => [] });
			focusTerminal();
			render(
				<QueryClientProvider client={queryClient}>
					<TooltipProvider>
						<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s1" people={false} />
					</TooltipProvider>
				</QueryClientProvider>,
			);
			await waitFor(() => expect(document.activeElement).toHaveAttribute("data-general-access-menu"));
		});

		it("lands on Copy link when it can copy", async () => {
			const link = shareLink({ sessionId: "s1" });
			vi.spyOn(aoBridge.sessionShareUrls, "recall").mockResolvedValue({ [LINK_A]: SHORT });
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			await queryClient.fetchQuery({ queryKey: ["cloud-share-links", "https://cp.example.test", ORG, "project"], queryFn: async () => [link] });
			await queryClient.fetchQuery({ queryKey: ["share-link-urls", "https://cp.example.test", "me", LINK_A], queryFn: async () => ({ [LINK_A]: SHORT }) });
			focusTerminal();
			render(
				<QueryClientProvider client={queryClient}>
					<TooltipProvider>
						<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s1" people={false} />
					</TooltipProvider>
				</QueryClientProvider>,
			);
			await waitFor(() => expect(document.activeElement).toBe(copyButton()));
		});
	});

	describe("a Trusted link made before links stopped offering it", () => {
		const trusted = () => shareLink({ sessionId: "s1", role: "editor", interaction: "interact", modeCap: "trusted", joinedCount: 2, expiresAt: new Date(Date.now() + 3 * DAY).toISOString() });

		it("shows its real access, and its access menu replaces it with less", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [trusted()] });
			renderDialog();
			const trigger = await within(await card()).findByRole("button", { name: /Change access: Can write/ });
			expect(trigger).toHaveTextContent("Can write· Trusted");
			await userEvent.click(trigger);
			const current = await screen.findByRole("menuitem", { name: "Trusted" });
			expect(current.querySelector("svg")).not.toBeNull();
			await userEvent.click(screen.getByRole("menuitem", { name: "Standard" }));
			const confirm = await screen.findByRole("dialog", { name: "Make a new link?" });
			// Narrower access still leaves who joined alone unless the owner ticks it.
			expect(within(confirm).getByRole("checkbox")).not.toBeChecked();
			expect(confirm).toHaveTextContent("Also remove the 2 people who joined via link");
			fireEvent.click(await confirmAction(confirm, "Make new link"));
			await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined));
			expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", expect.objectContaining({ role: "editor", modeCap: "standard" }));
		});

		it("never makes another write link from Copy: it asks to replace the Trusted one, then copies the new link", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [trusted()] });
			const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
			renderDialog();
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "replace"));
			expect(copyButton()).toHaveAccessibleDescription(/New links can't be Trusted/);
			// Asking again and again makes nothing.
			for (let attempt = 0; attempt < 3; attempt += 1) {
				fireEvent.click(copyButton());
				const confirm = await screen.findByRole("dialog", { name: "Replace the Trusted link?" });
				fireEvent.click(within(confirm).getByRole("button", { name: "Cancel" }));
				await waitFor(() => expect(screen.queryByRole("dialog", { name: "Replace the Trusted link?" })).toBeNull());
			}
			expect(mocks.client.createShareLink).not.toHaveBeenCalled();
			fireEvent.click(copyButton());
			const confirm = await screen.findByRole("dialog", { name: "Replace the Trusted link?" });
			expect(confirm).toHaveTextContent("the current one stops working");
			// The people who joined keep the access the old link gave them unless the owner removes them.
			expect(within(confirm).getByRole("checkbox")).not.toBeChecked();
			expect(confirm).toHaveTextContent("People who already opened it keep their access.");
			fireEvent.click(await confirmAction(confirm, "Make new link"));
			await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
			expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1);
			expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", expect.objectContaining({ role: "editor", modeCap: "standard" }));
			expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined);
		});

		it("removes who joined by the Trusted link it replaces only when the owner ticks it", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [trusted()] });
			const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
			renderDialog();
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "replace"));
			fireEvent.click(copyButton());
			const confirm = await screen.findByRole("dialog", { name: "Replace the Trusted link?" });
			const make = await confirmAction(confirm, "Make new link");
			fireEvent.click(within(confirm).getByRole("checkbox"));
			fireEvent.click(make);
			await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, { removeJoined: true }));
			await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
		});

		it("is named with its cap in Link settings", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [trusted()] });
			renderDialog();
			await userEvent.click(await screen.findByRole("button", { name: "Link settings" }));
			expect(await screen.findByText("Can write · Trusted")).toBeInTheDocument();
		});
	});

	describe("turning several links off", () => {
		const three = () => [
			shareLink({ id: LINK_A, sessionId: "s1" }),
			shareLink({ id: LINK_B, sessionId: "s1" }),
			shareLink({ id: NEW_LINK, sessionId: "s1" }),
		];

		it("tries every link, counts one already revoked as off, and names the one still working", async () => {
			const forget = vi.spyOn(aoBridge.sessionShareUrls, "forget");
			mocks.client.listShareLinks.mockResolvedValue({ links: three() });
			mocks.client.revokeShareLink.mockImplementation(async (_org: string, _project: string, id: string) => {
				if (id === LINK_A) throw new CloudCpError("Not found", { status: 404 });
				if (id === LINK_B) throw new CloudCpError("Internal error", { status: 500 });
			});
			renderDialog();
			await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
			await chooseAudience("Only people with access");
			const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
			// Several links: "them", not "it".
			expect(confirm).toHaveTextContent("3 links stop working. People who already opened them keep their access.");
			fireEvent.click(await confirmAction(confirm, "Turn off"));
			await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledTimes(3));
			expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't turn off 1 link. It still works.");
			await waitFor(() => expect(forget).toHaveBeenCalledWith("https://cp.example.test", [LINK_A, NEW_LINK]));
		});

		it("says the old link still works when a replacement couldn't turn it off", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
			mocks.client.revokeShareLink.mockRejectedValue(new CloudCpError("Internal error", { status: 500 }));
			renderDialog();
			await userEvent.click(await within(await card()).findByRole("button", { name: /Change access: Can watch/ }));
			await userEvent.click(await screen.findByRole("menuitem", { name: /Can write/ }));
			fireEvent.click(await confirmAction(await screen.findByRole("dialog", { name: "Let anyone with the link write?" }), "Allow writing"));
			expect(await screen.findByRole("alert")).toHaveTextContent(
				"The new link is ready, but 1 old link couldn't be turned off and still works. Revoke it in Link settings.",
			);
		});
	});

	it("counts who joined from the links as they are when the confirmation opens", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", joinedCount: 0 })] });
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		// Someone joined while the dialog was open.
		let answer!: (value: { links: CloudCpShareLink[] }) => void;
		mocks.client.listShareLinks.mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		expect(within(confirm).getByRole("button", { name: "Turn off" })).toBeDisabled();
		answer({ links: [shareLink({ sessionId: "s1", joinedCount: 1 })] });
		const turnOff = await confirmAction(confirm, "Turn off");
		expect(confirm).toHaveTextContent("Also remove the person who joined via link");
		expect(confirm).toHaveTextContent("People who already opened it keep their access.");
		fireEvent.click(within(confirm).getByRole("checkbox"));
		expect(confirm).not.toHaveTextContent("keep their access");
		fireEvent.click(turnOff);
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, { removeJoined: true }));
	});

	it("reads who has access again once people who joined by a link are removed", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", joinedCount: 1 })] });
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		const reads = mocks.client.getShareAccess.mock.calls.length;
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		const turnOff = await confirmAction(confirm, "Turn off");
		fireEvent.click(within(confirm).getByRole("checkbox"));
		fireEvent.click(turnOff);
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalled());
		await waitFor(() => expect(mocks.client.getShareAccess.mock.calls.length).toBeGreaterThan(reads));
	});

	it.each([
		["leaves who joined alone by default", false, undefined],
		["removes who joined once the owner ticks it", true, { removeJoined: true }],
	])("narrowing a link's access offers to remove who joined, unticked, and %s", async (_name, tick, options) => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard", joinedCount: 2 })] });
		renderDialog();
		await userEvent.click(await within(await card()).findByRole("button", { name: /Change access: Can write/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Can watch/ }));
		const confirm = await screen.findByRole("dialog", { name: "Make a new link?" });
		const make = await confirmAction(confirm, "Make new link");
		expect(within(confirm).getByRole("checkbox")).not.toBeChecked();
		expect(confirm).toHaveTextContent("Also remove the 2 people who joined via link");
		if (tick) fireEvent.click(within(confirm).getByRole("checkbox"));
		fireEvent.click(make);
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, options));
		expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", expect.objectContaining({ role: "viewer", modeCap: "read-only" }));
	});

	it.each([
		["leaves who joined alone by default", false, undefined],
		["removes who joined once the owner ticks it", true, { removeJoined: true }],
	])("revoking a link in Link settings %s", async (_name, tick, options) => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", joinedCount: 2 })] });
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Link settings" }));
		fireEvent.click(await waitFor(() => {
			const button = document.querySelector<HTMLButtonElement>(`[data-share-revoke="${LINK_A}"]`);
			expect(button).not.toBeNull();
			return button!;
		}));
		const confirm = await screen.findByRole("dialog", { name: "Revoke this link?" });
		const revoke = await confirmAction(confirm, "Revoke");
		expect(within(confirm).getByRole("checkbox")).not.toBeChecked();
		expect(confirm).toHaveTextContent("Also remove the 2 people who joined by this link");
		if (tick) fireEvent.click(within(confirm).getByRole("checkbox"));
		fireEvent.click(revoke);
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, options));
	});

	it("turning link access off leaves who joined alone unless the owner ticks it", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", joinedCount: 2 })] });
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		const turnOff = await confirmAction(confirm, "Turn off");
		expect(within(confirm).getByRole("checkbox")).not.toBeChecked();
		fireEvent.click(turnOff);
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined));
		expect(await screen.findByText("Link access turned off. People who opened the link keep access.")).toBeInTheDocument();
	});

	it("asks before raising a Can write link from Read only agent permissions to Standard", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", role: "editor", interaction: "interact", modeCap: "read-only" })] });
		renderDialog();
		await userEvent.click(await within(await card()).findByRole("button", { name: /Change access: Can write/ }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Standard" }));
		const confirm = await screen.findByRole("dialog", { name: "Let anyone with the link write?" });
		expect(confirm).toHaveTextContent("your repository access and its secrets");
		fireEvent.click(await confirmAction(confirm, "Allow writing"));
		await waitFor(() => expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", expect.objectContaining({ role: "editor", modeCap: "standard" })));
	});

	describe("Copy new link for a link that can write", () => {
		const writer = () => shareLink({ sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard", expiresAt: new Date(Date.now() + 2 * DAY).toISOString() });

		it("makes nothing when the owner doesn't allow it", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [writer()] });
			const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
			renderDialog();
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "new"));
			fireEvent.click(copyButton());
			const confirm = await screen.findByRole("dialog", { name: "Let anyone with the link write?" });
			expect(confirm).toHaveTextContent("the current one keeps working until it expires");
			// Nothing stops, so there is nobody to remove.
			expect(within(confirm).queryByRole("checkbox")).toBeNull();
			fireEvent.click(within(confirm).getByRole("button", { name: "Cancel" }));
			await waitFor(() => expect(screen.queryByRole("dialog", { name: "Let anyone with the link write?" })).toBeNull());
			expect(mocks.client.createShareLink).not.toHaveBeenCalled();
			expect(mocks.client.revokeShareLink).not.toHaveBeenCalled();
			expect(writeText).not.toHaveBeenCalled();
		});

		it("doesn't ask for a watch-only link", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1", expiresAt: new Date(Date.now() + 2 * DAY).toISOString() })] });
			vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
			renderDialog();
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "new"));
			fireEvent.click(copyButton());
			await waitFor(() => expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1));
			expect(screen.queryByRole("dialog", { name: "Let anyone with the link write?" })).toBeNull();
		});
	});

	it("copies the link it made, not another new one, when the control plane's clock is behind this device's", async () => {
		// The control plane is 3 hours behind, so a copy counted on this
		// device's clock ends over an hour before the link it stands in for.
		const skew = 3 * 3_600_000;
		const broadest = shareLink({
			sessionId: "s1", createdAt: new Date(Date.now() - DAY - skew).toISOString(),
			expiresAt: new Date(Date.now() + 2 * DAY - 30 * 60_000).toISOString(),
		});
		const serverNow = new Date(Date.now() - skew);
		const made = shareLink({
			id: NEW_LINK, sessionId: "s1", code: CODE, shortUrl: SHORT, createdAt: serverNow.toISOString(),
			expiresAt: new Date(serverNow.getTime() + 47 * 3_600_000).toISOString(),
		});
		let links = [broadest];
		mocks.client.listShareLinks.mockImplementation(async () => ({ links }));
		mocks.client.createShareLink.mockImplementation(async () => {
			const { code: _code, shortUrl: _shortUrl, ...listed } = made;
			links = [broadest, listed];
			return { link: made };
		});
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderDialog();
		await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "new"));
		fireEvent.click(copyButton());
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
		expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", expect.objectContaining({ expiresInHours: 47 }));
		// Once the links are read again (the button still says "Link copied").
		await waitFor(() => expect(mocks.client.listShareLinks.mock.calls.length).toBeGreaterThan(1));
		await waitFor(() => expect(copyButton()).not.toHaveAttribute("data-share-copy-busy"));
		fireEvent.click(copyButton());
		await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
		expect(writeText).toHaveBeenLastCalledWith(SHORT);
		expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1);
	});

	it("copies the link it made even when reading the links again fails", async () => {
		const broadest = shareLink({ sessionId: "s1", expiresAt: new Date(Date.now() + 2 * DAY).toISOString() });
		mocks.client.listShareLinks.mockResolvedValueOnce({ links: [broadest] }).mockRejectedValue(new CloudCpError("Unavailable", { status: 503 }));
		mocks.client.createShareLink.mockResolvedValue({
			link: shareLink({ id: NEW_LINK, sessionId: "s1", code: CODE, shortUrl: SHORT, createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 47 * 3_600_000).toISOString() }),
		});
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderDialog();
		await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "new"));
		fireEvent.click(copyButton());
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
		// Once the links are read again (the button still says "Link copied").
		await waitFor(() => expect(mocks.client.listShareLinks.mock.calls.length).toBeGreaterThan(1));
		await waitFor(() => expect(copyButton()).not.toHaveAttribute("data-share-copy-busy"));
		fireEvent.click(copyButton());
		await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
		expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1);
	});

	describe("Copy link waits", () => {
		it("while a change to the links runs", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
			vi.spyOn(aoBridge.sessionShareUrls, "recall").mockResolvedValue({ [LINK_A]: SHORT });
			const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
			mocks.client.revokeShareLink.mockImplementation(() => new Promise(() => undefined));
			renderDialog();
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "ready"));
			await chooseAudience("Only people with access");
			fireEvent.click(await confirmAction(await screen.findByRole("dialog", { name: "Turn off link access?" }), "Turn off"));
			await waitFor(() => expect(copyButton()).toHaveAttribute("aria-disabled", "true"));
			fireEvent.click(copyButton());
			expect(writeText).not.toHaveBeenCalled();
		});

		it("while this device's URLs load", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
			vi.spyOn(aoBridge.sessionShareUrls, "recall").mockImplementation(() => new Promise(() => undefined));
			renderDialog();
			await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
			expect(copyButton()).toHaveAttribute("data-share-copy-link", "loading");
			expect(copyButton()).toHaveAttribute("aria-disabled", "true");
			fireEvent.click(copyButton());
			expect(mocks.client.createShareLink).not.toHaveBeenCalled();
		});

		it("for the person to choose Copy new link when this device's URLs can't be read", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
			const recall = vi.spyOn(aoBridge.sessionShareUrls, "recall").mockRejectedValue(new Error("store unavailable"));
			renderDialog();
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "new"));
			expect(copyButton()).toHaveTextContent("Copy new link");
			expect(mocks.client.createShareLink).not.toHaveBeenCalled();
			// Asked once, not retried.
			expect(recall).toHaveBeenCalledTimes(1);
		});
	});

	it("keeps a link it just made when the preload has no URL store", async () => {
		const store = aoBridge.sessionShareUrls;
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		(aoBridge as { sessionShareUrls?: unknown }).sessionShareUrls = undefined;
		try {
			renderDialog();
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ id: NEW_LINK, sessionId: "s1" })] });
			await chooseAudience("Anyone with the link");
			await waitFor(() => expect(copyButton()).toHaveAttribute("data-share-copy-link", "ready"));
			fireEvent.click(copyButton());
			await waitFor(() => expect(writeText).toHaveBeenCalledWith(SHORT));
			expect(mocks.client.createShareLink).toHaveBeenCalledTimes(1);
		} finally {
			(aoBridge as { sessionShareUrls?: unknown }).sessionShareUrls = store;
		}
	});

	describe("focus around Link settings", () => {
		it("moves to Back, and back to the gear that opened it", async () => {
			renderDialog();
			const gear = await screen.findByRole("button", { name: "Link settings" });
			await userEvent.click(gear);
			await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Back" })));
			await userEvent.click(screen.getByRole("button", { name: "Back" }));
			await waitFor(() => expect(document.activeElement).toHaveAttribute("data-share-settings"));
		});

		it("returns to the Link settings link in General access when that opened it", async () => {
			mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
			renderDialog();
			const region = await card();
			await userEvent.click(await within(region).findByRole("button", { name: "Link settings" }));
			await screen.findByRole("heading", { name: "Link settings" });
			await userEvent.keyboard("{Escape}");
			await waitFor(() => expect(document.activeElement).toHaveAttribute("data-share-link-settings"));
		});

		it("moves to the next link's Revoke once a link is revoked", async () => {
			let links = [shareLink({ id: LINK_A, sessionId: "s1", joinedCount: 0 }), shareLink({ id: LINK_B, sessionId: "s1", joinedCount: 0 })];
			mocks.client.listShareLinks.mockImplementation(async () => ({ links }));
			mocks.client.revokeShareLink.mockImplementation(async (_org: string, _project: string, id: string) => {
				links = links.filter((link) => link.id !== id);
			});
			renderDialog();
			await userEvent.click(await screen.findByRole("button", { name: "Link settings" }));
			const first = await waitFor(() => {
				const button = document.querySelector<HTMLButtonElement>(`[data-share-revoke="${LINK_A}"]`);
				expect(button).not.toBeNull();
				return button!;
			});
			await userEvent.click(first);
			await waitFor(() => expect(document.activeElement).toHaveAttribute("data-share-revoke", LINK_B));
			await userEvent.click(document.activeElement as HTMLElement);
			await waitFor(() => expect(document.activeElement).toHaveAttribute("data-share-back"));
		});
	});
});

describe("share links", () => {
	it("makes a session link, or a whole-project link without naming a session", () => {
		expect(shareLinkRequest({ scope: "session", role: "editor", modeCap: "standard", expiresInHours: 24, sessionId: "s1" })).toEqual({
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard", expiresInHours: 24,
		});
		expect(shareLinkRequest({ scope: "project", role: "editor", modeCap: "read-only", expiresInHours: undefined, sessionId: "s1" })).toEqual({
			role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "read-only",
		});
	});

	it("never makes a Trusted link, and keeps a watch-only link read-only", () => {
		expect(shareLinkRequest({ scope: "session", role: "editor", modeCap: "trusted", expiresInHours: 168, sessionId: "s1" })).toMatchObject({ modeCap: "standard" });
		expect(shareLinkRequest({ scope: "project", role: "viewer", modeCap: "trusted", expiresInHours: 168, sessionId: "s1" })).toMatchObject({
			role: "viewer", interaction: "view", modeCap: "read-only", expiresInHours: 168,
		});
	});

	it("ranks a Trusted link above a Standard one", () => {
		expect(broadestLink([shareLink({ id: "standard", role: "editor", modeCap: "standard" }), shareLink({ id: "trusted", role: "editor", modeCap: "trusted" })])?.id).toBe("trusted");
	});

	it("picks writing over watching, then the higher cap, then the later expiry", () => {
		const watch = shareLink({ id: "watch" });
		const readOnlyWriter = shareLink({ id: "ro", role: "editor", modeCap: "read-only" });
		const soon = shareLink({ id: "soon", role: "editor", modeCap: "standard", expiresAt: "2026-11-08T00:00:00Z" });
		const never = shareLink({ id: "never", role: "editor", modeCap: "standard" });
		expect(broadestLink([watch, readOnlyWriter])?.id).toBe("ro");
		expect(broadestLink([watch, soon, readOnlyWriter])?.id).toBe("soon");
		expect(broadestLink([soon, never])?.id).toBe("never");
		expect(broadestLink([])).toBeUndefined();
	});

	it("never lets a new link outlast the one it stands in for, nor bring back an expired one", () => {
		const now = Date.parse("2026-11-06T12:00:00Z");
		expect(remainingHours({ expiresAt: "2026-11-08T12:00:00Z" }, now)).toBe(48);
		expect(remainingHours({ expiresAt: "2026-11-08T11:59:00Z" }, now)).toBe(47);
		// Links are made in whole hours: some time left is at least one.
		expect(remainingHours({ expiresAt: "2026-11-06T12:10:00Z" }, now)).toBe(1);
		expect(remainingHours({ expiresAt: "2026-11-06T12:00:00Z" }, now)).toBeNull();
		expect(remainingHours({ expiresAt: "2026-11-05T12:00:00Z" }, now)).toBeNull();
		expect(remainingHours({}, now)).toBeUndefined();
	});

	it("copies a copy made here of the broadest link, so Copy new link doesn't make one on every click", () => {
		const broadest = shareLink({ id: LINK_A, role: "editor", modeCap: "standard", expiresAt: "2026-11-08T12:00:00Z" });
		const copy = shareLink({ id: NEW_LINK, role: "editor", modeCap: "standard", expiresAt: "2026-11-08T11:30:00Z" });
		const older = shareLink({ id: LINK_B, role: "editor", modeCap: "standard", createdAt: "2026-10-30T00:00:00Z", expiresAt: "2026-11-07T00:00:00Z" });
		const links = [broadest, copy, older];
		expect(broadestLink(links)?.id).toBe(LINK_A);
		expect(urlForBroadest(broadest, links, { [NEW_LINK]: SHORT })).toBe(SHORT);
		// One made before it and ending much sooner, or with other access, isn't the same link to copy.
		expect(urlForBroadest(broadest, links, { [LINK_B]: SHORT })).toBeUndefined();
		expect(urlForBroadest(broadest, [broadest, { ...copy, role: "viewer", modeCap: "read-only" }], { [NEW_LINK]: SHORT })).toBeUndefined();
	});

	it("knows a copy made here by the control plane's own times, whatever this device's clock says", () => {
		const broadest = shareLink({ id: LINK_A, role: "editor", modeCap: "standard", createdAt: "2026-11-01T00:00:00Z", expiresAt: "2026-11-08T12:00:00Z" });
		// Made after it, ending hours sooner: a copy counted on a clock ahead of the control plane's.
		const copy = shareLink({ id: NEW_LINK, role: "editor", modeCap: "standard", createdAt: "2026-11-06T09:00:00Z", expiresAt: "2026-11-08T08:00:00Z" });
		expect(urlForBroadest(broadest, [broadest, copy], { [NEW_LINK]: SHORT })).toBe(SHORT);
		// Made before it: not a copy of it.
		const earlier = { ...copy, createdAt: "2026-10-31T00:00:00Z" };
		expect(urlForBroadest(broadest, [broadest, earlier], { [NEW_LINK]: SHORT })).toBeUndefined();
	});

	it("copies the link on the public site when the control plane names one for the same code", () => {
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { code: CODE, shortUrl: SHORT })).toBe(SHORT);
		// A short URL for another code, not https, or not a share link is ignored.
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { code: CODE, shortUrl: "https://orchestrator.inc/s/AAAAAAAAAAAAAAAAAAAAAA" })).toBe(`https://cp.example.test/s/${CODE}`);
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { code: CODE, shortUrl: `http://orchestrator.inc/s/${CODE}` })).toBe(`https://cp.example.test/s/${CODE}`);
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { code: CODE, shortUrl: "https://orchestrator.inc/" })).toBe(`https://cp.example.test/s/${CODE}`);
	});

	it("copies the short link when the control plane minted a code, else the token link", () => {
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { code: CODE, token: "tok" })).toBe(`https://cp.example.test/s/${CODE}`);
		expect(createdShareLinkUrl("https://cp.example.test/", ORG, { code: CODE })).toBe(`https://cp.example.test/s/${CODE}`);
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { token: "tok" })).toBe(`https://cp.example.test/app/share#${ORG}/tok`);
		expect(createdShareLinkUrl("https://cp.example.test", ORG, {})).toBeUndefined();
	});

	it("copies the token link when the code is not one the app can open", () => {
		const tokenLink = `https://cp.example.test/app/share#${ORG}/tok`;
		for (const code of ["", "k7Hq2x9PmB4w", "k7Hq2x9PmB4wR5tY8uI0oPZ", "k7Hq2x9PmB4w/5tY8uI0oP", "k7Hq2x9PmB4wR5tY8uI0o "]) {
			expect(createdShareLinkUrl("https://cp.example.test", ORG, { code, token: "tok" }), code).toBe(tokenLink);
			expect(createdShareLinkUrl("https://cp.example.test", ORG, { code }), code).toBeUndefined();
		}
		expect(createdShareLinkUrl("https://cp.example.test", ORG, { code: 12345 as unknown as string, token: "tok" })).toBe(tokenLink);
	});
});
