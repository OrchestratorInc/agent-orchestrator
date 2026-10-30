import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudShareInspection, CloudShareLinks, CloudSharePublishResult } from "../../shared/cloud-share";
import { aoBridge } from "../lib/bridge";
import { formatBytes, ShareArtifactDialog } from "./ShareArtifactDialog";

const mocks = vi.hoisted(() => ({
	status: "authenticated" as string,
	openSettings: vi.fn(),
}));

vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.test" } }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: mocks.status, session: null }) }));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { openGlobalSettings: typeof mocks.openSettings }) => unknown) => select({ openGlobalSettings: mocks.openSettings }),
}));

const file = { sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
const inspection: CloudShareInspection = { name: "report.html", size: 2048, secretFindings: [], unsupported: false };
const links: CloudShareLinks = { shares: [], limit: 3, used: 1, unlimited: false };

function stub(inspect: CloudShareInspection, list: CloudShareLinks, publish?: CloudSharePublishResult) {
	vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue(inspect);
	vi.spyOn(aoBridge.cloudShare, "list").mockResolvedValue(list);
	return vi.spyOn(aoBridge.cloudShare, "publish").mockResolvedValue(publish ?? { status: "cancelled" });
}

function renderDialog(onOpenChange = vi.fn(), shareId?: string) {
	render(<ShareArtifactDialog file={{ ...file, shareId }} name="report.html" open onOpenChange={onOpenChange} />);
	return onOpenChange;
}

beforeEach(() => {
	mocks.status = "authenticated";
	mocks.openSettings.mockReset();
	vi.restoreAllMocks();
});

describe("ShareArtifactDialog", () => {
	it("asks a signed-out person to sign in", async () => {
		mocks.status = "unauthenticated";
		const signIn = vi.spyOn(aoBridge.cloud, "signIn").mockResolvedValue();
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(signIn).toHaveBeenCalled();
	});

	it("shows what goes public, the plan usage and any credential found", async () => {
		stub({ ...inspection, secretFindings: ["AWS access key"] }, links);
		renderDialog();
		expect(await screen.findByText("Anyone with the link can view this file. Free plan: 2 of 3 active links.")).toBeTruthy();
		expect(screen.getByText("2.0 KB")).toBeTruthy();
		expect(screen.getByRole("alert").textContent).toContain("AWS access key");
	});

	it("publishes and offers the link to copy", async () => {
		const publish = stub(inspection, links, {
			status: "published",
			replaced: false,
			share: { id: "s-1", linkId: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://viewer.test/artifact/abc",
				contentType: "text/html", size: 2048, sha256: "", createdAt: "", updatedAt: "" },
		});
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		expect(publish).toHaveBeenCalledWith({ ...file, shareId: undefined, baseUrl: "https://cp.test" });
		expect(await screen.findByText("https://viewer.test/artifact/abc")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
		expect(writeText).toHaveBeenCalledWith("https://viewer.test/artifact/abc");
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
	});

	it("closes when the person declines the native confirmation", async () => {
		stub(inspection, links, { status: "cancelled" });
		const onOpenChange = renderDialog();
		await userEvent.click(await screen.findByRole("button", { name: "Publish" }));
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
	});

	it("offers only Upgrade at the link limit, never unpublishing", async () => {
		stub(inspection, { ...links, used: 3 });
		renderDialog();
		expect(await screen.findByText("Free plan: up to 3 active links. Upgrade to share more.")).toBeTruthy();
		expect(screen.queryByText(/unpublish/i)).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Upgrade" }));
		expect(mocks.openSettings).toHaveBeenCalledWith("billing");
	});

	it("lets an update or a paid plan through at the limit", async () => {
		stub(inspection, { ...links, used: 3 });
		renderDialog(vi.fn(), "share-9");
		expect(await screen.findByRole("button", { name: "Update link" })).toBeTruthy();
	});

	it("shares a file of any size on the free plan", async () => {
		stub({ ...inspection, size: 5 * 1024 ** 3 }, links);
		renderDialog();
		expect(await screen.findByRole("button", { name: "Publish" })).toBeTruthy();
		expect(screen.getByText("5.0 GB")).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Upgrade" })).toBeNull();
	});

	it("says video is coming soon", async () => {
		stub({ ...inspection, name: "clip.mp4", unsupported: true }, links);
		renderDialog();
		expect(await screen.findByText("Sharing videos is coming soon.")).toBeTruthy();
	});

	it("formats sizes", () => {
		expect([formatBytes(512), formatBytes(2048), formatBytes(64 << 20), formatBytes(5 * 1024 ** 3)]).toEqual(["512 B", "2.0 KB", "64 MB", "5.0 GB"]);
	});
});
