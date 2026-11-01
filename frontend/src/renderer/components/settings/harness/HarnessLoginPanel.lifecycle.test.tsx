import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { AgentLoginSnapshot } from "../../../hooks/useAgentAuth";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { clientForHost, connectHost, disconnectHost } from "../../../lib/host-clients";
import { HarnessLoginPanel } from "./HarnessLoginPanel";

afterEach(async () => {
	cleanup();
	await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
	vi.restoreAllMocks();
});

it.each([
	{ hostId: "recovery-remote", method: "browser", opensAutomatically: false, showsLink: false },
	{ hostId: "recovery-remote", method: "device_code", opensAutomatically: true, showsLink: true },
	{ hostId: undefined, method: "browser", opensAutomatically: false, showsLink: true },
	{ hostId: "local", method: "browser", opensAutomatically: false, showsLink: true },
	{ hostId: undefined, method: "device_code", opensAutomatically: true, showsLink: true },
])("restricts browser opening for a recovered $method login on $hostId", async ({ hostId, method, opensAutomatically, showsLink }) => {
	const remote = hostId === "recovery-remote";
	if (remote) {
		vi.spyOn(aoBridge.remotes, "connect").mockResolvedValue({ hostId, label: "Remote recovery", url: "https://recovery.example", base: "http://remote-recovery.test" });
		await connectHost("https://recovery.example");
	}
	const client = remote ? clientForHost(hostId) : apiClient;
	const recovered: AgentLoginSnapshot = {
		id: "recovered-" + method, agentId: "codex", method, status: "running", expiresAt: "2026-11-02T12:00:00Z",
		loginUrl: method === "browser" ? "https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback" : "https://auth.openai.com/codex/device",
		...(method === "device_code" ? { deviceCode: "ABCD-EFGH" } : {}),
	};
	const post = vi.spyOn(client, "POST").mockResolvedValue({ error: { code: "AGENT_AUTH_IN_PROGRESS", details: { operationId: recovered.id } } } as never);
	const get = vi.spyOn(client, "GET").mockResolvedValue({ data: recovered } as never);
	vi.spyOn(client, "DELETE").mockResolvedValue({} as never);
	const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
	try {
		render(<HarnessLoginPanel agentId="codex" methods={[
			{ id: "browser", label: "ChatGPT in browser", secret: false },
			{ id: "device_code", label: "Device code", secret: false },
		]} hostId={hostId} onVerify={vi.fn().mockResolvedValue(true)} onClose={vi.fn()} onTerminal={vi.fn()} />);
		await userEvent.setup().click(screen.getByRole("button", { name: "Device code" }));
		await waitFor(() => expect(screen.getByRole("status")).toBeVisible());
		expect(post).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations", { params: { path: { agent: "codex" } }, body: { method: "device_code" } });
		expect(get).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: "codex", operationId: recovered.id } } });
		if (opensAutomatically) expect(open).toHaveBeenCalledExactlyOnceWith(recovered.loginUrl);
		else expect(open).not.toHaveBeenCalled();
		if (showsLink) expect(screen.getByRole("button", { name: "Open sign-in page" })).toBeVisible();
		else expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
	} finally {
		cleanup();
		await act(async () => { await Promise.resolve(); });
		if (remote) await disconnectHost(hostId);
	}
});

it.each(["old-login", "recovered-login"])("finishes stale startup and disposal before a reopened panel recovers %s", async (operationId) => {
	const old: AgentLoginSnapshot = { id: "old-login", agentId: "codex", method: "browser", status: "running", expiresAt: "2026-10-27T12:00:00Z" };
	const recovered = { ...old, id: operationId, loginUrl: "https://auth.openai.com/oauth/authorize" };
	let finishStart!: (value: unknown) => void;
	let finishStop!: () => void;
	const started = new Promise<never>((resolve) => { finishStart = (value) => resolve(value as never); });
	const stopped = new Promise<never>((resolve) => { finishStop = () => resolve({} as never); });
	const post = vi.spyOn(apiClient, "POST").mockReturnValueOnce(started).mockResolvedValue({ error: { code: "AGENT_AUTH_IN_PROGRESS", details: { operationId: recovered.id } } } as never);
	const remove = vi.spyOn(apiClient, "DELETE").mockReturnValueOnce(stopped).mockResolvedValue({} as never);
	const get = vi.spyOn(apiClient, "GET").mockResolvedValue({ data: recovered } as never);
	const props = { agentId: "codex" as const, methods: [{ id: "browser", label: "ChatGPT in browser", secret: false }], onVerify: vi.fn().mockResolvedValue(true), onClose: vi.fn(), onTerminal: vi.fn() };
	const user = userEvent.setup();
	try {
		const first = render(<HarnessLoginPanel {...props} />);
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		first.unmount();
		render(<HarnessLoginPanel {...props} hostId="local" />);
		await user.click(screen.getByRole("button", { name: "ChatGPT in browser" }));
		expect(post).toHaveBeenCalledTimes(1);
		await act(async () => finishStart({ data: old }));
		await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
		expect(post).toHaveBeenCalledTimes(1);
		expect(get).not.toHaveBeenCalled();
		await act(async () => finishStop());
		await waitFor(() => expect(get).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: "codex", operationId: recovered.id } } }));
		expect(await screen.findByRole("button", { name: "Open sign-in page" })).toBeVisible();
		expect(remove).toHaveBeenCalledTimes(1);
		expect(remove).toHaveBeenCalledWith("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: "codex", operationId: old.id } } });
		get.mockResolvedValue({ data: { ...recovered, status: "succeeded" } } as never);
		expect(await screen.findByText("Connected", {}, { timeout: 3_000 })).toBeVisible();
		expect(props.onVerify).toHaveBeenCalledTimes(1);
		expect(remove).toHaveBeenCalledTimes(1);
		expect(screen.queryByText("Sign-in cancelled")).not.toBeInTheDocument();
	} finally {
		finishStart({ data: old });
		finishStop();
	}
});
