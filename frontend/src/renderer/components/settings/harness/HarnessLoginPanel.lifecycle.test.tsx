import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { AgentLoginSnapshot } from "../../../hooks/useAgentAuth";
import { apiClient } from "../../../lib/api-client";
import { HarnessLoginPanel } from "./HarnessLoginPanel";

afterEach(async () => {
	cleanup();
	await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
	vi.restoreAllMocks();
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
