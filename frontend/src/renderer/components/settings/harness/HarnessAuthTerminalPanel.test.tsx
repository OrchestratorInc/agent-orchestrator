import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppBrowserLinkContext } from "../../AppLink";
import { SettingsBrowserLinkContext } from "../SettingsBrowserOverlay";
import { HarnessAuthTerminalPanel, type AuthTerminalWorkflow } from "./HarnessAuthTerminalPanel";

const loginUrl = "https://app.cline.bot/auth/device";
const terminalProps = vi.hoisted(() => ({ suppressDimInLightTheme: undefined as boolean | undefined }));

vi.mock("../../TerminalPane", () => ({
	TerminalPane: ({ onLinkOpen, suppressDimInLightTheme }: { onLinkOpen?: (url: string) => void; suppressDimInLightTheme?: boolean }) => {
		terminalProps.suppressDimInLightTheme = suppressDimInLightTheme;
		return <button onClick={() => onLinkOpen?.(loginUrl)} type="button">Cline device link</button>;
	},
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

describe("HarnessAuthTerminalPanel", () => {
	it.each([
		["cline", "login", true],
		["cline", "logout", false],
		["codex", "login", false],
	] as const)("scopes dim suppression for %s %s", (agentId, action, expected) => {
		render(<HarnessAuthTerminalPanel workflow={{ ...workflow, agentId, action }} onClose={vi.fn()} onRetry={vi.fn()} onTerminalState={vi.fn()} />);
		expect(terminalProps.suppressDimInLightTheme).toBe(expected);
	});
});