import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { useInitialAccountChoice } from "./useInitialAccountChoice";

const api = vi.hoisted(() => ({ GET: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: api }));

const inventory = () => ({ revision: 1, availability: "ready", stale: false, routing: [], oauthSessions: [], accounts: [
  { id: "account-a", provider: "codex", status: "active", verification: "verified", disabled: false, unavailable: false, quotaSupported: false },
] });
const ok = (data: unknown) => ({ data, response: new Response(null, { status: 200 }) });
const wrapper = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
};

beforeEach(() => {
  vi.resetAllMocks();
  api.GET.mockImplementation(async path => ok(path.endsWith("account-selection") ? { initialSelection: true } : inventory()));
});

it("does not choose a default or require quota access for an explicitly verified account", async () => {
  const { result, rerender } = renderHook(({ choice }) => useInitialAccountChoice("codex", true, choice), { initialProps: { choice: "" }, wrapper: wrapper() });
  await waitFor(() => expect(result.current.accounts).toHaveLength(1));
  expect(result.current.ready).toBe(false);
  rerender({ choice: "managed:account-a" });
  expect(result.current.ready).toBe(true);
  await act(async () => expect(await result.current.confirm()).toEqual({ mode: "managed", accountId: "account-a" }));
});

it.each([false, true])("keeps explicit managed and native choices usable when legacy routing is %s", async enabled => {
  const data = { ...inventory(), routing: [{ provider: "codex", enabled, accountIds: ["account-a"] }] };
  api.GET.mockImplementation(async path => ok(path.endsWith("account-selection") ? { initialSelection: true } : data));
  const { result, rerender } = renderHook(({ choice }) => useInitialAccountChoice("codex", true, choice), { initialProps: { choice: "" }, wrapper: wrapper() });
  await waitFor(() => expect(result.current.accounts).toHaveLength(1));
  expect(result.current.ready).toBe(false);
  rerender({ choice: "managed:account-a" });
  await act(async () => expect(await result.current.confirm()).toEqual({ mode: "managed", accountId: "account-a" }));
  rerender({ choice: "native" });
  await act(async () => expect(await result.current.confirm()).toEqual({ mode: "native" }));
});

it.each(["stale", "disabled", "unverified", "unavailable", "wrong provider"])("rejects %s inventory without a native fallback", async failure => {
  const data = inventory();
  switch (failure) {
    case "stale": data.stale = true; break;
    case "disabled": data.accounts[0].disabled = true; break;
    case "unverified": data.accounts[0].verification = "unverified"; break;
    case "unavailable": data.accounts[0].unavailable = true; break;
    case "wrong provider": data.accounts[0].provider = "other"; break;
  }
  api.GET.mockImplementation(async path => ok(path.endsWith("account-selection") ? { initialSelection: true } : data));
  const { result } = renderHook(() => useInitialAccountChoice("codex", true, "managed:account-a"), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.inventory.isFetched).toBe(true));
  expect(result.current.ready).toBe(false);
  await act(async () => { await expect(result.current.confirm()).rejects.toThrow(); });
});

it("rejects a lost capability with a safe request ID, never observed native success", async () => {
  const { result } = renderHook(() => useInitialAccountChoice("codex", true, "managed:account-a"), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.ready).toBe(true));
  api.GET.mockResolvedValue({ error: { requestId: "capability-request", message: "private-runtime-secret" }, response: new Response(null, { status: 503 }) });
  await act(async () => {
    await expect(result.current.confirm()).rejects.toMatchObject({ requestId: "capability-request", status: 503 });
  });
  await waitFor(() => expect(result.current.ready).toBe(false));
  expect(result.current.capability.error?.message).not.toContain("private-runtime-secret");
});

it("preserves legacy omitted intent only when the daemon explicitly reports no capability", async () => {
  api.GET.mockResolvedValue(ok({ initialSelection: false }));
  const { result, rerender } = renderHook(({ choice }) => useInitialAccountChoice("codex", true, choice), { initialProps: { choice: "" }, wrapper: wrapper() });
  await waitFor(() => expect(result.current.ready).toBe(true));
  await act(async () => expect(await result.current.confirm()).toBeUndefined());
  rerender({ choice: "native" });
  expect(result.current.ready).toBe(false);
  await act(async () => { await expect(result.current.confirm()).rejects.toMatchObject({ status: 501 }); });
});

it("makes no local account requests for cloud tasks", () => {
  const { result, rerender } = renderHook(({ active, harness }) => useInitialAccountChoice(harness, active, ""), { initialProps: { active: false, harness: "codex" }, wrapper: wrapper() });
  expect(result.current.ready).toBe(true);
	rerender({ active: false, harness: "other" });
  expect(result.current.ready).toBe(true);
  expect(api.GET).not.toHaveBeenCalled();
});

it("discovers managed providers while another local harness is selected, without selecting an account", async () => {
  const { result } = renderHook(() => useInitialAccountChoice("other", true, ""), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.managedProviders).toEqual(["codex"]));
  expect(result.current.enabled).toBe(false);
  expect(result.current.accounts).toEqual([]);
  expect(result.current.selected).toBeUndefined();
  await act(async () => expect(await result.current.confirm()).toBeUndefined());
});
