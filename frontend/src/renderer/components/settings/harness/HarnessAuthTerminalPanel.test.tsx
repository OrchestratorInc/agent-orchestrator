import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AppBrowserLinkContext } from "../../AppLink";
import { SettingsBrowserLinkContext } from "../SettingsBrowserOverlay";
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

it.each([false, true])("opens a Cline login link in the Settings Browser with active session: %s", (activeSession) => {
	const openSettingsBrowser = vi.fn();
	const openSessionBrowser = vi.fn();
	const onClose = vi.fn();
	render(
		<AppBrowserLinkContext.Provider value={activeSession ? openSessionBrowser : undefined}>
			<SettingsBrowserLinkContext.Provider value={openSettingsBrowser}>
				<HarnessAuthTerminalPanel workflow={workflow} onClose={onClose} onRetry={vi.fn()} onTerminalState={vi.fn()} />
			</SettingsBrowserLinkContext.Provider>
		</AppBrowserLinkContext.Provider>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Cline device link" }));
	expect(openSettingsBrowser).toHaveBeenCalledExactlyOnceWith(loginUrl);
	expect(openSessionBrowser).not.toHaveBeenCalled();
	expect(onClose).not.toHaveBeenCalled();
});
