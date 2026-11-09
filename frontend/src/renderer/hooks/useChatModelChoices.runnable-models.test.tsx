import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it } from "vitest";
import type { ChatModel } from "../types/conversation";
import { agentModelsQueryKey } from "./useAgentModelsQuery";
import { useChatModelChoices } from "./useChatModelChoices";

afterEach(cleanup);

it("本机保存目录按可运行列表收窄，实时目录和云端目录保持独立", () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
	const catalogKey = agentModelsQueryKey("opencode", "");
	client.setQueryData(catalogKey, {
		agent: "opencode", selectionMode: "catalog",
		models: [
			{ id: "runnable", label: "Runnable" },
			{ id: "unavailable", label: "Unavailable" },
			{ id: "disabled", label: "Disabled" },
			{ id: "saved", label: "Saved" },
		],
		runnableModels: ["runnable", "disabled"],
	});
	client.setQueryData(["settings"], {
		harnessDefaults: { opencode: { disabledModels: ["disabled", "saved"] } },
	});
	const { result, rerender } = renderHook(
		({ models, loaded, local }: { models: ChatModel[]; loaded: boolean; local: boolean }) =>
			useChatModelChoices("opencode", models, undefined, "saved", undefined, loaded, local),
		{
			wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
			initialProps: { models: [] as ChatModel[], loaded: false, local: true },
		},
	);
	// 删除保存目录传入 runnableModels 的代码，此处会多出 unavailable。
	expect(result.current.models.map((model) => model.id)).toEqual(["runnable", "saved"]);

	const live: ChatModel[] = [
		{ id: "runnable", displayName: "Runnable", default: true },
		{ id: "live-only", displayName: "Live only", default: false },
	];
	rerender({ models: live, loaded: true, local: true });
	expect(result.current.models.map((model) => model.id)).toEqual(["runnable", "live-only"]);

	rerender({ models: [], loaded: false, local: false });
	expect(result.current.models.map((model) => model.id)).toEqual(["runnable", "unavailable", "saved"]);
	rerender({ models: live, loaded: true, local: false });
	expect(result.current.models.map((model) => model.id)).toEqual(["runnable", "live-only"]);

	// 保存目录没有模型时实际展示的是 controller 列表，不得套用残留的 runnableModels。
	client.setQueryData(catalogKey, { models: [], runnableModels: ["runnable"] });
	rerender({ models: live, loaded: false, local: true });
	expect(result.current.models.map((model) => model.id)).toEqual(["runnable", "live-only"]);
});
