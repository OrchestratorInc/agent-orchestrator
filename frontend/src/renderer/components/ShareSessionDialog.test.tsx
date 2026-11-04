import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { aoBridge } from "../lib/bridge";
import { CloudCpError, type CloudCpShareLink } from "../lib/cloud-cp";
import { createdShareLinkUrl, ShareSessionDialog, shareLinkLabel, shareLinkRequest } from "./ShareSessionDialog";

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

// "Create link" opens the link form; the form's own "Create link" makes it.
async function createLink() {
	fireEvent.click(await screen.findByRole("button", { name: "Create link" }));
	const form = await screen.findByRole("region", { name: "Anyone with the link" }).then((section) => section.querySelector("[data-share-link-form]"));
	if (!(form instanceof HTMLElement)) throw new Error("the link form did not open");
	fireEvent.click(within(form).getByRole("button", { name: "Create link" }));
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
		mocks.client.getShareAccess.mockReset().mockImplementation(async () => ({ entries: [] }));
	});

	it("says a link opens the session too when nobody has been added", async () => {
		renderDialog();
		expect(await screen.findByText("Only people in this workspace and anyone with the link can open this session. Add people or teams above.")).toBeInTheDocument();
		mocks.client.listShareLinks.mockResolvedValue({ links: [] });
		cleanup();
		renderDialog();
		expect(await screen.findByText("Only people in this workspace can open this session. Add people or teams above.")).toBeInTheDocument();
	});

	it("keeps a link just made through picking people, and drops it for another scope", async () => {
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: "new", sessionId: "s1", code: CODE }) });
		renderDialog();
		await createLink();
		expect(await screen.findByRole("textbox", { name: "Share link" })).toHaveValue(`https://cp.example.test/s/${CODE}`);
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.change(input, { target: { value: "lee@acme.co" } });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(await screen.findByRole("button", { name: "Share" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		// Shown only once, so it must survive the confirm step.
		expect(await screen.findByRole("textbox", { name: "Share link" })).toHaveValue(`https://cp.example.test/s/${CODE}`);
		fireEvent.click(screen.getByRole("radio", { name: "The whole project" }));
		await screen.findByText("Share project");
		expect(screen.queryByRole("textbox", { name: "Share link" })).toBeNull();
	});

	it("offers links only, with no error, against a control plane from before teams", async () => {
		mocks.client.getShareAccess.mockReset().mockRejectedValue(new CloudCpError("404 page not found", { status: 404 }));
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: "new", sessionId: "s1", code: CODE }) });
		renderDialog();
		expect(await screen.findByText("Anyone with the link can open this session in AO.")).toBeInTheDocument();
		await waitFor(() => expect(screen.queryByRole("combobox", { name: "Add people or teams" })).toBeNull());
		expect(screen.queryByRole("alert")).toBeNull();
		expect(screen.queryByText("People with access")).toBeNull();
		// A 404 is the answer, not a failure to retry.
		expect(mocks.client.getShareAccess).toHaveBeenCalledTimes(1);
		const rows = within(await screen.findByRole("list")).getAllByRole("listitem");
		expect(rows).toHaveLength(2);
		await createLink();
		expect(await screen.findByRole("textbox", { name: "Share link" })).toHaveValue(`https://cp.example.test/s/${CODE}`);
	});

	it("shows a project or session the control plane can't find as an error, and keeps people for every other session", async () => {
		// A control plane with teams says "not_found" for an archived project or
		// a session not in the project; that is no reason to drop to links only.
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		mocks.client.getShareAccess.mockReset().mockRejectedValueOnce(new CloudCpError("The requested resource was not found.", { status: 404, code: "not_found" }));
		const first = render(
			<QueryClientProvider client={queryClient}>
				<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s1" />
			</QueryClientProvider>,
		);
		expect(await screen.findByRole("alert")).toHaveTextContent("The requested resource was not found.");
		expect(screen.getByRole("combobox", { name: "Add people or teams" })).toBeInTheDocument();
		first.unmount();

		// Another session's dialog in the same app run offers people from the start.
		mocks.client.getShareAccess.mockImplementation(() => new Promise(() => undefined));
		render(
			<QueryClientProvider client={queryClient}>
				<ShareSessionDialog open onOpenChange={() => undefined} orgId={ORG} projectId="project" sessionId="s2" />
			</QueryClientProvider>,
		);
		expect(screen.getByRole("combobox", { name: "Add people or teams" })).toBeInTheDocument();
	});

	it.each([
		["a well-formed code", { code: CODE, token: "tok" }, `https://cp.example.test/s/${CODE}`],
		["no code", { token: "tok" }, `https://cp.example.test/app/share#${ORG}/tok`],
		["a code of the wrong length", { code: "k7Hq2x9PmB4w", token: "tok" }, `https://cp.example.test/app/share#${ORG}/tok`],
	])("shows and copies the link a response with %s makes", async (_name, created, expected) => {
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: "new", sessionId: "s1", ...created }) });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderDialog();
		await createLink();
		const field = await screen.findByRole("textbox", { name: "Share link" });
		expect((field as HTMLInputElement).value).toBe(expected);
		fireEvent.click(screen.getByRole("button", { name: "Copy" }));
		await waitFor(() => expect(writeText).toHaveBeenCalledWith(expected));
		expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
		writeText.mockRestore();
	});

	it("takes a link's one-time URL off screen when that link is revoked, and only that link", async () => {
		mocks.client.listShareLinks.mockReset().mockResolvedValue({
			links: [
				shareLink({ id: "new", sessionId: "s1", role: "editor", interaction: "interact", modeCap: "standard" }),
				shareLink({ id: "whole-project", role: "editor", interaction: "interact" }),
			],
		});
		mocks.client.createShareLink.mockResolvedValue({ link: shareLink({ id: "new", sessionId: "s1", code: CODE }) });
		renderDialog();
		await createLink();
		expect(await screen.findByRole("textbox", { name: "Share link" })).toHaveValue(`https://cp.example.test/s/${CODE}`);
		const rows = () => within(screen.getAllByRole("list")[0]!).getAllByRole("listitem");
		fireEvent.click(within(rows()[1]!).getByRole("button", { name: "Revoke" }));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", "whole-project"));
		expect(screen.getByRole("textbox", { name: "Share link" })).toBeInTheDocument();
		fireEvent.click(within(rows()[0]!).getByRole("button", { name: "Revoke" }));
		await waitFor(() => expect(mocks.client.revokeShareLink).toHaveBeenCalledWith(ORG, "project", "new"));
		await waitFor(() => expect(screen.queryByRole("textbox", { name: "Share link" })).toBeNull());
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
		await createLink();
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
