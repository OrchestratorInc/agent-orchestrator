import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Settings } from "../../../hooks/useSettings";
import { appI18n } from "../../../i18n";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { connectHost, disconnectHost } from "../../../lib/host-clients";
import { HarnessExecutablePanel } from "./HarnessExecutablePanel";

const defaults: Settings = {
	defaultSessionMode: "tui", chatHarnesses: [], client: "", localEnabled: true,
	cloudEnabled: false, cloudControlPlaneUrl: "", trackerIntakeEnabled: false, harnessDefaults: {},
};
let settings: Settings;

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	settings = { ...defaults };
	vi.spyOn(apiClient, "GET").mockImplementation(async () => ({ data: settings }) as never);
});

afterEach(async () => {
	await disconnectHost("box-a");
	await disconnectHost("box-b");
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

/** 使用真实设置 Hook，测试扫描、保存和缓存更新的完整交互。 */
function panel(props: Partial<ComponentProps<typeof HarnessExecutablePanel>> = {}, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const content = (next: typeof props) => <QueryClientProvider client={client}><HarnessExecutablePanel agentId="codex" {...next} /></QueryClientProvider>;
	const view = render(content(props));
	return { client, rerender: (next: typeof props) => view.rerender(content(next)) };
}

it("requires choosing among candidates and applies only the selected CLI", async () => {
	vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/opt/codex");
	const scan = vi.spyOn(apiClient, "POST").mockResolvedValue({ data: {
		directory: "/opt/codex", truncated: false, candidates: [
			{ path: "/opt/codex/bin/codex", source: "directory", version: "1.0" },
			{ path: "/opt/codex/app/codex", source: "desktop", version: "2.0" },
		],
	} } as never);
	const patch = vi.spyOn(apiClient, "PATCH").mockImplementation(async () => {
		settings = { ...settings, harnessDefaults: { codex: { executableDirectory: "/opt/codex", executablePath: "/opt/codex/app/codex" } } };
		return { data: settings } as never;
	});
	const { client } = panel();
	const keys = [["agent-readiness"], ["agent-auth-plans"], ["agent-installers"], ["agent-update-advisory", "local", "codex"], ["agent-models", "codex", ""]];
	keys.forEach((key) => client.setQueryData(key, {}));
	await waitFor(() => expect(screen.getByRole("button", { name: "Browse" })).toBeEnabled());
	await userEvent.click(screen.getByRole("button", { name: "Browse" }));
	expect(screen.getByRole("textbox", { name: "CLI directory" })).toHaveValue("/opt/codex");
	await userEvent.click(screen.getByRole("button", { name: "Scan" }));
	const candidate = await screen.findByRole("radio", { name: /\/opt\/codex\/app\/codex/ });
	expect(scan).toHaveBeenCalledWith("/api/v1/agents/{agent}/executables/scan", { params: { path: { agent: "codex" } }, body: { directory: "/opt/codex" } });
	expect(screen.getByRole("button", { name: "Apply CLI" })).toBeDisabled();
	expect(patch).not.toHaveBeenCalled();
	expect(screen.getByText("Automatic detection")).toBeVisible();
	await userEvent.click(candidate);
	await userEvent.click(screen.getByRole("button", { name: "Apply CLI" }));
	await screen.findByText("Specified path");
	expect(patch).toHaveBeenCalledWith("/api/v1/settings/harness-defaults/{agent}", { params: { path: { agent: "codex" } }, body: { executableDirectory: "/opt/codex", executablePath: "/opt/codex/app/codex" } });
	await waitFor(() => keys.forEach((key) => expect(client.getQueryState(key)?.isInvalidated).toBe(true)));
});

it("keeps the saved CLI when scanning or applying fails and rejects invalid candidates", async () => {
	settings = { ...defaults, harnessDefaults: { codex: { executableDirectory: "/old", executablePath: "/old/codex" } } };
	const scan = vi.spyOn(apiClient, "POST").mockResolvedValueOnce({ error: { error: { message: "Directory unavailable" } } } as never).mockResolvedValueOnce({ data: {
		directory: "/new", truncated: true, candidates: [
			{ path: "/new/broken", source: "directory", error: "Cannot execute" },
			{ path: "/new/codex", source: "directory", version: "2.0" },
		],
	} } as never);
	const patch = vi.spyOn(apiClient, "PATCH").mockResolvedValue({ error: { error: { message: "Executable disappeared" } } } as never);
	panel();
	await screen.findByText("/old/codex");
	const input = screen.getByRole("textbox", { name: "CLI directory" });
	await userEvent.clear(input);
	await userEvent.type(input, "/new");
	await userEvent.click(screen.getByRole("button", { name: "Scan" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("Directory unavailable");
	expect(screen.getByText("/old/codex")).toBeVisible();
	expect(patch).not.toHaveBeenCalled();
	await userEvent.click(screen.getByRole("button", { name: "Scan" }));
	expect(await screen.findByRole("radio", { name: /\/new\/broken/ })).toBeDisabled();
	expect(screen.getByText(/More files were found/)).toBeVisible();
	await userEvent.click(screen.getByRole("radio", { name: /\/new\/codex/ }));
	await userEvent.click(screen.getByRole("button", { name: "Apply CLI" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("Executable disappeared");
	expect(screen.getByText("/old/codex")).toBeVisible();
	expect(screen.getByText("Specified path")).toBeVisible();
	expect(scan).toHaveBeenCalledTimes(2);
});

it("restores automatic detection with two empty fields without clearing model preferences", async () => {
	settings = { ...defaults, harnessDefaults: { codex: { disabledModels: ["model-x"], executableDirectory: "/old", executablePath: "/old/codex" } } };
	const patch = vi.spyOn(apiClient, "PATCH").mockImplementation(async () => {
		settings = { ...settings, harnessDefaults: { codex: { disabledModels: ["model-x"] } } };
		return { data: settings } as never;
	});
	const { client } = panel();
	await screen.findByText("Specified path");
	await userEvent.click(screen.getByRole("button", { name: "Restore automatic detection" }));
	await screen.findByText("Automatic detection");
	expect(patch).toHaveBeenCalledWith("/api/v1/settings/harness-defaults/{agent}", { params: { path: { agent: "codex" } }, body: { executableDirectory: "", executablePath: "" } });
	expect(client.getQueryData<Settings>(["settings"])?.harnessDefaults?.codex.disabledModels).toEqual(["model-x"]);
	expect(screen.getByRole("button", { name: "Restore automatic detection" })).toBeDisabled();
});

it("isolates late scans and saves after switching hosts and browses the selected remote filesystem", async () => {
	vi.spyOn(aoBridge.remotes, "connect").mockResolvedValueOnce({ hostId: "box-a", label: "Box A", url: "http://box-a:3001", base: "http://127.0.0.1:4500/a" }).mockResolvedValueOnce({ hostId: "box-b", label: "Box B", url: "http://box-b:3001", base: "http://127.0.0.1:4500/b" });
	vi.spyOn(aoBridge.remotes, "disconnect").mockResolvedValue(undefined);
	const localPicker = vi.spyOn(aoBridge.app, "chooseDirectory");
	const localPatch = vi.spyOn(apiClient, "PATCH");
	let finishScan!: (response: Response) => void;
	const pendingScan = new Promise<Response>((resolve) => { finishScan = resolve; });
	let finishSave!: (response: Response) => void;
	const pendingSave = new Promise<Response>((resolve) => { finishSave = resolve; });
	const requests: { path: string; method: string; body?: unknown }[] = [];
	let remoteSettings = { ...defaults };
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const request = input instanceof Request ? input : new Request(input);
		const path = new URL(request.url).pathname;
		requests.push({ path, method: request.method, body: request.method === "GET" ? undefined : await request.json() });
		if (path.endsWith("/settings")) return Response.json(path.startsWith("/b/") ? remoteSettings : defaults);
		if (path.endsWith("/fs/dirs")) return Response.json({ path: "/srv/bin", parent: "/srv", entries: [] });
		if (path === "/a/api/v1/agents/codex/executables/scan") return pendingScan;
		if (path.endsWith("/executables/scan")) return Response.json({ directory: "/srv/bin", truncated: false, candidates: [{ path: "/srv/bin/codex", source: "directory", version: "2.0" }] });
		remoteSettings = { ...defaults, harnessDefaults: { codex: { executableDirectory: "/srv/bin", executablePath: "/srv/bin/codex" } } };
		return pendingSave;
	}));
	await connectHost("http://box-a:3001");
	await connectHost("http://box-b:3001");
	const { client, rerender } = panel({ hostId: "box-a" });
	const localKey = ["agent-readiness"];
	const firstHostKey = ["agent-readiness", "box-a"];
	const selectedHostKey = ["agent-readiness", "box-b"];
	[localKey, firstHostKey, selectedHostKey].forEach((key) => client.setQueryData(key, {}));
	await waitFor(() => expect(screen.getByRole("textbox", { name: "CLI directory" })).toBeEnabled());
	await userEvent.type(screen.getByRole("textbox", { name: "CLI directory" }), "/old-host");
	await userEvent.click(screen.getByRole("button", { name: "Scan" }));
	await waitFor(() => expect(requests.some(({ path }) => path === "/a/api/v1/agents/codex/executables/scan")).toBe(true));
	rerender({ hostId: "box-b" });
	await act(async () => finishScan(Response.json({ directory: "/old-host", truncated: false, candidates: [{ path: "/old-host/codex", source: "directory" }] })));
	expect(screen.queryByRole("radio")).not.toBeInTheDocument();
	expect(screen.getByRole("textbox", { name: "CLI directory" })).toHaveValue("");
	await waitFor(() => expect(screen.getByRole("button", { name: "Browse" })).toBeEnabled());
	await userEvent.click(screen.getByRole("button", { name: "Browse" }));
	const picker = await screen.findByRole("dialog");
	const choose = await within(picker).findByRole("button", { name: "Use this folder" });
	await waitFor(() => expect(choose).toBeEnabled());
	await userEvent.click(choose);
	await userEvent.click(screen.getByRole("button", { name: "Scan" }));
	await screen.findByRole("radio", { name: /\/srv\/bin\/codex/ });
	await userEvent.click(screen.getByRole("button", { name: "Apply CLI" }));
	await waitFor(() => expect(requests).toContainEqual({ path: "/b/api/v1/settings/harness-defaults/codex", method: "PATCH", body: { executableDirectory: "/srv/bin", executablePath: "/srv/bin/codex" } }));
	rerender({ hostId: "box-a" });
	await act(async () => finishSave(Response.json(remoteSettings)));
	expect(screen.getByText("Automatic detection")).toBeVisible();
	expect(screen.queryByText("Specified path")).not.toBeInTheDocument();
	expect(client.getQueryData<Settings>(["settings", "box-b"])?.harnessDefaults?.codex.executablePath).toBe("/srv/bin/codex");
	expect(localPicker).not.toHaveBeenCalled();
	expect(localPatch).not.toHaveBeenCalled();
	await waitFor(() => expect(client.getQueryState(selectedHostKey)?.isInvalidated).toBe(true));
	expect(client.getQueryState(localKey)?.isInvalidated).toBe(false);
	expect(client.getQueryState(firstHostKey)?.isInvalidated).toBe(false);
});
