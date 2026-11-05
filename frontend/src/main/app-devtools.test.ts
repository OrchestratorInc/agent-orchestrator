import { describe, expect, it, vi } from "vitest";
import { reloadAppView, toggleAppDevTools } from "./app-devtools";

describe("toggleAppDevTools", () => {
	it.each([true, false])("keeps the shell untouched when Browser DevTools are open=%s", async (open) => {
		const browserHost = {
			toggleDevToolsForLastFocused: vi.fn().mockResolvedValue({
				viewId: "view-1", activeTabId: "tab-1", open,
			}),
		};
		const shell = { toggleDevTools: vi.fn() };

		await toggleAppDevTools(browserHost, () => shell, true);

		expect(browserHost.toggleDevToolsForLastFocused).toHaveBeenCalledOnce();
		expect(shell.toggleDevTools).not.toHaveBeenCalled();
	});

	it.each(["no host", "no focused view", "unavailable view"])("toggles shell DevTools once with %s", async (scenario) => {
		const browserHost = scenario === "no host" ? null : {
			toggleDevToolsForLastFocused: scenario === "no focused view"
				? vi.fn().mockResolvedValue(null)
				: vi.fn().mockRejectedValue(new Error("Browser view unavailable")),
		};
		const shell = { toggleDevTools: vi.fn() };

		await toggleAppDevTools(browserHost, () => shell, true);

		expect(shell.toggleDevTools).toHaveBeenCalledOnce();
	});

	it("tolerates the shell closing while the Browser operation is pending", async () => {
		let finish: (state: null) => void = () => undefined;
		const browserHost = {
			toggleDevToolsForLastFocused: () => new Promise<null>((resolve) => { finish = resolve; }),
		};
		let shell: { toggleDevTools: () => void } | null = { toggleDevTools: vi.fn() };
		const toggle = toggleAppDevTools(browserHost, () => shell, true);

		shell = null;
		finish(null);

		await expect(toggle).resolves.toBeUndefined();
	});
});


describe("packaged app view actions", () => {
	it.each(["no host", "no focused view", "unavailable view"])("never falls back to shell DevTools with %s", async (scenario) => {
		const browserHost = scenario === "no host" ? null : {
			toggleDevToolsForLastFocused: scenario === "no focused view"
				? vi.fn().mockResolvedValue(null)
				: vi.fn().mockRejectedValue(new Error("Browser view unavailable")),
		};
		const shell = { toggleDevTools: vi.fn() };
		await toggleAppDevTools(browserHost, () => shell);
		expect(shell.toggleDevTools).not.toHaveBeenCalled();
	});

	it("keeps browser DevTools available in packaged builds", async () => {
		const browserHost = { toggleDevToolsForLastFocused: vi.fn().mockResolvedValue({ open: true }) };
		const shell = { toggleDevTools: vi.fn() };
		await toggleAppDevTools(browserHost as never, () => shell);
		expect(browserHost.toggleDevToolsForLastFocused).toHaveBeenCalledOnce();
		expect(shell.toggleDevTools).not.toHaveBeenCalled();
	});

	it.each([false, true])("blocks shell reload with ignoreCache=%s", (ignoreCache) => {
		const shell = { reload: vi.fn(), reloadIgnoringCache: vi.fn() };
		reloadAppView(null, () => shell, false, ignoreCache);
		expect(shell.reload).not.toHaveBeenCalled();
		expect(shell.reloadIgnoringCache).not.toHaveBeenCalled();
	});

	it.each([false, true])("allows shell reload in development with ignoreCache=%s", (ignoreCache) => {
		const shell = { reload: vi.fn(), reloadIgnoringCache: vi.fn() };
		reloadAppView(null, () => shell, true, ignoreCache);
		expect(ignoreCache ? shell.reloadIgnoringCache : shell.reload).toHaveBeenCalledOnce();
	});

	it.each([false, true])("reloads browser pages in packaged builds with ignoreCache=%s", (ignoreCache) => {
		const page = { reload: vi.fn(), reloadIgnoringCache: vi.fn() };
		const shell = { reload: vi.fn(), reloadIgnoringCache: vi.fn() };
		const browserHost = { getLastFocusedPanelContents: () => page, reloadAgentTab: vi.fn().mockReturnValue(false) };
		reloadAppView(browserHost as never, () => shell, false, ignoreCache);
		expect(ignoreCache ? page.reloadIgnoringCache : page.reload).toHaveBeenCalledOnce();
		expect(shell.reload).not.toHaveBeenCalled();
		expect(shell.reloadIgnoringCache).not.toHaveBeenCalled();
	});

	it("reloads an agent's page normally and its viewer only on force reload", () => {
		const page = { reload: vi.fn(), reloadIgnoringCache: vi.fn() };
		const browserHost = { getLastFocusedPanelContents: () => page, reloadAgentTab: vi.fn().mockReturnValue(true) };
		reloadAppView(browserHost as never, () => null);
		expect(browserHost.reloadAgentTab).toHaveBeenCalledOnce();
		expect(page.reload).not.toHaveBeenCalled();
		reloadAppView(browserHost as never, () => null, false, true);
		expect(browserHost.reloadAgentTab).toHaveBeenCalledOnce();
		expect(page.reloadIgnoringCache).toHaveBeenCalledOnce();
	});
});
