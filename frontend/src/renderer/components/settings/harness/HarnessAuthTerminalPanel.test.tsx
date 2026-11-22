import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { aoBridge } from "../../../lib/bridge";
import { SESSION_BROWSER_POPOUT_REQUEST } from "../../../lib/session-browser-popout-request";
import { AppBrowserLinkContext } from "../../AppLink";
import { HarnessAuthTerminalPanel, type AuthTerminalWorkflow } from "./HarnessAuthTerminalPanel";

const loginUrl = "https://app.cline.bot/auth/device";

vi.mock("../../TerminalPane", () => ({
	TerminalPane: ({ onLinkOpen }: { onLinkOpen?: (url: string) => void }) => (
		<button onClick={() => onLinkOpen?.(loginUrl)} type="button">Cline device link</button>
	),
}));

const workflow: AuthTerminalWorkflow = {
	agentId: "cline",
	action: "login",
	terminal: { createdAt: "2026-11-22T00:00:00Z", handleId: "cline-login", title: "Cline login", workingDir: "." },
	guidance: "",
	phase: "running",
	startedAt: 0,
};

afterEach(() => vi.restoreAllMocks());

it("opens a Cline login link in the session Browser above Settings without ending login", () => {
	const openInBrowser = vi.fn();
	const onClose = vi.fn();
	const popOut = vi.fn();
	window.addEventListener(SESSION_BROWSER_POPOUT_REQUEST, popOut);
	try {
		render(
			<AppBrowserLinkContext.Provider value={openInBrowser}>
				<HarnessAuthTerminalPanel workflow={workflow} onClose={onClose} onRetry={vi.fn()} onTerminalState={vi.fn()} />
			</AppBrowserLinkContext.Provider>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Cline device link" }));
		expect(openInBrowser).toHaveBeenCalledExactlyOnceWith(loginUrl);
		expect(popOut).toHaveBeenCalledOnce();
		expect(onClose).not.toHaveBeenCalled();
	} finally {
		window.removeEventListener(SESSION_BROWSER_POPOUT_REQUEST, popOut);
	}
});

it("opens the login link externally when Settings has no session Browser", () => {
	const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
	render(<HarnessAuthTerminalPanel workflow={workflow} onClose={vi.fn()} onRetry={vi.fn()} onTerminalState={vi.fn()} />);
	fireEvent.click(screen.getByRole("button", { name: "Cline device link" }));
	expect(openExternal).toHaveBeenCalledExactlyOnceWith(loginUrl);
});
