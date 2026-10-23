import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useUiStore } from "../stores/ui-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ArtifactShareButton, useArtifactShare } from "./ArtifactShare";

const { publishMock, signInMock, sessionState, localAuth } = vi.hoisted(() => ({
	publishMock: vi.fn(),
	signInMock: vi.fn(),
	sessionState: {
		status: "authenticated" as "authenticated" | "unauthenticated",
		session: { user: { id: "u1", email: "me@example.com", displayName: "Me" }, storedAt: "t1", authProvider: "local" } as object | null,
	},
	localAuth: { available: false },
}));

vi.mock("../lib/artifact-share", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/artifact-share")>()),
	publishArtifactShare: publishMock,
}));
vi.mock("../lib/cloud-session", () => ({
	isCloudSignInConfigured: () => true,
	useCloudSession: () => ({ status: sessionState.status, session: sessionState.session, signIn: signInMock }),
}));
vi.mock("../hooks/useCloudLocalAuth", () => ({ useCloudLocalAuth: () => ({ available: localAuth.available }) }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ baseUrl: "http://127.0.0.1:8081" }) }));

const artifact: SessionArtifact = {
	kind: "html",
	name: "report.html",
	path: "report.html",
	rawUrl: "http://ao-preview-artifact.x.localhost:3001/report.html?raw=true",
	size: 10,
	updatedAt: "2026-10-23T00:00:00Z",
};
const session = { id: "sess-1", title: "Login page", workspaceName: "agent-orchestrator" } as WorkspaceSession;

function Harness() {
	const share = useArtifactShare(session, artifact);
	return (
		<>
			<button type="button" onClick={share.share}>share</button>
			<span data-testid="state">{share.state}</span>
			{share.error ? <span role="alert">{share.error}</span> : null}
			{share.limitDialog}
		</>
	);
}

const SIGNED_IN = { user: { id: "u1", email: "me@example.com", displayName: "Me" }, storedAt: "t1", authProvider: "local" };

describe("useArtifactShare", () => {
	beforeEach(() => {
		publishMock.mockReset();
		signInMock.mockReset();
		sessionState.status = "authenticated";
		sessionState.session = SIGNED_IN;
		localAuth.available = false;
		useLocalSignInDialogStore.setState({ open: false });
		useUiStore.setState({ globalToast: null, settingsModal: null });
	});

	it("publishes the artifact under its project and session, copies the link, and announces it", async () => {
		publishMock.mockResolvedValue({ id: "s1", url: "http://agent-orchestrator-login-page-me.share.localhost:8081/", name: "report.html", replaced: false });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText");
		render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		expect(publishMock).toHaveBeenCalledWith(expect.objectContaining({
			baseUrl: "http://127.0.0.1:8081",
			name: "report.html",
			project: "agent-orchestrator",
			feature: "Login page",
			rawUrl: artifact.rawUrl,
		}), expect.any(String));
		expect(writeText).toHaveBeenCalledWith("http://agent-orchestrator-login-page-me.share.localhost:8081/");
		expect(useUiStore.getState().globalToast).toMatchObject({ title: "Share link copied", body: "http://agent-orchestrator-login-page-me.share.localhost:8081/" });
		expect(screen.getByTestId("state").textContent).toBe("idle");
	});

	it("says the link was updated when publishing replaced an existing one", async () => {
		publishMock.mockResolvedValue({ id: "s1", url: "http://x.share.localhost/", name: "report.html", replaced: true });
		render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		expect(useUiStore.getState().globalToast?.title).toBe("Share link updated and copied");
	});

	it("asks a signed-out user to sign in, then shares once they are signed in", async () => {
		sessionState.status = "unauthenticated";
		sessionState.session = null;
		publishMock.mockResolvedValue({ id: "s1", url: "http://x.share.localhost/", name: "report.html", replaced: false });
		const view = render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		expect(signInMock).toHaveBeenCalledTimes(1);
		expect(publishMock).not.toHaveBeenCalled();
		expect(screen.getByTestId("state").textContent).toBe("signingIn");

		sessionState.status = "authenticated";
		sessionState.session = SIGNED_IN;
		await act(async () => view.rerender(<Harness />));
		expect(publishMock).toHaveBeenCalledTimes(1);
	});

	it("asks for sign-in once when the control plane refuses the session, without retrying in a loop", async () => {
		const { ArtifactShareAuthError } = await import("../lib/artifact-share");
		publishMock.mockRejectedValue(new ArtifactShareAuthError());
		const view = render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		for (let index = 0; index < 3; index++) await act(async () => view.rerender(<Harness />));
		expect(publishMock).toHaveBeenCalledTimes(1);
		expect(signInMock).toHaveBeenCalledTimes(1);

		// A new sign-in brings a different session, and the share continues.
		publishMock.mockResolvedValue({ id: "s1", url: "http://x.share.localhost/", name: "report.html", replaced: false });
		sessionState.session = { ...SIGNED_IN, storedAt: "t2" };
		await act(async () => view.rerender(<Harness />));
		expect(publishMock).toHaveBeenCalledTimes(2);
		expect(useUiStore.getState().globalToast?.title).toBe("Share link copied");
	});

	it("cancels the share when the local sign-in dialog is closed without signing in", async () => {
		sessionState.status = "unauthenticated";
		sessionState.session = null;
		localAuth.available = true;
		render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		expect(useLocalSignInDialogStore.getState().open).toBe(true);
		expect(signInMock).not.toHaveBeenCalled();

		await act(async () => useLocalSignInDialogStore.getState().setOpen(false));
		expect(screen.getByTestId("state").textContent).toBe("idle");
		expect(screen.getByRole("alert").textContent).toBe("Sign in to AO Cloud to share.");
		expect(useLocalSignInDialogStore.getState().open).toBe(false);
		expect(publishMock).not.toHaveBeenCalled();
	});

	it("opens the upgrade dialog at the link limit, and Upgrade opens the billing settings", async () => {
		const { ArtifactShareLimitError } = await import("../lib/artifact-share");
		publishMock.mockRejectedValue(new ArtifactShareLimitError("limit"));
		render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		expect(await screen.findByRole("dialog", { name: "Share limit reached" })).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: "Upgrade to AO Cloud" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "billing" });
		expect(screen.queryByRole("dialog", { name: "Share limit reached" })).toBeNull();
	});

	it("shows why sharing failed", async () => {
		publishMock.mockRejectedValue(new Error("The artifact could not be stored."));
		render(<Harness />);
		await userEvent.click(screen.getByRole("button", { name: "share" }));
		expect(screen.getByRole("alert").textContent).toBe("The artifact could not be stored.");
		expect(useUiStore.getState().globalToast).toBeNull();
	});
});

describe("ArtifactShareButton", () => {
	beforeEach(() => {
		publishMock.mockReset();
		sessionState.status = "authenticated";
		sessionState.session = SIGNED_IN;
		useUiStore.setState({ globalToast: null, settingsModal: null });
	});

	it("shares the open artifact, showing progress on the button while it publishes", async () => {
		let finish: (value: unknown) => void = () => {};
		publishMock.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		const busy = screen.getByRole("button", { name: "Sharing…" });
		expect(busy).toBeDisabled();
		await act(async () => finish({ id: "s1", url: "http://x.share.localhost/", name: "report.html", replaced: false }));
		expect(screen.getByRole("button", { name: "Share" })).toBeEnabled();
		expect(useUiStore.getState().globalToast?.body).toBe("http://x.share.localhost/");
	});

	it("says when the signed-in server does not offer share links", async () => {
		const { ArtifactShareUnavailableError } = await import("../lib/artifact-share");
		publishMock.mockRejectedValue(new ArtifactShareUnavailableError());
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await userEvent.click(screen.getByRole("button", { name: "Share" }));
		expect(screen.getByRole("alert").textContent).toBe("Sharing isn’t available on this AO Cloud server.");
	});
});
