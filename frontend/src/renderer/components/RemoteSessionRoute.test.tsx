import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { setChatDraftBoundary } from "../lib/chat-draft-boundary";
import { refKey } from "../lib/hosts";
import { useUiStore } from "../stores/ui-store";
import { RemoteSessionRoute } from "./RemoteSessionView";

const blocker = vi.hoisted(() => ({
	options: undefined as { disabled: boolean; enableBeforeUnload: boolean; shouldBlockFn: () => boolean } | undefined,
}));

vi.mock("@tanstack/react-router", () => ({ useBlocker: (options: NonNullable<typeof blocker.options>) => { blocker.options = options; } }));
vi.mock("../hooks/useWorkspaceQuery", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useWorkspaceQuery")>(),
	useWorkspaceSession: () => ({ data: undefined, isError: false }),
}));

const draftKey = (host: string) => `remote:${refKey({ host, id: "session-1" })}`;

afterEach(() => {
	setChatDraftBoundary(draftKey("box-a"), "composer", undefined);
	setChatDraftBoundary(draftKey("box-b"), "composer", undefined);
	blocker.options = undefined;
	useUiStore.setState({ remoteHosts: true });
	vi.unstubAllGlobals();
});

function renderRoute() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}>
		<RemoteSessionRoute hostId="box-a" sessionId="session-1" />
	</QueryClientProvider>);
}

it("blocks an unsafe remote draft until the user confirms discarding it", () => {
	setChatDraftBoundary(draftKey("box-a"), "composer", "persistence-failed");
	const confirm = vi.fn().mockReturnValue(false);
	vi.stubGlobal("confirm", confirm);
	renderRoute();

	expect(blocker.options).toMatchObject({ disabled: false, enableBeforeUnload: true });
	expect(blocker.options!.shouldBlockFn()).toBe(true);
	expect(confirm).toHaveBeenCalledWith(expect.stringContaining("discard the unsaved changes"));

	confirm.mockReturnValue(true);
	expect(blocker.options!.shouldBlockFn()).toBe(false);
});

it("does not block Box A for an unsafe draft on Box B's same-named session", () => {
	setChatDraftBoundary(draftKey("box-b"), "composer", "persistence-failed");
	const confirm = vi.fn();
	vi.stubGlobal("confirm", confirm);
	renderRoute();

	expect(blocker.options).toMatchObject({ disabled: true, enableBeforeUnload: false });
	expect(blocker.options!.shouldBlockFn()).toBe(false);
	expect(confirm).not.toHaveBeenCalled();
});

it("keeps remote hosts enabled when an unsafe draft blocks leaving", () => {
	useUiStore.setState({ remoteHosts: true });
	setChatDraftBoundary(draftKey("box-a"), "composer", "persistence-failed");
	vi.stubGlobal("confirm", vi.fn().mockReturnValue(false));
	renderRoute();

	act(() => useUiStore.setState({ remoteHosts: false }));
	act(() => expect(blocker.options!.shouldBlockFn()).toBe(true));
	expect(useUiStore.getState().remoteHosts).toBe(true);
});
