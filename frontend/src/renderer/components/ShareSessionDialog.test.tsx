import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { aoBridge } from "../lib/bridge";
import type { CloudCpShareLink } from "../lib/cloud-cp";
import { createdShareLinkUrl, ShareSessionDialog, shareLinkLabel, shareLinkRequest } from "./ShareSessionDialog";

const mocks = vi.hoisted(() => ({
	client: {
		listShareLinks: vi.fn(),
		createShareLink: vi.fn(),
		revokeShareLink: vi.fn(),
	},
}));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.example.test", userId: "me" }) }));

const t = appI18n.t.bind(appI18n);
const ORG = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
const CODE = "k7Hq2x9PmB4wR5tY8uI0oP";

function shareLink(overrides: Partial<CloudCpShareLink>): CloudCpShareLink {
	return {
		id: "link", orgId: ORG, projectId: "project", role: "viewer", status: "active", accessScope: "anyone",
		recipients: [], interaction: "view", deniedCommands: [], createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
		...overrides,
	};
}

function renderDialog() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s1" />
		</QueryClientProvider>,
	);
}

describe("sharing a session", () => {
	// Radix's focus scope moves focus from a 0 ms timer when a dialog unmounts.
	// Unmount, then wait one tick so that timer fires while this file's DOM
	// still exists; left pending, it fires into the next test file's jsdom
	// window on a shared CI worker and fails the run as an unhandled error.
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});

	beforeEach(() => {
		mocks.client.listShareLinks.mockReset().mockResolvedValue({
			links: [
				shareLink({ id: "this-session", sessionId: "s1" }),
				shareLink({ id: "whole-project", role: "editor", interaction: "interact" }),
				shareLink({ id: "revoked", sessionId: "s1", status: "revoked" }),
				shareLink({ id: "other-session", sessionId: "s2" }),
			],
		});
		mocks.client.createShareLink.mockReset();
		mocks.client.revokeShareLink.mockReset().mockResolvedValue(undefined);
	});

	it.each([
		["a well-formed code", { code: CODE, token: "tok" }, `https://cp.example.test/s/${CODE}`],
		["no code", { token: "tok" }, `https://cp.example.test/app/share#${ORG}/tok`],
		["a code of the wrong length", { code: "k7Hq2x9PmB4w", token: "tok" }, `https://cp.example.test/app/share#${ORG}/tok`],
	])("shows and copies the link a response with %s makes", async (_name, created, expected) => {
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: "new", sessionId: "s1", ...created }) });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderDialog();
		fireEvent.click(await screen.findByRole("button", { name: "Create link" }));
		const field = await screen.findByRole("textbox", { name: "Share link" });
		expect((field as HTMLInputElement).value).toBe(expected);
		fireEvent.click(screen.getByRole("button", { name: "Copy" }));
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(expected));
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
		writeText.mockRestore();
	});

	it("lists this session's and the project's live links, and revokes one", async () => {
		renderDialog();
		const list = await screen.findByRole("list");
		const rows = within(list).getAllByRole("listitem");
		expect(rows.map((row) => row.textContent)).toEqual([
			expect.stringContaining("Watch"),
			expect.stringContaining("Whole project · Start sessions"),
		]);
		fireEvent.click(within(rows[1]!).getByRole("button", { name: "Revoke" }));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", "whole-project"));
		await waitFor(() => expect(mocks.client.listShareLinks).toHaveBeenCalledTimes(2));
	});

	it("says the link is missing when the response has neither a usable code nor a token", async () => {
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: "new", code: "not-a-code" }) });
		renderDialog();
		fireEvent.click(await screen.findByRole("button", { name: "Create link" }));
		expect((await screen.findByRole("alert")).textContent).toBe("The control plane did not return the link.");
		expect(screen.queryByRole("textbox", { name: "Share link" })).toBeNull();
	});
});

describe("share links", () => {
	// Radix's focus scope moves focus from a 0 ms timer when a dialog unmounts.
	// Unmount, then wait one tick so that timer fires while this file's DOM
	// still exists; left pending, it fires into the next test file's jsdom
	// window on a shared CI worker and fails the run as an unhandled error.
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});

	it("shares one session, or the whole project without naming a session", () => {
		expect(shareLinkRequest({ scope: "session", access: "editor", modeCap: "trusted", expiry: "24", sessionId: "s1" })).toEqual({
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "trusted", expiresInHours: 24,
		});
		expect(shareLinkRequest({ scope: "project", access: "editor", modeCap: "standard", expiry: "never", sessionId: "s1" })).toEqual({
			role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard",
		});
	});

	it("keeps a watch-only link read-only whatever mode was picked", () => {
		expect(shareLinkRequest({ scope: "project", access: "viewer", modeCap: "trusted", expiry: "168", sessionId: "s1" })).toMatchObject({
			role: "viewer", interaction: "view", modeCap: "read-only", expiresInHours: 168,
		});
	});

	it("copies the short link when the control plane minted a code, else the token link", () => {
		const org = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
		const code = "k7Hq2x9PmB4wR5tY8uI0oP";
		expect(createdShareLinkUrl("https://cp.example.test", org, { code, token: "tok" })).toBe(`https://cp.example.test/s/${code}`);
		expect(createdShareLinkUrl("https://cp.example.test/", org, { code })).toBe(`https://cp.example.test/s/${code}`);
		expect(createdShareLinkUrl("https://cp.example.test", org, { token: "tok" })).toBe(`https://cp.example.test/app/share#${org}/tok`);
		expect(createdShareLinkUrl("https://cp.example.test", org, {})).toBeUndefined();
	});

	it("copies the token link when the code is not one the app can open", () => {
		const org = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
		const tokenLink = `https://cp.example.test/app/share#${org}/tok`;
		for (const code of ["", "k7Hq2x9PmB4w", "k7Hq2x9PmB4wR5tY8uI0oPZ", "k7Hq2x9PmB4w/5tY8uI0oP", "k7Hq2x9PmB4wR5tY8uI0o "]) {
			expect(createdShareLinkUrl("https://cp.example.test", org, { code, token: "tok" }), code).toBe(tokenLink);
			expect(createdShareLinkUrl("https://cp.example.test", org, { code }), code).toBeUndefined();
		}
		expect(createdShareLinkUrl("https://cp.example.test", org, { code: 12345 as unknown as string, token: "tok" })).toBe(tokenLink);
		expect(createdShareLinkUrl("https://cp.example.test", org, { code: null as unknown as string, token: "tok" })).toBe(tokenLink);
	});

	it("names a whole-project link and what it lets people do", () => {
		expect(shareLinkLabel({ role: "editor", sessionId: "s1" }, t)).toBe("Write to the agent");
		expect(shareLinkLabel({ role: "editor" }, t)).toBe("Whole project · Start sessions");
		expect(shareLinkLabel({ role: "viewer" }, t)).toBe("Whole project · Watch");
	});
});
