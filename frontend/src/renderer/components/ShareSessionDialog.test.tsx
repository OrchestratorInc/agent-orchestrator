import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { CloudCpError, type CloudCpShareLink } from "../lib/cloud-cp";
import { createdShareLinkUrl } from "../lib/share-links";
import { broadestLink, isPersonalWorkspace, shareLinkRequest } from "./share/linkAccess";
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
		mocks.client.revokeShareLink.mockReset().mockResolvedValue(undefined);
		mocks.client.getShareAccess.mockReset().mockImplementation(async () => ({ entries: [] }));
	});

	it("names the session and its project, and starts at Only people with access with nothing to copy", async () => {
		renderDialog();
		expect(await screen.findByRole("heading", { name: "Share “Fix flaky auth test”" })).toBeInTheDocument();
		expect(screen.getByText("Session in acme/web")).toBeInTheDocument();
		expect(await sentence()).toBe("Only people in Northwind and people added above can open this session.");
		expect(copyButton()).toBeDisabled();
		expect(copyButton()).toHaveTextContent("Copy link");
		expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
	});

	it.each([
		["a team workspace", { displayName: "Northwind", offering: { kind: "b2b" } }, false],
		["a self-serve workspace", { displayName: "Northwind", offering: { kind: "b2c" } }, true],
		["one named after the account's email", { displayName: "asha@northwind.dev", offering: { kind: "both" } }, true],
		["one named after the person", { displayName: "Asha Rao", offering: { kind: "both" } }, true],
		["\"<name>'s organization\"", { displayName: "Pritom's organization", offering: { kind: "both" } }, true],
	])("knows %s is personal or not", (_name, org, personal) => {
		expect(isPersonalWorkspace(org, { email: "asha@northwind.dev", displayName: "Asha Rao" })).toBe(personal);
	});

	it("says only you and people added above in a personal workspace", async () => {
		mocks.org = { id: ORG, displayName: "Asha's organization", offering: { kind: "b2c" } };
		renderDialog();
		expect(await sentence()).toBe("Only you and people added above can open this session.");
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
		await waitFor(() => expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", {
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard", expiresInHours: 48,
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
		fireEvent.click(within(confirm).getByRole("button", { name: "Allow writing" }));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, undefined));
		// The new link is made first, so a failure leaves the old one working.
		expect(mocks.client.createShareLink.mock.invocationCallOrder[0]).toBeLessThan(mocks.client.revokeShareLink.mock.invocationCallOrder[0]!);
		expect(mocks.client.createShareLink).toHaveBeenCalledWith(ORG, "project", {
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard", expiresInHours: 72,
		});
	});

	it("turns link access off after saying how many links stop, and can remove the people who joined by them", async () => {
		const forget = vi.spyOn(aoBridge.sessionShareUrls, "forget");
		mocks.client.listShareLinks.mockResolvedValue({
			links: [shareLink({ id: LINK_A, sessionId: "s1", joinedCount: 2 }), shareLink({ id: LINK_B, sessionId: "s1", joinedCount: 1 })],
		});
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		expect(confirm).toHaveTextContent("2 links stop working.");
		fireEvent.click(within(confirm).getByRole("checkbox"));
		expect(confirm).toHaveTextContent("Also remove the 3 people who joined by this link");
		fireEvent.click(within(confirm).getByRole("button", { name: "Turn off" }));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_B, { removeJoined: true }));
		expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", LINK_A, { removeJoined: true });
		await waitFor(() => expect(forget).toHaveBeenCalledWith("https://cp.example.test", [LINK_A, LINK_B]));
		expect(await screen.findByText("Link access turned off, and the 3 people who joined by it lost access.")).toBeInTheDocument();
	});

	it("says people who opened a link keep access when the control plane can't remove them", async () => {
		mocks.client.listShareLinks.mockResolvedValue({ links: [shareLink({ sessionId: "s1" })] });
		renderDialog();
		await waitFor(async () => expect((await card()).dataset.shareGeneralAccess).toBe("anyone"));
		await chooseAudience("Only people with access");
		const confirm = await screen.findByRole("dialog", { name: "Turn off link access?" });
		expect(confirm).toHaveTextContent("The link stops working. People who already opened it keep their access.");
		expect(within(confirm).queryByRole("checkbox")).toBeNull();
		fireEvent.click(within(confirm).getByRole("button", { name: "Turn off" }));
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
		expect(await sentence()).toBe("Only people in Northwind and people added above can open this session. A link to the whole project opens it too.");
		fireEvent.click(screen.getByRole("button", { name: "Share the whole project instead" }));
		expect(await screen.findByRole("heading", { name: "Share project “acme/web”" })).toBeInTheDocument();
		await waitFor(async () => expect(await sentence()).toBe("Anyone signed in to Orchestrator.inc with the link can watch every session in this project and start their own, which run in your sandbox with your repository access and its secrets."));
		await waitFor(() => expect(mocks.client.getShareAccess).toHaveBeenCalledWith(ORG, "project", undefined));
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
