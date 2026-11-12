import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudShareInspection, CloudShareLink, CloudShareLinks, CloudSharePublishResult } from "../../shared/cloud-share";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { ShareArtifactButton, ShareArtifactMenuPopover, publishTitle, useArtifactShareFile, useReconciledShareLinks } from "./ShareArtifactPopover";
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

/** Clicks the header button once it says `label`: Republish only after its check finds the file changed. */
async function openPopover(path = file.path, name = "report.html", label: "Publish" | "Republish" = "Publish") {
	render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={{ ...file, path }} name={name} /></TooltipProvider></QueryClientProvider>);
	await userEvent.click(await screen.findByRole("button", { name: `${label}: ${name}` }));
	return screen.findByRole("dialog");
}

/** The panel as the artifact menu's Publish item opens it: it shows a published file's link without republishing. */
async function openMenuPanel() {
	render(
		<QueryClientProvider client={new QueryClient()}><TooltipProvider>
			<ShareArtifactMenuPopover file={file} name="report.html" open onOpenChange={() => undefined}>
				<button type="button">menu</button>
			</ShareArtifactMenuPopover>
		</TooltipProvider></QueryClientProvider>,
	);
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
		stub(inspection, links, { status: "published", replaced: false, share });
		const popover = await openPopover();
		expect(popover).not.toHaveAttribute("aria-modal", "true");
		expect(await screen.findByRole("textbox", { name: "Public link" })).toBeTruthy();
	});

	it("names the file without its extension", async () => {
		stub(inspection, links, { status: "published", replaced: false, share });
		const popover = await openPopover();
		await screen.findByRole("textbox", { name: "Public link" });
		expect(popover.textContent).toContain("report");
		expect(popover.textContent).not.toContain("report.html");
		expect(publishTitle("notes.v2.md")).toBe("notes.v2");
		expect(publishTitle("README")).toBe("README");
		expect(publishTitle(".env")).toBe(".env");
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

	it("waits for Publish under the warning when the file looks like it holds a credential", async () => {
		const { publishSpy } = stub({ ...inspection, secretFindings: ["AWS access key"] }, links, { status: "published", replaced: false, share });
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-1" });
		expect(await screen.findByRole("textbox", { name: "Public link" })).toBeTruthy();
	});

	it("publishes on the first Publish click and offers the link to copy", async () => {
		const { listSpy, publishSpy } = stub(inspection, links, { status: "published", replaced: false, share });
		publishSpy.mockImplementation(async () => {
			listSpy.mockResolvedValue({ ...links, used: 2, shares: [share] });
			return { status: "published", replaced: false, share };
		});
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		await openPopover();
		// The publish names the inspection it was shown, with no second Publish.
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-1" });
		expect(screen.queryByRole("button", { name: "Publish" })).toBeNull();
		// The plan count includes the new link.
		expect(await screen.findByText("1 of 3 free links left")).toBeTruthy();
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
		expect(screen.getByText("2 of 3 free links left")).toBeTruthy();
	});

	it("says when the file changed since it was published, and the link shows the earlier version", async () => {
		stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] });
		await openMenuPanel();
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
		expect(screen.queryByRole("button", { name: "Republish" })).toBeNull();
	});

	it("says Republish on the button once the file changed, and one click puts the newer version on the same link", async () => {
		const { inspectSpy, publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, { status: "published", replaced: true, share });
		publishSpy.mockImplementation(async () => {
			inspectSpy.mockResolvedValue({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" });
			return { status: "published", replaced: true, share };
		});
		await openPopover(file.path, "report.html", "Republish");
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-1", shareId: SHARE_ID });
		expect(screen.queryByText(/changed since you published/)).toBeNull();
		expect(screen.queryByRole("button", { name: "Republish" })).toBeNull();
		// Once republished, the button checks again and is back to Publish.
		expect(await screen.findByRole("button", { name: "Publish: report.html" })).toBeTruthy();
	});

	it("says Publish, not Republish, while the file is unchanged or never published", async () => {
		const { inspectSpy, listSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-1" }, { ...links, shares: [share] });
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={file} name="report.html" /></TooltipProvider></QueryClientProvider>);
		await screen.findByRole("button", { name: "Publish: report.html" });
		await waitFor(() => expect(inspectSpy).toHaveBeenCalled());
		// Unchanged: the link list is not asked for.
		expect(listSpy).not.toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: "Republish: report.html" })).toBeNull();
	});

	it("says Republish after a pure rename, and one click updates the same link under the new name", async () => {
		const renamed = { ...file, path: "docs/final.html", artifactId: "art-1" };
		const { publishSpy } = stub(
			{ ...inspection, name: "final.html", shareId: SHARE_ID, publishedFingerprint: inspection.fingerprint },
			{ ...links, shares: [share] },
			{ status: "published", replaced: true, share: { ...share, name: "final.html" } },
		);
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={renamed} name="final.html" /></TooltipProvider></QueryClientProvider>);
		await userEvent.click(await screen.findByRole("button", { name: "Republish: final.html" }));
		await waitFor(() => expect(publishSpy).toHaveBeenCalledExactlyOnceWith({
			...renamed, baseUrl: "https://cp.test", fingerprint: inspection.fingerprint, shareId: SHARE_ID,
		}));
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue(share.viewerUrl);
	});

	it("offers Republish beside Unpublish in the panel when the file changed, and it updates the same link", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, { status: "published", replaced: true, share });
		await openMenuPanel();
		const republish = await screen.findByRole("button", { name: "Republish" });
		expect(screen.getByRole("button", { name: "Unpublish" })).toBeTruthy();
		// The link stays copyable, and Republish is the row's one primary action, at its right.
		const copy = screen.getByRole("button", { name: "Copy link" });
		expect(copy.compareDocumentPosition(republish) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		await userEvent.click(republish);
		expect(publishSpy).toHaveBeenCalledWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-1", shareId: SHARE_ID });
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		expect(screen.queryByText(/changed since you published/)).toBeNull();
		expect(screen.queryByRole("button", { name: "Republish" })).toBeNull();
	});

	it("warns about a credential in the newer version before it is republished", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0", secretFindings: ["AWS access key"] }, { ...links, shares: [share] });
		await openPopover(file.path, "report.html", "Republish");
		await screen.findByRole("button", { name: "Republish" });
		expect(screen.getByRole("alert").textContent).toContain("AWS access key");
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("stays on the link, with the newest version to republish, when the file changes during a republish", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, {
			status: "changed",
			inspection: { ...inspection, shareId: SHARE_ID, secretFindings: ["GitHub token"], fingerprint: "fp-2" },
		});
		await openPopover(file.path, "report.html", "Republish");
		expect(await screen.findByText("This file changed while it was being published. Check it and publish again.")).toBeTruthy();
		expect(screen.getByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		expect(screen.getByRole("alert").textContent).toContain("GitHub token");
		publishSpy.mockResolvedValue({ status: "published", replaced: true, share });
		await userEvent.click(screen.getByRole("button", { name: "Republish" }));
		expect(publishSpy).toHaveBeenLastCalledWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-2", shareId: SHARE_ID });
	});

	it("keeps offering Republish, without republishing again, after a cancelled republish", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [share] }, { status: "cancelled" });
		await openPopover(file.path, "report.html", "Republish");
		expect(await screen.findByRole("button", { name: "Republish" })).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Unpublish" })).toBeTruthy();
	});

	it("never offers Republish on a link that was unpublished", async () => {
		stub({ ...inspection, shareId: SHARE_ID, publishedFingerprint: "fp-0" }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Republish" })).toBeNull();
	});

	it("says Publishing while the file uploads", async () => {
		stub(inspection, links);
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise(() => undefined));
		// A publish that never ends outlives this test: give it its own file.
		await openPopover("docs/slow.html");
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

	it("opens the menu's Unpublish on its confirmation, unpublishes, and closes", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] });
		const unpublish = vi.spyOn(aoBridge.cloudShare, "unpublish").mockResolvedValue(undefined);
		const onOpenChange = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient()}><TooltipProvider>
				<ShareArtifactMenuPopover file={file} intent="unpublish" name="report.html" open onOpenChange={onOpenChange}>
					<button type="button">menu</button>
				</ShareArtifactMenuPopover>
			</TooltipProvider></QueryClientProvider>,
		);
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
		render(
			<QueryClientProvider client={new QueryClient()}><TooltipProvider>
				<ShareArtifactMenuPopover file={file} intent="unpublish" name="report.html" open onOpenChange={() => undefined}>
					<button type="button">menu</button>
				</ShareArtifactMenuPopover>
			</TooltipProvider></QueryClientProvider>,
		);
		await userEvent.click(await screen.findByRole("button", { name: "Try again" }));
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("never publishes from the menu's Unpublish when the file has no active link", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		render(
			<QueryClientProvider client={new QueryClient()}><TooltipProvider>
				<ShareArtifactMenuPopover file={file} intent="unpublish" name="report.html" open onOpenChange={() => undefined}>
					<button type="button">menu</button>
				</ShareArtifactMenuPopover>
			</TooltipProvider></QueryClientProvider>,
		);
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("publishes a file whose link was unpublished to a new link", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] }, { status: "published", replaced: false, share: { ...share, id: "new" } });
		await openPopover();
		expect(await screen.findByRole("textbox", { name: "Public link" })).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledExactlyOnceWith({ ...file, baseUrl: "https://cp.test", fingerprint: "fp-1" });
	});

	it("says nothing about an earlier link for a file never published from here", async () => {
		stub(inspection, links);
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(screen.queryByText(/was unpublished/)).toBeNull();
	});

	it("offers only Upgrade at the link limit, never unpublishing", async () => {
		const { publishSpy } = stub(inspection, { ...links, used: 3 });
		await openPopover();
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to publish more.")).toBeTruthy();
		expect(screen.queryByText(/unpublish/i)).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Upgrade" }));
		expect(mocks.openSettings).toHaveBeenCalledWith("billing");
		expect(publishSpy).not.toHaveBeenCalled();
	});

	it("lets a paid plan through at the limit, for a file of any size", async () => {
		stub({ ...inspection, size: 5 * 1024 ** 3 }, { ...links, used: 3, unlimited: true }, { status: "published", replaced: false, share });
		await openPopover();
		expect(await screen.findByRole("textbox", { name: "Public link" })).toBeTruthy();
		// A paid plan has no count to show.
		expect(screen.queryByText(/free links left/)).toBeNull();
	});

	it("shows the server's limit refusal at once, even when another link has the file's name", async () => {
		stub(inspection, { ...links, used: 3, shares: [{ ...share, id: "other" }] }, { status: "limit", message: "Free plan: up to 3 active links. Upgrade to publish more." });
		await openPopover();
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to publish more.")).toBeTruthy();
		expect(screen.getByRole("button", { name: "Upgrade" })).toBeTruthy();
	});

	it("shows the file again, with what it now holds, when it changed while publishing", async () => {
		const { publishSpy } = stub(inspection, links, {
			status: "changed",
			inspection: { ...inspection, size: 4096, secretFindings: ["GitHub token"], fingerprint: "fp-2" },
		});
		await openPopover();
		expect(await screen.findByText("This file changed while it was being published. Check it and publish again.")).toBeTruthy();
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
		await screen.findByRole("button", { name: "Publishing…" });
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		expect(cancel).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Publish: long.html" }));
		expect(await screen.findByRole("button", { name: "Publishing…" })).toBeDisabled();
		finish({ status: "published", replaced: false, share });
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
	});

	it("is not offered where the control plane does not serve share links", async () => {
		mocks.baseUrl = "https://no-shares.test";
		const available = vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(false);
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={file} name="report.html" /></TooltipProvider></QueryClientProvider>);
		await waitFor(() => expect(available).toHaveBeenCalledWith("https://no-shares.test"));
		expect(screen.queryByRole("button", { name: /^Publish/ })).toBeNull();
		// The answer is kept: a second button does not ask again.
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={{ ...file, path: "b.html" }} name="b.html" /></TooltipProvider></QueryClientProvider>);
		expect(available).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("button", { name: /^Publish/ })).toBeNull();
	});

	it("shows a refusal's message, without Electron's IPC wrapper", async () => {
		stub(inspection, links, { status: "error", message: "There is not enough free disk space to prepare this file for sharing." });
		await openPopover();
		expect(await screen.findByText("Publishing failed: There is not enough free disk space to prepare this file for sharing.")).toBeTruthy();
		vi.spyOn(aoBridge.cloudShare, "inspect").mockRejectedValue(new Error("Error invoking remote method 'cloudShare:inspect': CloudShareError: The file is no longer available."));
		await userEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(await screen.findByText("Publishing failed: The file is no longer available.")).toBeTruthy();
	});

	it("offers no Cancel while a cloud session's file is being shared", async () => {
		stub(inspection, links);
		vi.spyOn(aoBridge.cloudShare, "publish").mockReturnValue(new Promise(() => undefined));
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={{ source: "cloud", orgId: "org-1", sessionId: "cloud-1", path: "docs/cloud.html", projectId: "" }} name="cloud.html" /></TooltipProvider></QueryClientProvider>);
		await userEvent.click(await screen.findByRole("button", { name: "Publish: cloud.html" }));
		expect(await screen.findByRole("button", { name: "Publishing…" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
	});

	it("says only the session's creator can publish when someone else tries", async () => {
		stub(inspection, links, { status: "not_owner" });
		await openPopover();
		expect(await screen.findByText("Only the person who started this session can publish its files.")).toBeTruthy();
		expect(screen.queryByRole("button", { name: /^(Publish|Try again)$/ })).toBeNull();
	});

	it("returns to Publish, without publishing again, when the upload is cancelled", async () => {
		const { publishSpy } = stub(inspection, links, { status: "cancelled" });
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(publishSpy).toHaveBeenCalledTimes(1);
	});

	it("disables Publish for a video", async () => {
		render(<QueryClientProvider client={new QueryClient()}><TooltipProvider><ShareArtifactButton file={{ ...file, path: "clip.mp4" }} name="clip.mp4" /></TooltipProvider></QueryClientProvider>);
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
});

describe("useReconciledShareLinks", () => {
	const render = () => {
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
		const invalidate = render();
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKeyForHost() }));
		expect(list).toHaveBeenCalledExactlyOnceWith("https://reconcile.test");
	});

	it("asks for nothing while signed out", async () => {
		mocks.baseUrl = "https://reconcile-signed-out.test";
		mocks.status = "unauthenticated";
		const available = vi.spyOn(aoBridge.cloudShare, "available").mockResolvedValue(true);
		const list = vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue(links);
		const invalidate = render();
		await waitFor(() => expect(available).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(list).not.toHaveBeenCalled();
		expect(invalidate).not.toHaveBeenCalled();
	});
});
