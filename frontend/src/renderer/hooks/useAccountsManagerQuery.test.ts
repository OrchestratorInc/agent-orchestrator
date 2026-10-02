import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createElement } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AccountsManagerSnapshot } from "./useAccountsManagerQuery";
import { accountsManagerQueryKey, cancelAccountsManagerOAuth, refreshAccountsManagerAccount, selectAccountsManagerSnapshot, setAccountsManagerDisabled, useAccountsManagerEvents, useAccountsManagerQuery } from "./useAccountsManagerQuery";
const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn(), PATCH: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: api, getApiBaseUrl: () => "http://127.0.0.1:9999", apiErrorMessage: (error: { message: string }) => error.message }));

function snapshot(
  revision: number,
  accountCount: number,
): AccountsManagerSnapshot {
  return {
    revision,
    availability: "ready",
    stale: false,
    accounts: Array.from({ length: accountCount }, (_, index) => ({
      id: `account-${index}`,
      provider: "codex",
      kind: "oauth",
      generation: 1,
      reconnectSupported: true,
      status: "active",
      disabled: false,
      unavailable: false,
      quotaSupported: false,
      cooldowns: [],
    })),
    oauthSessions: [],
    routing: [],
  };
}

describe("selectAccountsManagerSnapshot", () => {
  it("accepts an authoritative snapshot when an unsafe int64 revision rounds to the same JavaScript number", () => {
    const current = snapshot(1_789_925_704_937_854_010, 0);
    const incoming = snapshot(1_789_925_704_937_854_011, 1);

    expect(selectAccountsManagerSnapshot(current, incoming)).toBe(incoming);
  });
});

describe("managed inventory public boundary", () => {
  beforeEach(() => vi.resetAllMocks());

  it("refreshes cached inventory when account controls mount again", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(accountsManagerQueryKey, snapshot(1, 1));
    api.GET.mockResolvedValue({ data: snapshot(2, 0), response: new Response(null, { status: 200 }) });
    const { result } = renderHook(() => useAccountsManagerQuery(), { wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children) });
    await waitFor(() => expect(result.current.data?.revision).toBe(2));
    expect(result.current.data?.accounts).toHaveLength(0);
  });

  it("preserves a refresh error request ID without exposing server details", async () => {
    api.POST.mockResolvedValue({ error: { requestId: "refresh-79", message: "private-token http://127.0.0.1:9999" }, response: new Response(null, { status: 503 }) });
    const error = await refreshAccountsManagerAccount("account-a").catch(value => value);
    expect(error).toMatchObject({ requestId: "refresh-79", status: 503 });
    expect(error.message).not.toContain("private-token");
    expect(error.message).not.toContain("127.0.0.1");
  });

  it("does not overwrite a newer event snapshot when a mount refresh returns late", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(accountsManagerQueryKey, snapshot(1, 1));
    let complete!: (response: unknown) => void;
    api.GET.mockImplementation(() => new Promise(resolve => { complete = resolve; }));
    const { result } = renderHook(() => useAccountsManagerQuery(), { wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children) });
    await waitFor(() => expect(api.GET).toHaveBeenCalledOnce());
    await act(async () => {
      client.setQueryData(accountsManagerQueryKey, snapshot(3, 0));
      complete({ data: snapshot(2, 1), response: new Response(null, { status: 200 }) });
    });
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    expect(result.current.data?.revision).toBe(3);
    expect(result.current.data?.accounts).toHaveLength(0);
  });

  it.each([
    ["distinct large int64", 1_789_925_704_937_855_000, 1_789_925_704_937_856_000],
    ["rounded-equal int64", 1_789_925_704_937_854_010, 1_789_925_704_937_854_011],
  ])("preserves a newer event during a delayed mount read with %s revisions", async (_name, readRevision, eventRevision) => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(accountsManagerQueryKey, snapshot(1_789_925_704_937_854_000, 1));
    let complete!: (response: unknown) => void;
    api.GET.mockImplementation(() => new Promise(resolve => { complete = resolve; }));
    const { result, unmount } = renderHook(() => useAccountsManagerQuery(), { wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children) });
    await waitFor(() => expect(api.GET).toHaveBeenCalledOnce());
    await act(async () => {
      client.setQueryData(accountsManagerQueryKey, snapshot(eventRevision, 0));
      complete({ data: snapshot(readRevision, 1), response: new Response(null, { status: 200 }) });
    });
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    expect.soft(result.current.data?.revision).toBe(eventRevision);
    expect.soft(result.current.data?.accounts).toHaveLength(0);
    unmount();
    client.clear();
  });

  it("treats bodyless login DELETE as acknowledgement without inventing a terminal status", async () => {
    api.DELETE.mockResolvedValue({ response: new Response(null, { status: 204 }) });
    expect(await cancelAccountsManagerOAuth("pruned-operation")).toBeUndefined();
  });
});

describe("inventory read causality", () => {
  class TestStream extends EventTarget {
    static instances: TestStream[] = [];
    onerror: (() => void) | null = null;
    close = vi.fn();
    constructor() {
      super();
      TestStream.instances.push(this);
    }
    send(data: AccountsManagerSnapshot) {
      this.dispatchEvent(new MessageEvent("accounts_manager", { data: JSON.stringify(data) }));
    }
  }

  beforeEach(() => {
    vi.resetAllMocks();
    TestStream.instances = [];
    vi.stubGlobal("EventSource", TestStream);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const revisions = [
    { width: "distinct large int64", older: 1_789_925_704_937_855_000, newer: 1_789_925_704_937_856_000 },
    { width: "rounded-equal int64", older: 1_789_925_704_937_854_010, newer: 1_789_925_704_937_854_011 },
  ];
  const schedules = revisions.flatMap(revision =>
    (["mount", "focus", "reconnect"] as const).flatMap(trigger =>
      (["event", "mutation receipt"] as const).map(writer => ({ ...revision, trigger, writer }))),
  );

  it.each(schedules)("fences $trigger reads against a $writer with $width revisions and accepts the next read", async ({ older, newer, trigger, writer }) => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const initial = snapshot(1_789_925_704_937_854_000, 2);
    initial.routing = [{ provider: "codex", enabled: true, accountIds: ["account-1"] }];
    client.setQueryData(accountsManagerQueryKey, initial);
    const olderData = { ...initial, revision: older };
    const update = snapshot(newer, 1);
    update.accounts[0].disabled = true;
    update.accounts[0].status = "disabled";
    update.routing = [{ provider: "codex", enabled: false, accountIds: [] }];
    let complete!: (response: unknown) => void;
    if (trigger !== "mount") api.GET.mockResolvedValueOnce({ data: initial });
    api.GET.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
    const { result, unmount } = renderHook(() => {
      useAccountsManagerEvents();
      return useAccountsManagerQuery();
    }, { wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children) });

    if (trigger !== "mount") {
      await waitFor(() => expect(result.current.isFetching).toBe(false));
      await act(async () => {
        if (trigger === "focus") window.dispatchEvent(new Event("focus"));
        else {
          vi.useFakeTimers();
          TestStream.instances[0].onerror?.();
          await vi.advanceTimersByTimeAsync(1_000);
          vi.useRealTimers();
        }
      });
    }
    await waitFor(() => expect(api.GET).toHaveBeenCalledTimes(trigger === "mount" ? 1 : 2));
    await act(async () => {
      if (writer === "event") TestStream.instances[0].send(update);
      else {
        api.PATCH.mockResolvedValue({ data: update });
        const receipt = await setAccountsManagerDisabled("account-0", true);
        client.setQueryData<AccountsManagerSnapshot>(accountsManagerQueryKey, current => selectAccountsManagerSnapshot(current, receipt));
      }
      complete({ data: olderData });
    });
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    await waitFor(() => expect(result.current.data).toEqual(client.getQueryData(accountsManagerQueryKey)));
    expect.soft(client.getQueryData(accountsManagerQueryKey)).toEqual(update);
    expect.soft(result.current.data).toEqual(update);
    if (trigger === "reconnect") expect(TestStream.instances).toHaveLength(2);

    const subsequent = snapshot(newer, 0);
    api.GET.mockResolvedValueOnce({ data: subsequent });
    await act(async () => { await result.current.refetch(); });
    await waitFor(() => expect(result.current.data).toEqual(subsequent));
    expect.soft(client.getQueryData(accountsManagerQueryKey)).toEqual(subsequent);
    unmount();
    client.clear();
  });

  it("checks causality at commit after the query function has returned", async () => {
    const client = new QueryClient();
    const initial = snapshot(1_789_925_704_937_854_010, 1);
    const update = snapshot(1_789_925_704_937_854_011, 0);
    client.setQueryData(accountsManagerQueryKey, initial);
    const completions: Array<(response: unknown) => void> = [];
    api.GET.mockImplementation(() => new Promise(resolve => { completions.push(resolve); }));
    const { result, unmount } = renderHook(() => useAccountsManagerQuery(), {
      wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children),
    });
    const query = client.getQueryCache().find({ queryKey: accountsManagerQueryKey, exact: true })!;
    let queryReturned = false;
    const queryFn = query.options.queryFn;
    expect(typeof queryFn).toBe("function");
    query.setOptions({ ...query.options, queryFn: async context => {
      const value = await (queryFn as Exclude<typeof queryFn, symbol | undefined>)(context);
      queryReturned = true;
      client.setQueryData(accountsManagerQueryKey, update);
      return value;
    } });
    // Restart with the wrapper to place the update after the real query function.
    await act(async () => { await client.cancelQueries({ queryKey: accountsManagerQueryKey }); });
    let refetch!: Promise<unknown>;
    await act(async () => {
      refetch = query.fetch();
      expect(completions).toHaveLength(2);
      completions[1]({ data: initial });
      await refetch;
      completions[0]({ data: initial });
    });
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    expect(queryReturned).toBe(true);
    expect(client.getQueryData(accountsManagerQueryKey)).toEqual(update);
    unmount();
    client.clear();
  });

  it("fences a focus read even when an applied write retains the same cached object", async () => {
    const client = new QueryClient();
    const current = snapshot(1_789_925_704_937_854_011, 0);
    client.setQueryData(accountsManagerQueryKey, current);
    let complete!: (response: unknown) => void;
    api.GET.mockImplementation(() => new Promise(resolve => { complete = resolve; }));
    const { unmount } = renderHook(() => useAccountsManagerEvents(), {
      wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children),
    });
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    await act(async () => {
      client.setQueryData(accountsManagerQueryKey, current);
      expect(client.getQueryData(accountsManagerQueryKey)).toBe(current);
      complete({ data: snapshot(1_789_925_704_937_854_010, 1) });
    });
    await waitFor(() => expect(client.isFetching()).toBe(0));
    expect(client.getQueryData(accountsManagerQueryKey)).toBe(current);
    unmount();
    client.clear();
  });

  it("shares the fence between observers without rejecting an unrelated client's read", async () => {
    const clients = [new QueryClient(), new QueryClient()];
    for (const client of clients) client.setQueryData(accountsManagerQueryKey, snapshot(1_789_925_704_937_854_010, 1));
    const completions: Array<(response: unknown) => void> = [];
    api.GET.mockImplementation(() => new Promise(resolve => { completions.push(resolve); }));
    const hooks = clients.map(client => renderHook(() => [useAccountsManagerQuery(), useAccountsManagerQuery()], {
      wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children),
    }));
    await waitFor(() => expect(api.GET).toHaveBeenCalledTimes(2));
    const update = snapshot(1_789_925_704_937_854_011, 0);
    const response = snapshot(1_789_925_704_937_854_010, 2);
    await act(async () => {
      clients[0].setQueryData(accountsManagerQueryKey, update);
      completions.forEach(complete => complete({ data: response }));
    });
    await waitFor(() => expect(clients.every(client => client.isFetching() === 0)).toBe(true));
    expect(clients[0].getQueryData(accountsManagerQueryKey)).toEqual(update);
    expect(clients[1].getQueryData(accountsManagerQueryKey)).toEqual(response);
    hooks.forEach(hook => hook.unmount());
    clients.forEach(client => client.clear());
  });

  it("does not resurrect a cleared query or apply an event after cleanup", async () => {
    const client = new QueryClient();
    client.setQueryData(accountsManagerQueryKey, snapshot(1_789_925_704_937_854_010, 1));
    let complete!: (response: unknown) => void;
    api.GET.mockImplementation(() => new Promise(resolve => { complete = resolve; }));
    const { unmount } = renderHook(() => {
      useAccountsManagerEvents();
      return useAccountsManagerQuery();
    }, { wrapper: ({ children }) => createElement(QueryClientProvider, { client }, children) });
    await waitFor(() => expect(api.GET).toHaveBeenCalledOnce());
    unmount();
    client.clear();
    const replacement = snapshot(1_789_925_704_937_854_011, 0);
    await act(async () => {
      client.setQueryData(accountsManagerQueryKey, replacement);
      complete({ data: snapshot(1_789_925_704_937_854_010, 1) });
      TestStream.instances[0].send(snapshot(1_789_925_704_937_854_010, 2));
    });
    expect(TestStream.instances[0].close).toHaveBeenCalledOnce();
    expect(client.getQueryData(accountsManagerQueryKey)).toEqual(replacement);
    expect(client.isFetching()).toBe(0);
    client.clear();
  });
});
