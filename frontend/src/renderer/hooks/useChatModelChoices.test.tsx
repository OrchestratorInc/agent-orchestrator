import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
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

// Settings-driven narrowing is covered by useOfferedModels' own tests; here only
// which ids a chat's lists are matched by against the switched-off ones.
const settings = vi.hoisted(() => ({ disabled: undefined as string[] | undefined }));
vi.mock("./useOfferedModels", async () => {
	const { filterDisabledModels } = await import("../lib/agent-model-choices");
	return {
		useOfferedModels: <T extends { id: string },>(_agentId: string | undefined, models: T[] | undefined, { keep = [], modelID }: { keep?: ReadonlyArray<string | undefined>; modelID?: (model: T) => string } = {}) =>
			filterDisabledModels(models ?? [], settings.disabled, keep, modelID),
	};
});

import { useChatModelChoices } from "./useChatModelChoices";
import { TurnSettingsBar } from "../components/chat/TurnSettingsBar";

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const live: ChatModel[] = [{ id: "live-model", displayName: "Live Model", default: true }];

describe("useChatModelChoices", () => {
	afterEach(() => { settings.disabled = undefined; });

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

	// The worker's CLI runs Haiku 4.5 for "haiku" where this computer's runs a newer one.
	const cloudList: ChatModel[] = [
		{ id: "sonnet", displayName: "Sonnet 4.6", description: "Sonnet 4.6 · Best for everyday tasks", default: true },
		{ id: "claude-opus-5-5", displayName: "Claude Opus 5.5", default: false },
		{ id: "haiku", displayName: "Haiku 4.5", description: "Haiku 4.5 · Fastest for quick answers", default: false },
	];
	const cloudOptions: ChatConfigOption[] = [{
		id: "model", name: "Model", category: "model", type: "select", currentValue: "sonnet",
		choices: cloudList.map((model) => ({ value: model.id, name: model.displayName })),
	}];
	const choiceIDs = (configOptions: ChatConfigOption[] | undefined) => configOptions?.[0]?.choices.map((choice) => choice.value);

	it("offers a cloud chat's own Claude list, not this computer's catalog, once it has one", async () => {
		const { result } = renderHook(() => useChatModelChoices("claude-code", cloudList, cloudOptions, "sonnet", undefined, true, false), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(result.current.models.map((model) => model.id)).toEqual(["sonnet", "claude-opus-5-5", "haiku"]);
		expect(choiceIDs(result.current.configOptions)).toEqual(["sonnet", "claude-opus-5-5", "haiku"]);
	});

	it("filters a cloud chat's Claude choices by their own ids, as New Task does", async () => {
		// This computer's Sonnet 5.5 is switched off; Cloud's "sonnet" alias is another model.
		settings.disabled = ["claude-sonnet-5-5"];
		const kept = renderHook(() => useChatModelChoices("claude-code", cloudList, cloudOptions, "haiku", undefined, true, false), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(choiceIDs(kept.result.current.configOptions)).toEqual(["sonnet", "claude-opus-5-5", "haiku"]);
		expect(kept.result.current.models.map((model) => model.id)).toContain("sonnet");
		// An id both lists share is the same model, and is left out of both.
		settings.disabled = ["claude-opus-5-5"];
		const hidden = renderHook(() => useChatModelChoices("claude-code", cloudList, cloudOptions, "haiku", undefined, true, false), { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(choiceIDs(hidden.result.current.configOptions)).toEqual(["sonnet", "haiku"]);
		expect(hidden.result.current.models.map((model) => model.id)).toEqual(["sonnet", "haiku"]);
	});

	it("still names and filters a local Claude chat's aliases through this computer's catalog", async () => {
		settings.disabled = ["claude-sonnet-5-5"];
		const options: ChatConfigOption[] = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "opus",
			choices: [{ value: "opus", name: "Opus" }, { value: "sonnet", name: "Sonnet" }],
		}];
		const { result } = renderHook(() => useChatModelChoices("claude-code", [], options, "opus"), { wrapper });
		// The local alias "sonnet" is this computer's Sonnet 5.5, so switching that off hides it.
		await waitFor(() => expect(choiceIDs(result.current.configOptions)).toEqual(["opus"]));
		expect(result.current.models.map((model) => model.id)).toEqual(["claude-opus-5-5"]);
	});

	it("still names a local Claude chat's alias by this computer's newest model of that family", async () => {
		const options: ChatConfigOption[] = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "sonnet",
			choices: [{ value: "opus", name: "Opus" }, { value: "sonnet", name: "Sonnet" }],
		}];
		const { result } = renderHook(() => useChatModelChoices("claude-code", [], options, "sonnet"), { wrapper });
		await waitFor(() => expect(result.current.models.map((model) => model.id)).toEqual(["claude-sonnet-5-5", "claude-opus-5-5"]));
		render(<TurnSettingsBar harness="claude-code" models={result.current.models} settings={{ model: "sonnet" }}
			configOptions={result.current.configOptions} onChangeConfigOption={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Sonnet 5.5");
	});
});
