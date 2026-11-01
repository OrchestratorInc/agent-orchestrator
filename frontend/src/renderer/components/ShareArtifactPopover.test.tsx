import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudShareInspection, CloudShareLink, CloudShareLinks, CloudSharePublishResult } from "../../shared/cloud-share";
import { aoBridge } from "../lib/bridge";
import { formatBytes, ShareArtifactButton } from "./ShareArtifactPopover";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	status: "authenticated" as string,
	openSettings: vi.fn(),
}));

vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.test" } }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: mocks.status, session: null }) }));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { openGlobalSettings: typeof mocks.openSettings }) => unknown) => select({ openGlobalSettings: mocks.openSettings }),
}));

const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";
const file = { sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
const inspection: CloudShareInspection = { name: "report.html", size: 2048, secretFindings: [], unsupported: false };
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
	await userEvent.click(screen.getByRole("button", { name: `Share: ${name}` }));
	return screen.findByRole("dialog");
}

beforeEach(() => {
	mocks.status = "authenticated";
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

	it("shows what goes public, the size, the plan usage and any credential found", async () => {
		stub({ ...inspection, secretFindings: ["AWS access key"] }, links);
		await openPopover();
		expect(await screen.findByText("Anyone with the link can view this file.")).toBeTruthy();
		expect(screen.getByText("2.0 KB · Free plan: 2 of 3 active links.")).toBeTruthy();
		expect(screen.getByRole("alert").textContent).toContain("AWS access key");
	});

	it("publishes on Publish and offers the link to copy", async () => {
		const { publishSpy } = stub(inspection, links, { status: "published", replaced: false, share });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(publishSpy).toHaveBeenCalledWith({ ...file, baseUrl: "https://cp.test", shareId: undefined });
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
		expect(writeText).toHaveBeenCalledWith("https://viewer.test/artifact/abc");
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
	});

	it("opens a published file to its link, with Update link and Unpublish", async () => {
		const { publishSpy } = stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [share] }, { status: "published", replaced: true, share });
		await openPopover();
		expect(await screen.findByRole("textbox", { name: "Public link" })).toHaveValue("https://viewer.test/artifact/abc");
		expect(screen.queryByRole("button", { name: "Publish" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Update link" }));
		expect(publishSpy).toHaveBeenCalledWith({ ...file, baseUrl: "https://cp.test", shareId: SHARE_ID });
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
		expect(screen.getByText("2.0 KB · Free plan: 1 of 3 active links.")).toBeTruthy();
	});

	it("treats a record of an unpublished link as unpublished", async () => {
		stub({ ...inspection, shareId: SHARE_ID }, { ...links, shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await openPopover();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
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
		expect(screen.getByText("5.0 GB")).toBeTruthy();
	});

	it("returns to Publish when the upload is cancelled", async () => {
		stub(inspection, links, { status: "cancelled" });
		await openPopover();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Publish" })).toBeTruthy());
	});

	it("disables Share for a video", () => {
		render(<TooltipProvider><ShareArtifactButton file={{ ...file, path: "clip.mp4" }} name="clip.mp4" /></TooltipProvider>);
		expect(screen.getByRole("button", { name: "Share: clip.mp4" })).toBeDisabled();
	});

	it("formats sizes", () => {
		expect([formatBytes(512), formatBytes(2048), formatBytes(64 << 20), formatBytes(5 * 1024 ** 3)]).toEqual(["512 B", "2.0 KB", "64 MB", "5.0 GB"]);
	});
});
