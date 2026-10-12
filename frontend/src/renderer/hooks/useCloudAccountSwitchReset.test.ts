import { QueryClient } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useCloudAccountSwitchReset } from "./useCloudAccountSwitchReset";

// A spawn the previous account's plan refused, as the board's orchestrator
// action leaves it in the mutation cache.
async function refusedSpawn(queryClient: QueryClient, projectId: string) {
	const mutation = queryClient.getMutationCache().build(queryClient, {
		mutationKey: ["project-orchestrator-open", projectId],
		mutationFn: () => Promise.reject(new Error("Choose a plan to use AO Cloud.")),
	});
	await mutation.execute(undefined).catch(() => undefined);
	return mutation;
}

describe("useCloudAccountSwitchReset", () => {
	let queryClient: QueryClient;
	beforeEach(() => {
		queryClient = new QueryClient();
		useUiStore.setState({ orchestratorStartupErrors: {}, orchestratorReplacementErrors: {} });
	});

	function render(initial: { userId: string | null; ready?: boolean; onLocalPage?: boolean }) {
		const leavePage = vi.fn();
		const hook = renderHook(
			(props: { userId: string | null; ready: boolean; onLocalPage: boolean }) =>
				useCloudAccountSwitchReset({ ...props, queryClient, leavePage }),
			{ initialProps: { ready: true, onLocalPage: false, ...initial } },
		);
		return { ...hook, leavePage };
	}

	it("changes nothing for the first known user or a token refresh", async () => {
		await refusedSpawn(queryClient, "project-1");
		const { rerender, leavePage } = render({ userId: "user-a" });
		rerender({ userId: "user-a", ready: true, onLocalPage: false });
		expect(queryClient.getMutationCache().getAll()).toHaveLength(1);
		expect(leavePage).not.toHaveBeenCalled();
	});

	it("drops the previous account's errors and leaves its cloud page on sign-out", async () => {
		await refusedSpawn(queryClient, "project-1");
		useUiStore.setState({ orchestratorStartupErrors: { "project-1": "Choose a plan to use AO Cloud." } });
		const { rerender, leavePage } = render({ userId: "user-a" });
		rerender({ userId: null, ready: true, onLocalPage: false });
		expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
		expect(useUiStore.getState().orchestratorStartupErrors).toEqual({});
		expect(leavePage).toHaveBeenCalledTimes(1);
	});

	it("keeps a local page when another account signs in", async () => {
		await refusedSpawn(queryClient, "project-1");
		const { rerender, leavePage } = render({ userId: "user-a", onLocalPage: true });
		rerender({ userId: "user-b", ready: true, onLocalPage: true });
		expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
		expect(leavePage).not.toHaveBeenCalled();
	});

	it("waits for the session to load and keeps actions still in flight", async () => {
		const pending = queryClient.getMutationCache().build(queryClient, {
			mutationKey: ["project-orchestrator-open", "project-2"],
			mutationFn: () => new Promise(() => undefined),
		});
		void pending.execute(undefined);
		const { rerender, leavePage } = render({ userId: null, ready: false });
		rerender({ userId: "user-a", ready: true, onLocalPage: false });
		expect(leavePage).not.toHaveBeenCalled();
		rerender({ userId: "user-b", ready: true, onLocalPage: false });
		expect(queryClient.getMutationCache().getAll()).toEqual([pending]);
		expect(leavePage).toHaveBeenCalledTimes(1);
	});
});
