import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { redeemShareTarget, requestOpenShareLink, ShareLinkHost } from "./OpenShareLinkDialog";

const CODE = "k7Hq2x9PmB4wR5tY8uI0oP";
const ORG = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";

const mocks = vi.hoisted(() => ({
	client: { redeemShareLink: vi.fn(), redeemShareLinkCode: vi.fn() },
	navigate: vi.fn(),
}));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.example.test", userId: "me" }) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));

const shared = { shared: { project: { id: "project" }, sessionId: "s1" } };

function renderHost() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><ShareLinkHost /></QueryClientProvider>);
}

describe("opening a share link", () => {
	// Radix's focus scope moves focus from a 0 ms timer when a dialog unmounts.
	// Unmount, then wait one tick so that timer fires while this file's DOM
	// still exists; left pending, it fires into the next test file's jsdom
	// window on a shared CI worker and fails the run as an unhandled error.
	afterEach(async () => {
		cleanup();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});

	beforeEach(() => {
		mocks.client.redeemShareLink.mockReset().mockResolvedValue(shared);
		mocks.client.redeemShareLinkCode.mockReset().mockResolvedValue(shared);
		mocks.navigate.mockReset();
	});

	it("redeems a short link by its code and an older link by its organization and token", async () => {
		await redeemShareTarget(mocks.client, { code: CODE });
		expect(mocks.client.redeemShareLinkCode).toHaveBeenCalledWith(CODE);
		expect(mocks.client.redeemShareLink).not.toHaveBeenCalled();
		await redeemShareTarget(mocks.client, { orgId: "org-1", token: "tok" });
		expect(mocks.client.redeemShareLink).toHaveBeenCalledWith("org-1", "tok");
	});

	it("opens a short link pasted from a chat and goes to the session", async () => {
		renderHost();
		act(() => requestOpenShareLink());
		fireEvent.change(await screen.findByRole("textbox", { name: "Share link" }), { target: { value: `  https://api.aoagents.dev/s/${CODE}/).  ` } });
		fireEvent.click(screen.getByRole("button", { name: "Open" }));
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalled());
		expect(mocks.client.redeemShareLinkCode).toHaveBeenCalledWith(CODE);
		expect(mocks.client.redeemShareLink).not.toHaveBeenCalled();
	});

	it("asks for a new link when the control plane refuses a short link as a bad request", async () => {
		mocks.client.redeemShareLinkCode.mockRejectedValue(new CloudCpError("The request body is invalid.", { status: 400, code: "invalid_request" }));
		renderHost();
		act(() => requestOpenShareLink(`ao-app://share/${CODE}`));
		expect((await screen.findByRole("alert")).textContent).toBe("This link can't be opened right now. Ask for a new link.");
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it.each([
		[404, "This link has expired or was revoked. Ask for a new one."],
		[403, "This link is for other people. Ask its sender to share it with your account."],
	])("explains a %i for a short link as before", async (status, message) => {
		mocks.client.redeemShareLinkCode.mockRejectedValue(new CloudCpError("refused", { status }));
		renderHost();
		act(() => requestOpenShareLink(`ao-app://share/${CODE}`));
		expect((await screen.findByRole("alert")).textContent).toBe(message);
	});

	it("keeps showing the control plane's text for a bad request on a link from before codes", async () => {
		mocks.client.redeemShareLink.mockRejectedValue(new CloudCpError("The request body is invalid.", { status: 400 }));
		renderHost();
		act(() => requestOpenShareLink(`ao-app://share/${ORG}/tok`));
		expect((await screen.findByRole("alert")).textContent).toBe("The request body is invalid.");
		expect(mocks.client.redeemShareLink).toHaveBeenCalledWith(ORG, "tok");
	});

	it("does not send a code of the wrong length", async () => {
		renderHost();
		act(() => requestOpenShareLink("ao-app://share/k7Hq2x9PmB4w"));
		expect((await screen.findByRole("alert")).textContent).toBe("That isn't a share link.");
		expect(mocks.client.redeemShareLinkCode).not.toHaveBeenCalled();
		expect(mocks.client.redeemShareLink).not.toHaveBeenCalled();
	});
});
