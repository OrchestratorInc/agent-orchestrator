import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { awakeQueryKey, type AwakeRule, type AwakeState } from "../../hooks/useAwake";
import { setApiBaseUrl } from "../../lib/api-client";
import { TooltipProvider } from "../ui/tooltip";
import { StayAwakeSection } from "./StayAwakeSection";

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	setApiBaseUrl(null);
});

it("preserves post-submit rule edits when an earlier HTTP PUT completes", async () => {
	let server: AwakeState = {
		settings: {
			lidClosed: false, displayAwake: false, stopBatteryPercent: 10,
			lowPowerBatteryPercent: 0, lowPowerOnSeriousHeat: false,
			muteAudio: false, darkenKeyboard: false, restoreDisplay: false,
			pauseApps: [], hotspotName: "", hotspotRecovery: false,
			agentReconnect: false, browserActions: false,
		},
		sessions: [],
		rules: [{ id: "work", name: "Original", enabled: false, match: "all", conditions: [{ kind: "agent_activity", value: "", threshold: 0, start: "", end: "", timezone: "" }], lidClosed: false, suppressed: false }],
		activeRules: [], capabilities: [{ id: "idle", available: true, reason: "" }],
		observation: {
			applications: [], browsers: [], knownNetworks: [], devices: [], networkAddresses: [],
			uptimeSeconds: 0, observedAt: "2026-10-17T12:00:00Z", thermal: "nominal",
			audioControl: false, appPause: false, displayControl: false, keyboardControl: false,
			externalDisplays: 0, lowPowerMode: false, lowPowerControl: false, lowPowerAuthorized: false,
		},
		active: false, lidActive: false, reason: "", paused: false, agentOutcome: "", networkOutcome: "",
	};
	const pending: Array<{ rules: AwakeRule[]; resolve: (response: Response) => void }> = [];
	const response = () => Response.json(server);
	// Keep the component, useAwake, query cache, and typed HTTP client real.
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
		const request = input instanceof Request ? input : new Request(input, init);
		const path = new URL(request.url).pathname;
		if (request.method === "GET" && path === "/api/v1/awake/observations") return response();
		if (request.method === "PUT" && path === "/api/v1/awake/rules") {
			const { rules } = await request.json() as { rules: AwakeRule[] };
			return new Promise<Response>((resolve) => pending.push({ rules, resolve }));
		}
		throw new Error(`Unexpected request: ${request.method} ${path}`);
	}));
	setApiBaseUrl("http://127.0.0.1:3001");
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	try {
		render(<QueryClientProvider client={client}><TooltipProvider><StayAwakeSection /></TooltipProvider></QueryClientProvider>);
		fireEvent.click(screen.getByText("Automatic activation"));
		const name = await screen.findByRole("textbox", { name: "Rule name" });
		const save = screen.getByRole("button", { name: "Save rules" });
		fireEvent.change(name, { target: { value: "Saved version" } });
		fireEvent.click(save);
		await waitFor(() => expect(pending).toHaveLength(1));
		expect(pending[0].rules[0].name).toBe("Saved version");
		fireEvent.change(name, { target: { value: "Newer unsaved edit" } });
		await act(async () => {
			server = { ...server, rules: pending[0].rules };
			pending[0].resolve(response());
		});
		await waitFor(() => expect(client.isMutating({ mutationKey: awakeQueryKey })).toBe(0));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(client.getQueryData<AwakeState>([...awakeQueryKey, "observations"])?.rules[0].name).toBe("Saved version");
		expect(name).toHaveValue("Newer unsaved edit");
		expect(save).toBeEnabled();
		// The next save submits the preserved draft; without newer edits it
		// becomes clean normally, so the fix does not leave Save stuck enabled.
		fireEvent.click(save);
		await waitFor(() => expect(pending).toHaveLength(2));
		expect(pending[1].rules[0].name).toBe("Newer unsaved edit");
		await act(async () => {
			server = { ...server, rules: pending[1].rules };
			pending[1].resolve(response());
		});
		await waitFor(() => expect(client.isMutating({ mutationKey: awakeQueryKey })).toBe(0));
		expect(name).toHaveValue("Newer unsaved edit");
		expect(save).toBeDisabled();
	} finally {
		cleanup();
		client.clear();
	}
});
