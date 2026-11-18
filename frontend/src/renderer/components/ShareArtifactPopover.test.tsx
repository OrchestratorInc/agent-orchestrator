import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudShareFile, CloudShareGrant, CloudShareGrants, CloudShareInspection, CloudShareLink, CloudShareLinks, CloudSharePublishResult } from "../../shared/cloud-share";
import { keptArtifactsQueryKey } from "../hooks/useKeptArtifacts";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { ShareArtifactButton, ShareArtifactMenuPopover, publishTitle, resetShareCachesForTests, useArtifactShareFile, useReconciledShareLinks } from "./ShareArtifactPopover";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	status: "authenticated" as string,
	userId: "u-1",
	baseUrl: "https://cp.test",
	openSettings: vi.fn(),
	capture: vi.fn(async () => true),
	noteSignIn: vi.fn(),
	clearSignIn: vi.fn(),
}));

vi.mock("../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/telemetry")>(),
	captureRendererEvent: mocks.capture,
}));
vi.mock("../lib/cloud-telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/cloud-telemetry")>(),
	noteSignInSource: mocks.noteSignIn,
	clearSignInSource: mocks.clearSignIn,
}));

vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: mocks.baseUrl } }) }));
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({
		status: mocks.status,
		session: mocks.status === "authenticated" ? { user: { id: mocks.userId, email: "aditi@acme.co", displayName: "Aditi Chauhan" } } : null,
	}),
}));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { openGlobalSettings: typeof mocks.openSettings }) => unknown) => select({ openGlobalSettings: mocks.openSettings }),
}));

const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";
/** A file on this computer: the popover sets it up and its Publish uploads it. */
const localFile = { sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
/** A cloud session's file, already in the bucket: the click publishes it. */
const cloudFile: Omit<CloudShareFile, "baseUrl" | "shareId"> = { source: "cloud", orgId: "org-1", sessionId: "cloud-1", path: "docs/report.html", projectId: "" };
const inspection: CloudShareInspection = { name: "report.html", size: 2048, unsupported: false, fingerprint: "fp-1" };
const links: CloudShareLinks = { shares: [], limit: 3, used: 1, unlimited: false, privateLinks: true };
const share: CloudShareLink = {
	id: SHARE_ID, slug: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://viewer.test/artifact/abc",
	contentType: "text/html", size: 2048, sha256: "", createdAt: "", updatedAt: "", access: "restricted", grantCount: 0,
};
const maya: CloudShareGrant = { id: "6f1c3a1e-2b4d-4c5e-8f7a-9b0c1d2e3f40", email: "maya@acme.co", createdAt: "" };

function stub(inspect: CloudShareInspection, list: CloudShareLinks, publish?: CloudSharePublishResult) {
	const inspectSpy = vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue(inspect);
	const listSpy = vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue(list);
	const publishSpy = vi.spyOn(aoBridge.cloudShare, "publish").mockResolvedValue(publish ?? { status: "cancelled" });
	const grantsSpy = vi.spyOn(aoBridge.cloudShare, "grants").mockResolvedValue({ people: [], teams: [], workspaces: [] });
	return { inspectSpy, listSpy, publishSpy, grantsSpy };
}

function renderButton(file: Omit<CloudShareFile, "baseUrl" | "shareId">, name = "report.html") {
	return render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={file} name={name} /></TooltipProvider></QueryClientProvider>);
}

/** Clicks the header button once it says `label`. */
async function openPopover(file: Omit<CloudShareFile, "baseUrl" | "shareId">, name = "report.html", label = "Publish") {
	renderButton(file, name);
	await userEvent.click(await screen.findByRole("button", { name: `${label}: ${name}` }));
	return screen.findByRole("dialog");
}

/** The panel as the artifact menu's Publish or Unpublish item opens it. */
async function openMenuPanel(file: Omit<CloudShareFile, "baseUrl" | "shareId"> = cloudFile, intent: "publish" | "unpublish" = "publish", onOpenChange = () => undefined) {
	render(
		<QueryClientProvider client={new QueryClient()}><TooltipProvider>
			<ShareArtifactMenuPopover file={file} intent={intent} name="report.html" open onOpenChange={onOpenChange}>
				<button type="button">menu</button>
			</ShareArtifactMenuPopover>
		</TooltipProvider></QueryClientProvider>,
	);
	return screen.findByRole("dialog");
}

beforeEach(() => {
	mocks.status = "authenticated";
	mocks.userId = "u-1";
	mocks.baseUrl = "https://cp.test";
	mocks.openSettings.mockReset();
	mocks.capture.mockClear();
	mocks.noteSignIn.mockReset();
	mocks.clearSignIn.mockReset();
	vi.restoreAllMocks();
	resetShareCachesForTests();
});

describe("ShareArtifactButton on a cloud session's file", () => {
	it("publishes privately on the toolbar click, with no second click, and opens on the live link", async () => {
		const { inspectSpy, listSpy, publishSpy } = stub(inspection, links, { status: "published", replaced: false, share });
		publishSpy.mockImplementation(async () => {
			// The main process records the link the file was published to.
			inspectSpy.mockResolvedValue({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" });
			listSpy.mockResolvedValue({ ...links, used: 2, shares: [share] });
			return { status: "published", replaced: false, share };
		});
		const popover = await openPopover(cloudFile);
		expect(popover).not.toHaveAttribute("aria-modal", "true");
		expect(await within(popover).findByText("Published")).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...cloudFile, baseUrl: "https://cp.test", fingerprint: "fp-1", access: "restricted" });
		// Private, nobody invited: Only you. The URL is not shown; Copy link and Unpublish are the only actions.
		expect(screen.getByText("Only you")).toBeTruthy();
		expect(screen.getByText("Nobody else can open the link. Invite people, or allow anyone with the link.")).toBeTruthy();
		expect(screen.queryByRole("textbox", { name: "Public link" })).toBeNull();
		expect(screen.getByRole("button", { name: "Copy link" })).toBeTruthy();
		expect(screen.getByRole("button", { name: "Unpublish" })).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Publish" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Republish" })).toBeNull();
		// The owner is listed.
		expect(screen.getByText("Aditi Chauhan (you)")).toBeTruthy();
		expect(screen.getByText("Owner")).toBeTruthy();
		// The toolbar button now reads Published.
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
	});

	it("says what a published page left out of the files it uses", async () => {
		stub(inspection, links, { status: "published", replaced: false, share, pageFiles: { included: 2, skipped: 3, videos: 1, unsupported: false } });
		const popover = await openPopover(cloudFile);
		await within(popover).findByText("Published");
		expect(popover.textContent).toContain("3 files this page uses weren't included, so the shared page won't show them. Videos can't be shared with a page yet.");
	});

	it("says a page's images were not included when Orchestrator Cloud cannot share them yet", async () => {
		stub(inspection, links, { status: "published", replaced: false, share, pageFiles: { included: 0, skipped: 0, videos: 0, unsupported: true } });
		const popover = await openPopover(cloudFile);
		await within(popover).findByText("Published");
		expect(popover.textContent).toContain("This page's images weren't included: Orchestrator Cloud can't share them with a page yet.");
	});

	it("says nothing more when a page included every file it uses", async () => {
		stub(inspection, links, { status: "published", replaced: false, share, pageFiles: { included: 2, skipped: 0, videos: 0, unsupported: false } });
		const popover = await openPopover(cloudFile);
		await within(popover).findByText("Published");
		expect(popover.textContent).not.toContain("weren't included");
		expect(popover.textContent).not.toContain("wasn't included");
	});

	it("names the file without its extension", async () => {
		stub(inspection, links, { status: "published", replaced: false, share });
		const popover = await openPopover(cloudFile);
		await within(popover).findByText("Published");
		expect(popover.textContent).toContain("report");
		expect(popover.textContent).not.toContain("report.html");
		expect(publishTitle("notes.v2.md")).toBe("notes.v2");
		expect(publishTitle("README")).toBe("README");
		expect(publishTitle(".env")).toBe(".env");
	});

	it("copies the viewer URL and never publishes from Copy", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		await openPopover(cloudFile, "report.html", "Published");
		await userEvent.click(await screen.findByRole("button", { name: "Copy link" }));
		expect(writeText).toHaveBeenCalledWith("https://viewer.test/artifact/abc");
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
		// Unpublish sits at the left of the action row, before Copy.
		const unpublish = screen.getByRole("button", { name: "Unpublish" });
		expect(unpublish.compareDocumentPosition(screen.getByRole("button", { name: "Copied" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	it("invites people on the live link at once, and removes them", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ people: [maya], teams: [], workspaces: [] });
		const remove = vi.spyOn(aoBridge.cloudShare, "removeGrant").mockResolvedValue();
		await openPopover(cloudFile, "report.html", "Published");
		const field = await screen.findByRole("textbox", { name: "Add people by email" });
		// Enter shares at once: there is no Add button.
		await userEvent.type(field, "Maya@acme.co{Enter}");
		expect(screen.queryByRole("button", { name: "Add" })).toBeNull();
		expect(invite).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, { emails: ["Maya@acme.co"] });
		expect(await screen.findByText("maya@acme.co")).toBeTruthy();
		expect(screen.getByText("Can view")).toBeTruthy();
		// With someone invited, Only you reads Only people invited.
		expect(screen.getByText("Only people invited")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Remove access for maya@acme.co" }));
		expect(remove).toHaveBeenCalledWith("https://cp.test", SHARE_ID, maya.id);
		await waitFor(() => expect(screen.queryByText("maya@acme.co")).toBeNull());
		expect(screen.getByText("Only you")).toBeTruthy();
	});

	it("shares nothing while an address is not an email address, and says which", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const invite = vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ people: [], teams: [], workspaces: [] });
		await openPopover(cloudFile, "report.html", "Published");
		const field = await screen.findByRole("textbox", { name: "Add people by email" });
		await userEvent.type(field, "maya@acme.co ravi@acme{Enter}");
		expect(screen.getByRole("alert").textContent).toBe("“ravi@acme” isn't an email address.");
		expect(invite).not.toHaveBeenCalled();
	});

	it("lists who the link is shared with when it opens", async () => {
		const { grantsSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, grantCount: 1 }] });
		grantsSpy.mockResolvedValue({ people: [maya], teams: [], workspaces: [] });
		await openPopover(cloudFile, "report.html", "Published");
		expect(await screen.findByText("maya@acme.co")).toBeTruthy();
		expect(grantsSpy).toHaveBeenCalledWith("https://cp.test", SHARE_ID);
	});

	it("switches access on the live link, keeping its URL, and notes what making it private does", async () => {
		const publicShare = { ...share, access: "anyone" as const };
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [publicShare] });
		const setAccess = vi.spyOn(aoBridge.cloudShare, "setAccess").mockResolvedValue({ ...publicShare, access: "restricted" });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		// A public link's button carries a globe; its name still says Published.
		await openPopover(cloudFile, "report.html", "Published");
		expect(await screen.findByText("Public. Anyone with the link can view it without signing in.")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Anyone with the link" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: /Only people invited/ }));
		expect(setAccess).toHaveBeenCalledExactlyOnceWith("https://cp.test", SHARE_ID, "restricted");
		expect(await screen.findByText("Anyone without an invite now has to sign in, and can't open it. Pages already open stop working when refreshed.")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
		expect(writeText).toHaveBeenCalledWith("https://viewer.test/artifact/abc");
	});

	it("unpublishes after an inline confirmation naming who it was shared with, then offers to publish again", async () => {
		const { inspectSpy, listSpy, publishSpy, grantsSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, grantCount: 2 }] });
		grantsSpy.mockResolvedValue({ people: [maya, { ...maya, id: "7f1c3a1e-2b4d-4c5e-8f7a-9b0c1d2e3f40", email: "dev@acme.co" }], teams: [], workspaces: [] });
		let finish: () => void = () => undefined;
		const unpublish = vi.spyOn(aoBridge.cloudShare, "unpublish").mockImplementation(() => new Promise((resolve) => {
			finish = () => {
				inspectSpy.mockResolvedValue(inspection);
				listSpy.mockResolvedValue({ ...links, used: 0, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
				resolve();
			};
		}));
		await openPopover(cloudFile, "report.html", "Published");
		await screen.findByText("dev@acme.co");
		await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
		expect(screen.getByText("The link stops working for everyone who has it, including the 2 people you invited. Pages already open stop working when refreshed.")).toBeTruthy();
		expect(screen.getByText("Publishing again makes a new link, and you'll need to invite people again.")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(unpublish).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Unpublish" }));
		await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
		// Both buttons wait while it runs.
		expect(await screen.findByRole("button", { name: "Unpublishing…" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
		finish();
		expect(await screen.findByText("The old link no longer works and the uploaded copy is deleted. Click Publish to make a new private link.")).toBeTruthy();
		expect(unpublish).toHaveBeenCalledWith("https://cp.test", SHARE_ID);
		// The toolbar is back to Publish; the note's Publish makes a new private link.
		expect(await screen.findByRole("button", { name: "Publish: report.html" })).toBeTruthy();
		publishSpy.mockResolvedValue({ status: "published", replaced: false, share: { ...share, id: "new" } });
		await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }));
		await waitFor(() => expect(publishSpy).toHaveBeenCalledWith({ ...cloudFile, baseUrl: "https://cp.test", fingerprint: "fp-1", access: "restricted" }));
	});

	it("says the link still works when unpublishing fails", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "unpublish").mockRejectedValue(new Error("network error"));
		await openPopover(cloudFile, "report.html", "Published");
		await userEvent.click(await screen.findByRole("button", { name: "Unpublish" }));
		await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
		expect((await screen.findByRole("alert")).textContent).toBe("Unpublishing failed: network error. The link still works.");
		expect(screen.getByRole("button", { name: "Copy link" })).toBeTruthy();
	});

	it("offers no Cancel while a cloud session's file is being published, and the toolbar says Publishing", async () => {
		stub(inspection, links);
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise(() => undefined));
		// A publish that never ends outlives this test: give it its own file.
		const slow = { ...cloudFile, path: "docs/slow.html" };
		await openPopover(slow, "slow.html");
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByRole("button", { name: "Publishing…" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
		// No percentage, no progress bar: a spinner and who it will open for.
		expect(screen.queryByRole("progressbar")).toBeNull();
		expect(within(dialog).getByText("Private. Nobody else can open the link until you invite people or allow anyone with the link.")).toBeTruthy();
		expect(await screen.findByRole("button", { name: "Publishing…: slow.html" })).toBeTruthy();
	});

	it("offers only Upgrade at the link limit, and uploads nothing", async () => {
		const { publishSpy } = stub(inspection, { ...links, used: 3 });
		await openPopover(cloudFile);
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to publish more.")).toBeTruthy();
		expect(screen.queryByText(/unpublish/i)).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Upgrade" }));
		expect(mocks.openSettings).toHaveBeenCalledWith("billing");
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("shows the server's limit refusal at once, even when another link has the file's name", async () => {
		stub(inspection, { ...links, used: 3, shares: [{ ...share, id: "other" }] }, { status: "limit", message: "Free plan: up to 3 active links. Upgrade to publish more." });
		await openPopover(cloudFile);
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to publish more.")).toBeTruthy();
		expect(screen.getByRole("button", { name: "Upgrade" })).toBeTruthy();
	});

	it("says only the session's creator can publish when someone else tries", async () => {
		stub(inspection, links, { status: "not_owner" });
		await openPopover(cloudFile);
		expect(await screen.findByText("Only the person who started this session can publish its files.")).toBeTruthy();
		expect(screen.queryByRole("button", { name: /^(Publish|Try again)$/ })).toBeNull();
	});

	it("opens the menu's Unpublish on its confirmation, unpublishes, and closes", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const unpublish = vi.spyOn(aoBridge.cloudShare, "unpublish").mockResolvedValue(undefined);
		const onOpenChange = vi.fn();
		await openMenuPanel(cloudFile, "unpublish", onOpenChange);
		expect(await screen.findByText("The link stops working for everyone who has it.")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
		expect(unpublish).toHaveBeenCalledWith("https://cp.test", SHARE_ID);
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("keeps the menu's Unpublish intent when Try again follows a failed check, never publishing", async () => {
		// The link was unpublished elsewhere meanwhile: a retry that forgot the intent would publish the file.
		const { inspectSpy, publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		inspectSpy.mockRejectedValueOnce(new Error("offline"));
		await openMenuPanel(cloudFile, "unpublish");
		await userEvent.click(await screen.findByRole("button", { name: "Try again" }));
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("never publishes from the menu's Unpublish when the file has no active link", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await openMenuPanel(cloudFile, "unpublish");
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("closes when a click lands in an artifact preview's frame", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		await openPopover(cloudFile, "report.html", "Published");
		await screen.findByRole("dialog");
		// A click inside a preview goes to its iframe: this document only sees its window blur.
		const frame = document.createElement("iframe");
		frame.tabIndex = 0;
		document.body.append(frame);
		frame.focus();
		window.dispatchEvent(new Event("blur"));
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		frame.remove();
	});

	it("stays open from the artifact menu when the closing menu takes focus back, and closes on a click outside", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const onOpenChange = vi.fn();
		await openMenuPanel(cloudFile, "publish", onOpenChange);
		await screen.findByText("Published");
		// The menu hands focus back to its trigger a frame after the panel opened.
		const trigger = screen.getByRole("button", { name: "menu" });
		trigger.focus();
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(onOpenChange).not.toHaveBeenCalledWith(false);
		await userEvent.click(document.body);
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
	});

	it("closes from the artifact menu once focus moves away after it opened", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const onOpenChange = vi.fn();
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider>
			<button type="button">elsewhere</button>
			<ShareArtifactMenuPopover file={cloudFile} name="report.html" open onOpenChange={onOpenChange}>
				<button type="button">menu</button>
			</ShareArtifactMenuPopover>
		</TooltipProvider></QueryClientProvider>);
		await screen.findByText("Published");
		await new Promise((resolve) => setTimeout(resolve, 450));
		// Focus going back to the menu's own trigger is still the menu's hand-off.
		screen.getByRole("button", { name: "menu" }).focus();
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(onOpenChange).not.toHaveBeenCalledWith(false);
		// Tabbing anywhere else is the person leaving.
		screen.getByRole("button", { name: "elsewhere" }).focus();
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
	});

	it("offers Republish from the artifact menu when the file changed since it was published", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, { status: "published", replaced: true, share });
		const popover = await openMenuPanel();
		await userEvent.click(await within(popover).findByRole("button", { name: "Republish" }));
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...cloudFile, baseUrl: "https://cp.test", fingerprint: "fp-1", shareId: SHARE_ID });
	});

	it("says who can view it under the email field, without promising anyone is told", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const popover = await openPopover(cloudFile, "report.html", "Published");
		expect(await within(popover).findByText("People signed in with these emails can view it.")).toBeTruthy();
	});

	it("says when who it is shared with can't be read, and reads it again on Try again", async () => {
		const { grantsSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, grantCount: 1 }] });
		grantsSpy.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce({ people: [maya], teams: [], workspaces: [] });
		const popover = await openPopover(cloudFile, "report.html", "Published");
		expect(await within(popover).findByText("Couldn't load who this link is shared with.")).toBeTruthy();
		await userEvent.click(within(popover).getByRole("button", { name: "Try again" }));
		expect(await within(popover).findByText("maya@acme.co")).toBeTruthy();
		expect(within(popover).queryByText("Couldn't load who this link is shared with.")).toBeNull();
	});

	it("never lets an older list of who it is shared with replace an invite's answer", async () => {
		const { grantsSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		let answerRead: (grants: CloudShareGrants) => void = () => undefined;
		grantsSpy.mockImplementation(() => new Promise((resolve) => { answerRead = resolve; }));
		const dev = { ...maya, id: "7f1c3a1e-2b4d-4c5e-8f7a-9b0c1d2e3f40", email: "dev@acme.co" };
		vi.spyOn(aoBridge.cloudShare, "invite").mockResolvedValue({ people: [maya, dev], teams: [], workspaces: [] });
		const popover = await openPopover(cloudFile, "report.html", "Published");
		await userEvent.type(await within(popover).findByRole("textbox", { name: "Add people by email" }), "dev@acme.co{Enter}");
		expect(await within(popover).findByText("dev@acme.co")).toBeTruthy();
		// The read started on opening lands last, without dev.
		act(() => answerRead({ people: [maya], teams: [], workspaces: [] }));
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(within(popover).getByText("dev@acme.co")).toBeTruthy();
	});

	it("opens a published file from the artifact menu without publishing again", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const popover = await openMenuPanel();
		expect(await within(popover).findByText("Published")).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
	});
});

describe("ShareArtifactButton on a file on this computer", () => {
	it("opens on its setup, uploads nothing, and publishes with the people and access chosen", async () => {
		const { publishSpy } = stub(inspection, links, { status: "published", replaced: false, share: { ...share, grantCount: 1 } });
		await openPopover(localFile);
		expect(await screen.findByText("2 of 3 free links left")).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
		// Enter adds them; people added get access when it is published.
		await userEvent.type(screen.getByRole("textbox", { name: "Add people by email" }), "dev@acme.co{Enter}");
		expect(await screen.findByText("dev@acme.co")).toBeTruthy();
		expect(screen.getByText("Gets access when you publish")).toBeTruthy();
		expect(screen.getByText("Only people invited")).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({
			...localFile, baseUrl: "https://cp.test", fingerprint: "fp-1", access: "restricted", emails: ["dev@acme.co"],
		});
		expect(await within(screen.getByRole("dialog")).findByText("Published")).toBeTruthy();
	});

	it("publishes for anyone with the link when chosen before publishing", async () => {
		const { publishSpy } = stub(inspection, links, { status: "published", replaced: false, share: { ...share, access: "anyone" } });
		await openPopover(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Only you" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: /Anyone with the link/ }));
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...localFile, baseUrl: "https://cp.test", fingerprint: "fp-1", access: "anyone" });
	});

	it("keeps who it is for, as chosen, when a fresher link list decides the panel again", async () => {
		// The button reads the list for a link since unpublished, so the panel opens on that cached list.
		const revoked = { ...share, revokedAt: "2026-11-01T00:00:00Z" };
		const { listSpy, publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [revoked] }, { status: "published", replaced: false, share: { ...share, access: "anyone" } });
		renderButton(localFile);
		const button = await screen.findByRole("button", { name: "Publish: report.html" });
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(1));
		let answer: (value: CloudShareLinks) => void = () => undefined;
		listSpy.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		await userEvent.click(button);
		await userEvent.click(await screen.findByRole("button", { name: "Only you" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: /Anyone with the link/ }));
		// The background read answers late, with another link used since: the panel is decided again.
		await act(async () => answer({ ...links, used: 2, shares: [revoked] }));
		expect(screen.getByRole("button", { name: "Anyone with the link" })).toBeTruthy();
		expect(screen.getByText("1 of 3 free links left")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...localFile, baseUrl: "https://cp.test", fingerprint: "fp-1", access: "anyone" });
	});

	it("never lets the opening's background list read undo a publish that answered changed", async () => {
		// Opened on the list the button read; the refresh behind it answers late.
		const revoked = { ...share, revokedAt: "2026-11-01T00:00:00Z" };
		const { listSpy, publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [revoked] }, {
			status: "changed",
			inspection: { ...inspection, shareId: SHARE_ID, fingerprint: "fp-2" },
		});
		renderButton(localFile);
		const button = await screen.findByRole("button", { name: "Publish: report.html" });
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(1));
		let answer: (value: CloudShareLinks) => void = () => undefined;
		listSpy.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		await userEvent.click(button);
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByText("This file changed while it was being published. Check it and publish again.")).toBeTruthy();
		// The late answer differs, but the publish's answer stands.
		await act(async () => answer({ ...links, used: 2, shares: [revoked] }));
		expect(screen.getByText("This file changed while it was being published. Check it and publish again.")).toBeTruthy();
		publishSpy.mockResolvedValue({ status: "published", replaced: false, share });
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenLastCalledWith(expect.objectContaining({ fingerprint: "fp-2" }));
	});

	it("says Publishing while it uploads, with Cancel, and keeps going when the popover closes", async () => {
		stub(inspection, links);
		let finish: (result: CloudSharePublishResult) => void = () => undefined;
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		const cancel = vi.spyOn(aoBridge.cloudShare, "cancel").mockResolvedValue();
		const long = { ...localFile, path: "docs/long.html" };
		await openPopover(long, "long.html");
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		const dialog = screen.getByRole("dialog");
		expect(await within(dialog).findByRole("button", { name: "Publishing…" })).toBeDisabled();
		expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeTruthy();
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		expect(cancel).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Publishing…: long.html" }));
		expect(await within(screen.getByRole("dialog")).findByRole("button", { name: "Publishing…" })).toBeDisabled();
		finish({ status: "published", replaced: false, share });
		expect(await within(screen.getByRole("dialog")).findByText("Published")).toBeTruthy();
	});

	it("returns to its setup, without publishing again, when the upload is cancelled", async () => {
		const { publishSpy } = stub(inspection, links, { status: "cancelled" });
		await openPopover(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledTimes(1);
	});

	it("shows the file again when it changed while publishing, and publishes its newest version", async () => {
		const { publishSpy } = stub(inspection, links, {
			status: "changed",
			inspection: { ...inspection, size: 4096, fingerprint: "fp-2" },
		});
		await openPopover(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByText("This file changed while it was being published. Check it and publish again.")).toBeTruthy();
		publishSpy.mockResolvedValue({ status: "published", replaced: false, share });
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenLastCalledWith({ ...localFile, baseUrl: "https://cp.test", fingerprint: "fp-2", access: "restricted" });
	});

	it("refuses more people than one publish can share it with, before uploading", async () => {
		const { publishSpy } = stub(inspection, links);
		const popover = await openPopover(localFile);
		const field = await within(popover).findByRole("textbox", { name: "Add people by email" });
		const emails = Array.from({ length: 50 }, (_, index) => `p${index}@acme.co`).join(", ");
		await userEvent.click(field);
		await userEvent.paste(emails);
		await userEvent.keyboard("{Enter}");
		await userEvent.type(field, "one-more@acme.co{Enter}");
		expect(await within(popover).findByText("Add up to 50 people before publishing. You can add more once it's published.")).toBeTruthy();
		// Not taken: it stays typed.
		expect((field as HTMLInputElement).value).toBe("one-more@acme.co");
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("says a file's link was unpublished elsewhere, and publishing gives it a new one", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await openPopover(localFile);
		expect(await screen.findByText("This file's link was unpublished. Publishing gives it a new link.")).toBeTruthy();
	});
});

describe("ShareArtifactButton once the file changed", () => {
	it("shows one Republish button, with no separate access button, and it puts the newer version on the same link", async () => {
		const { inspectSpy, publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, { status: "published", replaced: true, share });
		publishSpy.mockImplementation(async () => {
			inspectSpy.mockResolvedValue({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" });
			return { status: "published", replaced: true, share };
		});
		const { container } = renderButton(cloudFile);
		const republish = await screen.findByRole("button", { name: "Republish: report.html" });
		expect(container.querySelector("[data-share-split]")).toBeNull();
		expect(screen.queryByRole("button", { name: "Access and people: report.html" })).toBeNull();
		await userEvent.click(republish);
		await waitFor(() => expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...cloudFile, baseUrl: "https://cp.test", fingerprint: "fp-1", shareId: SHARE_ID }));
		// The popover opens on the published link, and never offers Republish itself.
		expect(await within(screen.getByRole("dialog")).findByText("Published")).toBeTruthy();
		expect(within(screen.getByRole("dialog")).queryByRole("button", { name: "Republish" })).toBeNull();
		// Once republished, the button checks again and is back to Published.
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
	});

	it("says Republish after a pure rename, and one click updates the same link under the new name", async () => {
		const renamed = { ...localFile, path: "docs/final.html", artifactId: "art-1" };
		const { publishSpy } = stub(
			{ ...inspection, name: "final.html", shareId: SHARE_ID, publishedFingerprint: inspection.fingerprint },
			{ ...links, shares: [share] },
			{ status: "published", replaced: true, share: { ...share, name: "final.html" } },
		);
		renderButton(renamed, "final.html");
		await userEvent.click(await screen.findByRole("button", { name: "Republish: final.html" }));
		await waitFor(() => expect(publishSpy).toHaveBeenCalledExactlyOnceWith({
			...renamed, baseUrl: "https://cp.test", fingerprint: inspection.fingerprint, shareId: SHARE_ID,
		}));
	});

	it("stays on the link, with the newest version to republish, when the file changes during a republish", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [{ ...share, access: "anyone" }] }, {
			status: "changed",
			inspection: { ...inspection, shareId: SHARE_ID, fingerprint: "fp-2" },
		});
		renderButton(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Republish: report.html" }));
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByText("This file changed while it was being published. Check it and publish again.")).toBeTruthy();
		publishSpy.mockResolvedValue({ status: "published", replaced: true, share });
		await userEvent.click(within(dialog).getByRole("button", { name: "Republish" }));
		expect(publishSpy).toHaveBeenLastCalledWith({ ...localFile, baseUrl: "https://cp.test", fingerprint: "fp-2", shareId: SHARE_ID });
	});

	it("keeps offering Republish, without republishing again, after a cancelled republish", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, { status: "cancelled" });
		renderButton(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Republish: report.html" }));
		const dialog = await screen.findByRole("dialog");
		expect(await within(dialog).findByRole("button", { name: "Republish" })).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledTimes(1);
		expect(within(dialog).getByRole("button", { name: "Unpublish" })).toBeTruthy();
	});

	it("shows nothing published for another file until that file is checked", async () => {
		const { inspectSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const client = new QueryClient();
		const view = render(<QueryClientProvider client={client}><TooltipProvider><ShareArtifactButton file={cloudFile} name="report.html" /></TooltipProvider></QueryClientProvider>);
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
		inspectSpy.mockReturnValue(new Promise(() => undefined));
		const other = { ...cloudFile, path: "docs/other.html" };
		view.rerender(<QueryClientProvider client={client}><TooltipProvider><ShareArtifactButton file={other} name="other.html" /></TooltipProvider></QueryClientProvider>);
		expect(await screen.findByRole("button", { name: "Publish: other.html" })).toBeTruthy();
	});

	it("says Published, not Republish, while the file is unchanged", async () => {
		stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" }, { ...links, shares: [share] });
		renderButton(cloudFile);
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Republish: report.html" })).toBeNull();
	});

	it("says Publish, never Republish, for a link that was unpublished", async () => {
		const { inspectSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		renderButton(cloudFile);
		await waitFor(() => expect(inspectSpy).toHaveBeenCalled());
		expect(await screen.findByRole("button", { name: "Publish: report.html" })).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Republish: report.html" })).toBeNull();
	});
});

describe("ShareArtifactButton where links are public only", () => {
	// An older control plane, or one whose viewer has no sign-in.
	const publicOnly: CloudShareLinks = { ...links, privateLinks: false };

	it("publishes as before, sending no access, and offers no invites", async () => {
		const { publishSpy } = stub(inspection, publicOnly, { status: "published", replaced: false, share: { ...share, access: undefined, grantCount: undefined } });
		await openPopover(cloudFile);
		expect(await within(screen.getByRole("dialog")).findByText("Published")).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...cloudFile, baseUrl: "https://cp.test", fingerprint: "fp-1" });
		expect(screen.getByText("Anyone with the link")).toBeTruthy();
		expect(screen.queryByRole("textbox", { name: "Add people by email" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Anyone with the link" })).toBeNull();
	});

	it("sets up a file on this computer with only Publish", async () => {
		const { publishSpy } = stub(inspection, publicOnly, { status: "published", replaced: false, share });
		await openPopover(localFile);
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...localFile, baseUrl: "https://cp.test", fingerprint: "fp-1" });
	});
});

describe("ShareArtifactButton states", () => {
	it("asks a signed-out person to sign in", async () => {
		mocks.status = "unauthenticated";
		const signIn = vi.spyOn(aoBridge.cloud, "signIn").mockResolvedValue();
		await openPopover(cloudFile);
		await userEvent.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(signIn).toHaveBeenCalled();
	});

	it("lets a paid plan through at the limit, for a file of any size", async () => {
		stub({ ...inspection, size: 5 * 1024 ** 3 }, { ...links, used: 3, unlimited: true }, { status: "published", replaced: false, share });
		await openPopover(cloudFile);
		expect(await within(screen.getByRole("dialog")).findByText("Published")).toBeTruthy();
		expect(screen.queryByText(/free links left/)).toBeNull();
	});

	it("is not offered where the control plane does not serve share links", async () => {
		mocks.baseUrl = "https://no-shares.test";
		const available = vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(false);
		renderButton(localFile);
		await waitFor(() => expect(available).toHaveBeenCalledWith("https://no-shares.test"));
		expect(screen.queryByRole("button", { name: /^Publish/ })).toBeNull();
		// The answer is kept: a second button does not ask again.
		renderButton({ ...localFile, path: "b.html" }, "b.html");
		expect(available).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("button", { name: /^Publish/ })).toBeNull();
	});

	it("shows a refusal's message, without Electron's IPC wrapper", async () => {
		stub(inspection, links, { status: "error", message: "There is not enough free disk space to prepare this file for sharing." });
		await openPopover(cloudFile);
		expect(await screen.findByText("Publishing failed: There is not enough free disk space to prepare this file for sharing.")).toBeTruthy();
		vi.spyOn(aoBridge.cloudShare, "inspect").mockRejectedValue(new Error("Error invoking remote method 'cloudShare:inspect': CloudShareError: The file is no longer available."));
		await userEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(await screen.findByText("Publishing failed: The file is no longer available.")).toBeTruthy();
	});

	it("opens on the link list the button already read, without waiting for another", async () => {
		const { listSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		renderButton(localFile);
		const button = await screen.findByRole("button", { name: "Published: report.html" });
		expect(listSpy).toHaveBeenCalledTimes(1);
		// The control plane is slow now: the panel still opens on its link at once.
		let answer: (value: CloudShareLinks) => void = () => undefined;
		listSpy.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
		await userEvent.click(button);
		expect(await screen.findByRole("button", { name: "Copy link" })).toBeTruthy();
		expect(screen.queryByText("Checking the file…")).toBeNull();
		// The list is read again behind it; an unchanged answer changes nothing.
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(2));
		await act(async () => answer({ ...links, shares: [share] }));
		expect(screen.getByRole("button", { name: "Copy link" })).toBeTruthy();
	});

	it("opens again on the fresh list when the cached one was out of date", async () => {
		const { listSpy, publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		renderButton(localFile);
		const button = await screen.findByRole("button", { name: "Published: report.html" });
		// Unpublished somewhere else since the button read the list.
		listSpy.mockResolvedValue({ ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await userEvent.click(button);
		expect(await screen.findByText("This file's link was unpublished. Publishing gives it a new link.")).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Copy link" })).toBeNull();
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("checks the file once to draw the button and once when Publish opens, and nothing on hover", async () => {
		const { inspectSpy } = stub(inspection, links);
		renderButton(localFile);
		const button = await screen.findByRole("button", { name: "Publish: report.html" });
		await waitFor(() => expect(inspectSpy).toHaveBeenCalledTimes(1));
		await userEvent.hover(button);
		expect(inspectSpy).toHaveBeenCalledTimes(1);
		await userEvent.click(button);
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(inspectSpy).toHaveBeenCalledTimes(2);
	});

	it("names a cloud file's version by the SHA-256 its list gave, and shows Republish when it changed", async () => {
		const { inspectSpy } = stub({ ...inspection, shareId: SHARE_ID, fingerprint: "sha-2", publishedFingerprint: "sha-1" }, { ...links, shares: [share] });
		renderButton({ ...cloudFile, fingerprint: "sha-2" });
		expect(await screen.findByRole("button", { name: "Republish: report.html" })).toBeTruthy();
		expect(inspectSpy).toHaveBeenCalledWith(expect.objectContaining({ source: "cloud", fingerprint: "sha-2" }));
	});

	it("treats a fingerprint recorded with a content hash by an older version as the same size and time", async () => {
		stub({ ...inspection, shareId: SHARE_ID, fingerprint: "2048:1700000000000", publishedFingerprint: "abc123:2048:1700000000000" }, { ...links, shares: [share] });
		renderButton(localFile);
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
	});

	it.each(["restricted", "anyone"] as const)("keeps the Publish icon on a %s link's Published button", async (access) => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, access }] });
		renderButton(localFile);
		const button = await screen.findByRole("button", { name: "Published: report.html" });
		// Who the link opens for is in the popover, not on the button.
		expect(button.querySelector("svg")).toHaveClass("icon-share");
		expect(button.querySelector(".icon-globe, .icon-link, .icon-lock")).toBeNull();
	});

	it("reads Published at once for a file the artifact list has published, before its check answers", async () => {
		const { inspectSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		inspectSpy.mockReturnValue(new Promise(() => undefined));
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={localFile} name="report.html" published /></TooltipProvider></QueryClientProvider>);
		const button = await screen.findByRole("button", { name: "Published: report.html" });
		expect(button.querySelector("svg")).toHaveClass("icon-share");
	});

	it("never reads one account's cached links after signing out and in again", async () => {
		const { listSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const first = renderButton(localFile);
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
		// Signed out: the mounted button sees it, and the cached list is dropped.
		mocks.status = "unauthenticated";
		first.rerender(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={localFile} name="report.html" /></TooltipProvider></QueryClientProvider>);
		first.unmount();
		// Signed in again; this time the list has no links.
		mocks.status = "authenticated";
		listSpy.mockResolvedValue({ ...links, shares: [] });
		renderButton(localFile);
		expect(await screen.findByRole("button", { name: "Publish: report.html" })).toBeTruthy();
		expect(listSpy).toHaveBeenCalledTimes(2);
	});

	it("never reads another account's cached links, even without seeing a sign-out", async () => {
		const { listSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const first = renderButton(localFile);
		expect(await screen.findByRole("button", { name: "Published: report.html" })).toBeTruthy();
		first.unmount();
		mocks.userId = "u-2";
		listSpy.mockResolvedValue({ ...links, shares: [] });
		renderButton(localFile);
		expect(await screen.findByRole("button", { name: "Publish: report.html" })).toBeTruthy();
		expect(listSpy).toHaveBeenCalledTimes(2);
	});

	it("drops a list read started for an account that signed out before it answered", async () => {
		const { listSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		let answer: (value: CloudShareLinks) => void = () => undefined;
		listSpy.mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));
		const first = renderButton(localFile);
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(1));
		mocks.status = "unauthenticated";
		first.rerender(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={localFile} name="report.html" /></TooltipProvider></QueryClientProvider>);
		// The old read answers after the sign-out: it is not kept for the next sign-in.
		await act(async () => answer({ ...links, shares: [share] }));
		first.unmount();
		mocks.status = "authenticated";
		const inspectSpy = vi.mocked(aoBridge.cloudShare.inspect);
		const checks = inspectSpy.mock.calls.length;
		let empty: (value: CloudShareLinks) => void = () => undefined;
		listSpy.mockReturnValue(new Promise((resolve) => { empty = resolve; }));
		renderButton(localFile);
		// The new button's check runs; it must ask for the list, not reuse the old answer.
		await waitFor(() => expect(inspectSpy.mock.calls.length).toBeGreaterThan(checks));
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(2));
		await act(async () => empty({ ...links, shares: [] }));
		expect(screen.getByRole("button", { name: "Publish: report.html" })).toBeTruthy();
	});

	it("disables Publish for a video", async () => {
		renderButton({ ...localFile, path: "clip.mp4" }, "clip.mp4");
		expect(await screen.findByRole("button", { name: "Publish: clip.mp4" })).toBeDisabled();
	});
});

describe("useArtifactShareFile", () => {
	it("shares a file by its artifact ID, which a rename keeps, so the renamed file finds its link", () => {
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => useArtifactShareFile({ id: "art-1", sessionId: "sess-1", path: "final/report.html", projectId: "proj-1" }), { wrapper });
		expect(result.current).toEqual({ sessionId: "sess-1", path: "final/report.html", projectId: "proj-1", artifactId: "art-1" });
		const untracked = renderHook(() => useArtifactShareFile({ sessionId: "sess-1", path: "notes.md", projectId: "proj-1" }), { wrapper });
		expect(untracked.result.current).not.toHaveProperty("artifactId");
	});

	it("names a remote host's file by its host, so it is downloaded from there", () => {
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => useArtifactShareFile({ id: "art-1", hostId: "https://h-vm.useao.dev", sessionId: "sess-1", path: "deck.html", projectId: "proj-1" }), { wrapper });
		expect(result.current).toEqual({ source: "host", hostId: "https://h-vm.useao.dev", sessionId: "sess-1", path: "deck.html", projectId: "proj-1", artifactId: "art-1" });
	});
});

describe("ShareArtifactButton on a kept artifact", () => {
	it("reads the kept list again on publish, so a mounted page shows the link", async () => {
		const { inspectSpy, listSpy, publishSpy } = stub(inspection, links);
		let keptShareUrl = "";
		publishSpy.mockImplementation(async () => {
			inspectSpy.mockResolvedValue({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" });
			listSpy.mockResolvedValue({ ...links, used: 2, shares: [share] });
			keptShareUrl = share.viewerUrl ?? "";
			return { status: "published", replaced: false, share };
		});
		// Stands in for the Artifacts page's kept rows, which take shareUrl from this list.
		function KeptShareUrl() {
			const { data } = useQuery({ queryKey: keptArtifactsQueryKey, queryFn: async () => keptShareUrl });
			return <output aria-label="kept link">{data || "none"}</output>;
		}
		render(
			<QueryClientProvider client={new QueryClient()}><TooltipProvider>
				<KeptShareUrl />
				<ShareArtifactButton file={localFile} name="report.html" />
			</TooltipProvider></QueryClientProvider>,
		);
		expect(await screen.findByText("none")).toBeTruthy();
		await userEvent.click(await screen.findByRole("button", { name: "Publish: report.html" }));
		const popover = await screen.findByRole("dialog");
		const publish = within(popover).queryByRole("button", { name: "Publish" });
		if (publish) await userEvent.click(publish);
		await waitFor(() => expect(screen.getByLabelText("kept link").textContent).toBe(share.viewerUrl ?? ""));
	});
});

describe("useReconciledShareLinks", () => {
	const renderReconcile = () => {
		const client = new QueryClient();
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		renderHook(() => useReconciledShareLinks(), { wrapper });
		return invalidate;
	};

	it("reconciles the publish records with the link list, then reads the artifacts again", async () => {
		mocks.baseUrl = "https://reconcile.test";
		vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(true);
		const list = vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		const invalidate = renderReconcile();
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKeyForHost() }));
		// Kept artifacts' links come from their own list.
		expect(invalidate).toHaveBeenCalledWith({ queryKey: keptArtifactsQueryKey });
		expect(list).toHaveBeenCalledExactlyOnceWith("https://reconcile.test");
	});

	it("returns who each active link opens for, by the URL a published artifact carries", async () => {
		mocks.baseUrl = "https://reconcile-access.test";
		vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(true);
		const publicShare = { ...share, id: "p-1", viewerUrl: undefined, contentUrl: "https://pub.share.test/", access: undefined };
		const revoked = { ...share, id: "r-1", viewerUrl: "https://viewer.test/artifact/gone", revokedAt: "2026-11-01T00:00:00Z" };
		vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share, publicShare, revoked] });
		const client = new QueryClient();
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const { result } = renderHook(() => useReconciledShareLinks(), { wrapper });
		await waitFor(() => expect(result.current.size).toBe(2));
		expect(result.current.get("https://viewer.test/artifact/abc")).toBe("restricted");
		expect(result.current.get("https://pub.share.test/")).toBe("anyone");
	});

	it("shares one list read between surfaces mounted together", async () => {
		mocks.baseUrl = "https://reconcile-shared.test";
		vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(true);
		const list = vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue({ ...links, shares: [share] });
		const client = new QueryClient();
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
		const first = renderHook(() => useReconciledShareLinks(), { wrapper });
		const second = renderHook(() => useReconciledShareLinks(), { wrapper });
		await waitFor(() => expect(first.result.current.size).toBe(1));
		expect(second.result.current.get("https://viewer.test/artifact/abc")).toBe("restricted");
		expect(list).toHaveBeenCalledTimes(1);
		// Once each for the artifact lists, kept artifacts, and the links of remote hosts' files.
		expect(invalidate).toHaveBeenCalledTimes(3);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["host-share-links"] });
	});

	it("asks for nothing while signed out", async () => {
		mocks.baseUrl = "https://reconcile-signed-out.test";
		mocks.status = "unauthenticated";
		const available = vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(true);
		const list = vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue(links);
		const invalidate = renderReconcile();
		await waitFor(() => expect(available).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(list).not.toHaveBeenCalled();
		expect(invalidate).not.toHaveBeenCalled();
	});
});

describe("publish telemetry", () => {
	/** Every capture of event, by its properties. */
	const sent = (event: string) => mocks.capture.mock.calls.filter((call) => (call as unknown[])[0] === event).map((call) => (call as unknown[])[1]);

	it("counts the Publish click once and the link it made, then a copy, with no file details", async () => {
		stub(inspection, links, { status: "published", replaced: false, share });
		vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		const popover = await openPopover(cloudFile);
		await within(popover).findByText("Published");
		await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
		await screen.findByRole("button", { name: "Copied" });
		expect(sent("ao.artifacts.publish_clicked")).toEqual([{ already_published: false, signed_in: true }]);
		expect(sent("ao.artifacts.published")).toEqual([{ is_republish: false }]);
		expect(sent("ao.artifacts.link_copied")).toEqual([{ surface: "panel" }]);
		expect(JSON.stringify(mocks.capture.mock.calls)).not.toMatch(/report|abc|viewer\.test|sess-|cloud-1/);
	});

	it("counts the sign-in prompt and the Sign in click, and forgets Publish's sign-in when the popover closes", async () => {
		mocks.status = "unauthenticated";
		vi.spyOn(aoBridge.cloud, "signIn").mockResolvedValue();
		const user = userEvent.setup();
		renderButton(cloudFile);
		const trigger = await screen.findByRole("button", { name: "Publish: report.html" });
		await user.click(trigger);
		await user.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(sent("ao.artifacts.publish_clicked")).toEqual([{ already_published: false, signed_in: false }]);
		expect(sent("ao.artifacts.sign_in_prompt_shown")).toEqual([{ reason: "signed_out" }]);
		expect(sent("ao.artifacts.sign_in_clicked")).toHaveLength(1);
		expect(mocks.noteSignIn).toHaveBeenCalledExactlyOnceWith("artifact_publish");
		expect(mocks.clearSignIn).not.toHaveBeenCalled();
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		expect(mocks.clearSignIn).toHaveBeenCalledWith("artifact_publish");
	});

	it("counts a publish that came back signed out as a failure and an expired session", async () => {
		stub(inspection, links, { status: "signed_out" });
		await openPopover(cloudFile);
		await screen.findByRole("button", { name: "Sign in" });
		expect(sent("ao.artifacts.publish_failed")).toEqual([{ is_republish: false, reason: "signed_out" }]);
		expect(sent("ao.artifacts.sign_in_prompt_shown")).toEqual([{ reason: "session_expired" }]);
	});

	it("counts a refusal the panel opens on once, as blocked rather than failed", async () => {
		const { publishSpy } = stub(inspection, { ...links, used: 3 });
		await openPopover(cloudFile);
		await waitFor(() => expect(sent("ao.artifacts.publish_blocked")).toEqual([{ reason: "limit", is_republish: false }]));
		expect(sent("ao.artifacts.publish_failed")).toEqual([]);
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("counts an unsupported file that already has a live link as a blocked republish", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, unsupported: true }, { ...links, shares: [share] });
		await openPopover(cloudFile, "report.html", "Published");
		await waitFor(() => expect(sent("ao.artifacts.publish_blocked")).toEqual([{ reason: "unsupported", is_republish: true }]));
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("counts a cancelled upload as cancelled, not failed", async () => {
		stub(inspection, links, { status: "cancelled" });
		await openPopover(cloudFile);
		await waitFor(() => expect(sent("ao.artifacts.publish_cancelled")).toEqual([{ is_republish: false }]));
		expect(sent("ao.artifacts.publish_failed")).toEqual([]);
	});

	it("counts an unpublish, and does not count its confirmation as a Publish click", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		vi.spyOn(aoBridge.cloudShare, "unpublish").mockResolvedValue();
		const onOpenChange = vi.fn();
		await openMenuPanel(cloudFile, "unpublish", onOpenChange);
		await userEvent.click(await screen.findByRole("button", { name: "Unpublish" }));
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		expect(sent("ao.artifacts.unpublished")).toEqual([undefined]);
		expect(sent("ao.artifacts.publish_clicked")).toEqual([]);
	});
});
