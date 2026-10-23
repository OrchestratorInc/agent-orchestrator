import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { ArtifactShareAuthError, ArtifactShareLimitError, ArtifactShareUnavailableError } from "../lib/artifact-share";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useUiStore } from "../stores/ui-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ArtifactShareButton } from "./ArtifactShare";

const { publishMock, signInMock, sessionState, localAuth } = vi.hoisted(() => ({
	publishMock: vi.fn(),
	signInMock: vi.fn(),
	sessionState: {
		status: "authenticated" as "authenticated" | "unauthenticated",
		session: null as object | null,
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

const SIGNED_IN = { user: { id: "u1", email: "me@example.com", displayName: "Me" }, storedAt: "t1", authProvider: "local" };
const LINK = "http://agent-orchestrator-login-page-me.share.localhost:8081/";
const artifact: SessionArtifact = {
	kind: "html",
	name: "report.html",
	path: "report.html",
	rawUrl: "http://ao-preview-artifact.x.localhost:3001/report.html?raw=true",
	size: 10,
	updatedAt: "2026-10-23T00:00:00Z",
};
const session = { id: "sess-1", title: "Login page", workspaceName: "agent-orchestrator" } as WorkspaceSession;
const SIGN_IN_TEXT = "Sign in to AO Cloud to create a shareable link for this artifact.";

function signedOut() {
	sessionState.status = "unauthenticated";
	sessionState.session = null;
}

async function openPanel() {
	await userEvent.click(screen.getByRole("button", { name: "Share" }));
}

describe("ArtifactShareButton", () => {
	beforeEach(() => {
		publishMock.mockReset();
		signInMock.mockReset();
		sessionState.status = "authenticated";
		sessionState.session = SIGNED_IN;
		localAuth.available = false;
		useLocalSignInDialogStore.setState({ open: false });
		useUiStore.setState({ settingsModal: null });
	});

	it("creates the link for a signed-in user and shows it with a copy action", async () => {
		publishMock.mockResolvedValue({ id: "s1", url: LINK, name: "report.html", replaced: false });
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();

		expect(publishMock).toHaveBeenCalledWith(expect.objectContaining({
			baseUrl: "http://127.0.0.1:8081",
			name: "report.html",
			project: "agent-orchestrator",
			feature: "Login page",
			rawUrl: artifact.rawUrl,
		}), expect.any(String));
		expect(screen.getByRole("dialog", { name: "Share report.html" })).toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: "Share link" })).toHaveValue(LINK);
		expect(screen.getByText("Anyone with this link can view this artifact.")).toBeInTheDocument();
		expect(screen.queryByText(SIGN_IN_TEXT)).toBeNull();

		await userEvent.click(screen.getByRole("button", { name: "Copy link" }));
		expect(writeText).toHaveBeenCalledWith(LINK);
		expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
		writeText.mockRestore();
	});

	it("says the link now shows the latest version when publishing replaced it", async () => {
		publishMock.mockResolvedValue({ id: "s1", url: LINK, name: "report.html", replaced: true });
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();
		expect(screen.getByText("This link now shows the latest version.")).toBeInTheDocument();
	});

	it("tells a signed-out user to sign in, and creates the link once they have", async () => {
		signedOut();
		publishMock.mockResolvedValue({ id: "s1", url: LINK, name: "report.html", replaced: false });
		const view = render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();

		expect(screen.getByText(SIGN_IN_TEXT)).toBeInTheDocument();
		expect(signInMock).not.toHaveBeenCalled();
		expect(publishMock).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
		expect(signInMock).toHaveBeenCalledTimes(1);
		expect(screen.getByText("Finish signing in to create the link.")).toBeInTheDocument();

		sessionState.status = "authenticated";
		sessionState.session = SIGNED_IN;
		await act(async () => view.rerender(<ArtifactShareButton artifact={artifact} session={session} />));
		expect(publishMock).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("textbox", { name: "Share link" })).toHaveValue(LINK);
	});

	it("asks a refused session to sign in once, without retrying in a loop", async () => {
		publishMock.mockRejectedValue(new ArtifactShareAuthError());
		const view = render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();
		expect(screen.getByText(SIGN_IN_TEXT)).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
		for (let index = 0; index < 3; index++) await act(async () => view.rerender(<ArtifactShareButton artifact={artifact} session={session} />));
		expect(publishMock).toHaveBeenCalledTimes(1);
		expect(signInMock).toHaveBeenCalledTimes(1);

		publishMock.mockResolvedValue({ id: "s1", url: LINK, name: "report.html", replaced: false });
		sessionState.session = { ...SIGNED_IN, storedAt: "t2" };
		await act(async () => view.rerender(<ArtifactShareButton artifact={artifact} session={session} />));
		expect(publishMock).toHaveBeenCalledTimes(2);
		expect(screen.getByRole("textbox", { name: "Share link" })).toHaveValue(LINK);
	});

	it("returns to the sign-in prompt when the local sign-in dialog is closed without signing in", async () => {
		signedOut();
		localAuth.available = true;
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();
		await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
		expect(useLocalSignInDialogStore.getState().open).toBe(true);
		expect(screen.queryByRole("dialog", { name: "Share report.html" })).toBeNull();

		await act(async () => useLocalSignInDialogStore.getState().setOpen(false));
		expect(screen.getByText(SIGN_IN_TEXT)).toBeInTheDocument();
		expect(useLocalSignInDialogStore.getState().open).toBe(false);
		expect(publishMock).not.toHaveBeenCalled();
	});

	it("offers the upgrade at the link limit", async () => {
		publishMock.mockRejectedValue(new ArtifactShareLimitError("limit"));
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();
		expect(screen.getByText(/You’ve used all of your share links/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Upgrade to AO Cloud" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "billing" });
	});

	it("explains a failure and can try again", async () => {
		publishMock.mockRejectedValueOnce(new ArtifactShareUnavailableError());
		publishMock.mockResolvedValueOnce({ id: "s1", url: LINK, name: "report.html", replaced: false });
		render(<ArtifactShareButton artifact={artifact} session={session} />);
		await openPanel();
		expect(screen.getByRole("alert").textContent).toBe("Sharing isn’t available on this AO Cloud server.");
		await userEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(screen.getByRole("textbox", { name: "Share link" })).toHaveValue(LINK);
	});
});
