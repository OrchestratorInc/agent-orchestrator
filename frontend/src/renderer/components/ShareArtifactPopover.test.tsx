import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudShareInspection, CloudShareLink, CloudShareLinks, CloudSharePublishResult } from "../../shared/cloud-share";
import { aoBridge } from "../lib/bridge";
import { ShareArtifactButton } from "./ShareArtifactPopover";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	status: "authenticated" as string,
	baseUrl: "https://cp.test",
	openSettings: vi.fn(),
}));

vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: mocks.baseUrl } }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: mocks.status, session: null }) }));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { openGlobalSettings: typeof mocks.openSettings }) => unknown) => select({ openGlobalSettings: mocks.openSettings }),
}));

const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";
const file = { sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
const inspection: CloudShareInspection = { name: "report.html", size: 2048, secretFindings: [], unsupported: false, fingerprint: "fp-1" };
const links: CloudShareLinks = { shares: [], limit: 3, used: 1, unlimited: false };
const share: CloudShareLink = {
	id: SHARE_ID, slug: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://viewer.test/artifact/abc",
	contentType: "text/html", size: 2048, sha256: "", createdAt: "", updatedAt: "",
};

function stub(inspect: CloudShareInspection, list: CloudShareLinks, publish?: CloudSharePublishResult) {
	const inspectSpy = vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue(inspect);
	const listSpy = vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue(list);
	const publishSpy = vi.spyOn(aoBridge.cloudShare, "publish").mockResolvedValue(publish ?? { status: "cancelled" });
	return { inspectSpy, listSpy, publishSpy };
}

async function openPopover(path = file.path, name = "report.html") {
	render(<TooltipProvider><ShareArtifactButton file={{ ...file, path }} name={name} /></TooltipProvider>);
	await userEvent.click(await screen.findByRole("button", { name: `Share: ${name}` }));
	return screen.findByRole("dialog");
}

beforeEach(() => {
	mocks.status = "authenticated";
	mocks.baseUrl = "https://cp.test";
	mocks.openSettings.mockReset();
	vi.restoreAllMocks();
});

describe("ShareArtifactButton", () => {
	it("opens a popover anchored to the button, not a modal", async () => {
		stub(inspection, links);
		const popover = await openPopover();
		expect(popover).not.toHaveAttribute("aria-modal", "true");
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
	});

	it("asks a signed-out person to sign in", async () => {
		mocks.status = "unauthenticated";
		const signIn = vi.spyOn(aoBridge.cloud, "signIn").mockResolvedValue();
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(signIn).toHaveBeenCalled();
	});

	it("shows the plan usage beside Publish and any credential found, but no status before publishing", async () => {
		stub({ ...inspection, secretFindings: ["AWS access key"] }, links);
		await openPopover();
		expect(await screen.findByText("2 of 3 free links left")).toBeTruthy();
		expect(screen.getByRole("alert").textContent).toContain("AWS access key");
		expect(screen.queryByText(/anyone with the link/i)).toBeNull();
		expect(screen.queryByText("2.0 KB")).toBeNull();
	});

	it("publishes on Publish and offers the link to copy", async () => {
		const { publishSpy } = stub(inspection, links, { status: "published", replaced: false, share });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		// The publish names the inspection it was shown.
		expect(publishSpy).toHaveBeenCalledWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-1" });
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
		expect(writeText).toHaveBeenCalledWith("https://viewer.test/artifact/abc");
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
		// Once published, it says who can open it.
		expect(screen.getByText("Public · anyone with the link can view")).toBeTruthy();
		// Unpublish sits at the left of the action row, before Copy.
		const unpublish = screen.getByRole("button", { name: "Unpublish" });
		expect(unpublish.compareDocumentPosition(screen.getByRole("button", { name: "Copied" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	it("opens a published file to its link, with only Copy and Unpublish", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		await openPopover();
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		expect(screen.getByRole("button", { name: "Unpublish" })).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Publish" })).toBeNull();
		expect(screen.queryByRole("button", { name: /update/i })).toBeNull();
	});

	it("says when the file changed since it was published, and the link shows the earlier version", async () => {
		stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] });
		await openPopover();
		expect(await screen.findByText("This file changed since you published it. The link still shows the earlier version.")).toBeTruthy();
		// It stays through an Unpublish that is cancelled.
		await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(await screen.findByText("This file changed since you published it. The link still shows the earlier version.")).toBeTruthy();
	});

	it("says nothing more when the published file is unchanged", async () => {
		stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" }, { ...links, shares: [share] });
		await openPopover();
		expect(await screen.findByRole("textbox", { name: "Public link" })).toBeTruthy();
		expect(screen.queryByText(/changed since you published/)).toBeNull();
	});

	it("says Publishing while the file uploads", async () => {
		stub(inspection, links);
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise(() => undefined));
		// A publish that never ends outlives this test: give it its own file.
		await openPopover("docs/slow.html");
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByRole("button", { name: "Publishing…" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
		// The progress is the row's left side, and the bar.
		expect(screen.getByText("0% uploaded")).toBeTruthy();
		expect(screen.getByRole("progressbar", { name: "0% uploaded" })).toHaveAttribute("aria-valuenow", "0");
	});

	it("unpublishes after an inline confirmation, then offers to publish again", async () => {
		const { inspectSpy, listSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const unpublish = vi.spyOn(aoBridge.cloudShare, "unpublish").mockImplementation(async () => {
			inspectSpy.mockResolvedValue(inspection);
			listSpy.mockResolvedValue({ ...links, used: 0, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		});
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Unpublish" }));
		expect(screen.getByText("The link stops working for everyone who has it.")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(unpublish).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Unpublish" }));
		await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
		expect(unpublish).toHaveBeenCalledWith("https://cp.test", SHARE_ID);
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(screen.getByText("3 of 3 free links left")).toBeTruthy();
	});

	it("treats a record of an unpublished link as unpublished, and says so", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(screen.getByText("This file's link was unpublished. Publishing gives it a new link.")).toBeTruthy();
	});

	it("says nothing about an earlier link for a file never published from here", async () => {
		stub(inspection, links);
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(screen.queryByText(/was unpublished/)).toBeNull();
	});

	it("offers only Upgrade at the link limit, never unpublishing", async () => {
		stub(inspection, { ...links, used: 3 });
		await openPopover();
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to share more.")).toBeTruthy();
		expect(screen.queryByText(/unpublish/i)).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Upgrade" }));
		expect(mocks.openSettings).toHaveBeenCalledWith("billing");
	});

	it("lets a paid plan through at the limit, for a file of any size", async () => {
		stub({ ...inspection, size: 5 * 1024 ** 3 }, { ...links, used: 3, unlimited: true });
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		// A paid plan has no count to show.
		expect(screen.queryByText(/free links left/)).toBeNull();
	});

	it("shows the server's limit refusal at once, even when another link has the file's name", async () => {
		stub(inspection, { ...links, used: 3, shares: [{ ...share, id: "other" }] }, { status: "limit", message: "Free plan: up to 3 active links. Upgrade to share more." });
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to share more.")).toBeTruthy();
		expect(screen.getByRole("button", { name: "Upgrade" })).toBeTruthy();
	});

	it("shows the file again, with what it now holds, when it changed since Share opened", async () => {
		const { publishSpy } = stub(inspection, links, {
			status: "changed",
			inspection: { ...inspection, size: 4096, secretFindings: ["GitHub token"], fingerprint: "fp-2" },
		});
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByText("This file changed since you opened Share. Check it and publish again.")).toBeTruthy();
		expect(screen.getByRole("alert").textContent).toContain("GitHub token");
		expect(screen.getByText("2 of 3 free links left")).toBeTruthy();
		publishSpy.mockResolvedValue({ status: "published", replaced: false, share });
		await userEvent.click(screen.getByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenLastCalledWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-2" });
	});

	it("keeps publishing when the popover closes, and shows it again when Share reopens", async () => {
		stub(inspection, links);
		let finish: (result: CloudSharePublishResult) => void = () => undefined;
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		const cancel = vi.spyOn(aoBridge.cloudShare, "cancel").mockResolvedValue();
		await openPopover("docs/long.html", "long.html");
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		await screen.findByRole("button", { name: "Publishing…" });
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		expect(cancel).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Share: long.html" }));
		expect(await screen.findByRole("button", { name: "Publishing…" })).toBeDisabled();
		finish({ status: "published", replaced: false, share });
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
	});

	it("is not offered where the control plane does not serve share links", async () => {
		mocks.baseUrl = "https://no-shares.test";
		const available = vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(false);
		render(<TooltipProvider><ShareArtifactButton file={file} name="report.html" /></TooltipProvider>);
		await waitFor(() => expect(available).toHaveBeenCalledWith("https://no-shares.test"));
		expect(screen.queryByRole("button", { name: /^Share/ })).toBeNull();
		// The answer is kept: a second button does not ask again.
		render(<TooltipProvider><ShareArtifactButton file={{ ...file, path: "b.html" }} name="b.html" /></TooltipProvider>);
		expect(available).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("button", { name: /^Share/ })).toBeNull();
	});

	it("shows a refusal's message, without Electron's IPC wrapper", async () => {
		stub(inspection, links, { status: "error", message: "There is not enough free disk space to prepare this file for sharing." });
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByText("Sharing failed: There is not enough free disk space to prepare this file for sharing.")).toBeTruthy();
		vi.spyOn(aoBridge.cloudShare, "inspect").mockRejectedValue(new Error("Error invoking remote method 'cloudShare:inspect': CloudShareError: The file is no longer available."));
		await userEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(await screen.findByText("Sharing failed: The file is no longer available.")).toBeTruthy();
	});

	it("offers no Cancel while a cloud session's file is being shared", async () => {
		stub(inspection, links);
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise(() => undefined));
		render(<TooltipProvider><ShareArtifactButton file={{ source: "cloud", orgId: "org-1", sessionId: "cloud-1", path: "docs/cloud.html", projectId: "" }} name="cloud.html" /></TooltipProvider>);
		await userEvent.click(await screen.findByRole("button", { name: "Share: cloud.html" }));
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByRole("button", { name: "Publishing…" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
	});

	it("says only the session's creator can share when someone else tries", async () => {
		stub(inspection, links, { status: "not_owner" });
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(await screen.findByText("Only the person who started this session can share its files.")).toBeTruthy();
		expect(screen.queryByRole("button", { name: /Publish|Try again/ })).toBeNull();
	});

	it("returns to Publish when the upload is cancelled", async () => {
		stub(inspection, links, { status: "cancelled" });
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Publish" })).toBeTruthy());
	});

	it("disables Share for a video", async () => {
		render(<TooltipProvider><ShareArtifactButton file={{ ...file, path: "clip.mp4" }} name="clip.mp4" /></TooltipProvider>);
		expect(await screen.findByRole("button", { name: "Share: clip.mp4" })).toBeDisabled();
	});

});
