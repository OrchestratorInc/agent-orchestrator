import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel } from "../types/conversation";

vi.mock("./useAgentModelsQuery", () => ({
	agentModelsQueryOptions: (agentId: string) => ({
		queryKey: ["agent-models", agentId],
		queryFn: async () => agentId === "claude-code"
			? {
				agentId,
				models: [
					{ id: "claude-sonnet-5-5", label: "Sonnet 5.5", isDefault: true },
					{ id: "claude-opus-5-5", label: "Opus 5.5", requiresVersion: "2.1.280", unavailableReason: "Requires Claude Code 2.1.280 or newer." },
				],
			}
			: { agentId, models: [{ id: "saved-model", label: "Saved Model", isDefault: true }] },
	}),
}));

// Settings-driven narrowing is covered by useOfferedModels' own tests.
vi.mock("./useOfferedModels", () => ({
	useOfferedModels: <T,>(_agentId: string | undefined, models: T[] | undefined) => ({ models: models ?? [], narrowed: false }),
}));

import { useChatModelChoices } from "./useChatModelChoices";

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const live: ChatModel[] = [{ id: "live-model", displayName: "Live Model", default: true }];

describe("useChatModelChoices", () => {
	it("shows the saved catalog until the live list answers, then the live list", async () => {
		const { result, rerender } = renderHook(
			({ models, loaded }: { models: ChatModel[]; loaded: boolean }) =>
				useChatModelChoices("codex", models, undefined, undefined, undefined, loaded),
			{ wrapper, initialProps: { models: [] as ChatModel[], loaded: false } },
		);
		await waitFor(() => expect(result.current.models.map((model) => model.id)).toEqual(["saved-model"]));

		rerender({ models: live, loaded: true });
		expect(result.current.models.map((model) => model.id)).toEqual(["live-model"]);
	});

	it("keeps an answered empty live list empty, so an agent without model choice hides its picker", async () => {
		const { result } = renderHook(() => useChatModelChoices("codex", [], undefined, undefined, undefined, true), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(result.current.models).toEqual([]);
	});

	it("does not add the saved catalog beside a provider's own model option", async () => {
		const options: ChatConfigOption[] = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "acp-model",
			choices: [{ value: "acp-model", name: "ACP Model" }],
		}];
		const { result } = renderHook(() => useChatModelChoices("opencode", [], options, undefined, undefined, false), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(result.current.models).toEqual([]);
	});

	it("marks Claude models the installed CLI is too old for, but never an alias or a cloud chat's", async () => {
		const options: ChatConfigOption[] = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "claude-sonnet-5-5",
			choices: [
				{ value: "opus", name: "Opus" },
				{ value: "claude-opus-5-5", name: "Opus 5.5" },
				{ value: "claude-opus-5-5[1m]", name: "Opus 5.5 (1M context)" },
				{ value: "claude-sonnet-5-5", name: "Sonnet 5.5" },
			],
		}];
		const reasons = (configOptions: ChatConfigOption[] | undefined) =>
			Object.fromEntries((configOptions?.[0]?.choices ?? []).map((choice) => [choice.value, choice.unavailableReason]));

		const local = renderHook(() => useChatModelChoices("claude-code", [], options, undefined), { wrapper });
		await waitFor(() => expect(local.result.current.models.find((model) => model.id === "claude-opus-5-5")?.unavailableReason).toBeTruthy());
		expect(local.result.current.models.find((model) => model.id === "claude-sonnet-5-5")?.unavailableReason).toBeUndefined();
		expect(reasons(local.result.current.configOptions)).toEqual({
			opus: undefined,
			"claude-opus-5-5": "Requires Claude Code 2.1.280 or newer.",
			"claude-opus-5-5[1m]": "Requires Claude Code 2.1.280 or newer.",
			"claude-sonnet-5-5": undefined,
		});

		const cloud = renderHook(() => useChatModelChoices("claude-code", [], options, undefined, undefined, true, false), { wrapper });
		await waitFor(() => expect(cloud.result.current.models.map((model) => model.id)).toContain("claude-opus-5-5"));
		expect(cloud.result.current.models.every((model) => !model.unavailableReason)).toBe(true);
		expect(Object.values(reasons(cloud.result.current.configOptions)).every((reason) => !reason)).toBe(true);
	});
});
