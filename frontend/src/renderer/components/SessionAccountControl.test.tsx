import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionAccountControl } from "./SessionAccountControl";
import userEvent from "@testing-library/user-event";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), inventory: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: api }));
vi.mock("../hooks/useAccountsManagerQuery", () => ({
  useAccountsManagerQuery: () => api.inventory(),
}));

const inventory = { data: {
    revision: 9, availability: "ready", stale: false,
    accounts: [
      { id: "account-a", provider: "codex", label: "Personal", status: "active", verification: "verified", disabled: false, unavailable: false },
      { id: "account-b", provider: "codex", label: "Work", status: "active", verification: "verified", disabled: false, unavailable: false },
    ],
  } };

const binding = { sessionId: "session-a", provider: "codex", mode: "managed", accountId: "account-a", revision: 7, blocked: false };
const operation = { id: "switch-a", sessionId: "session-a", provider: "codex", sourceMode: "managed", sourceAccountId: "account-a", sourceRevision: 7, targetMode: "managed", targetAccountId: "account-b", targetRevision: 0, policy: "drain", phase: "waiting", newConversation: false, recoveryRequired: false, createdAt: "2026-09-28T00:00:00Z", updatedAt: "2026-09-28T00:00:00Z" };
const success = (data: unknown) => ({ data, response: new Response(null, { status: 200 }) });
const missing = () => ({ error: { requestId: "missing-79" }, response: new Response(null, { status: 404 }) });

function mockState(state: typeof binding & { switch?: typeof operation }) {
  api.GET.mockImplementation(async path => path.endsWith("/{operationId}")
    ? state.switch ? success(state.switch) : missing()
    : success(state));
}

let queryClient: QueryClient;

async function chooseMenu(label: string, name: string | RegExp) {
  await userEvent.click(await screen.findByRole("button", { name: label }));
  await userEvent.click(await screen.findByRole("menuitem", { name }));
}

function show() {
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><SessionAccountControl sessionId="session-a" /></QueryClientProvider>);
}

describe("SessionAccountControl", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.inventory.mockReturnValue(inventory);
    localStorage.clear();
    mockState(binding);
  });

  it("hides the managed current target and retains native credentials as an alternate", async () => {
    api.inventory.mockReturnValue({ data: { ...inventory.data, accounts: inventory.data.accounts.slice(0, 1) } });
    show();
    await userEvent.click(await screen.findByRole("button", { name: "Target account" }));
    expect(screen.queryByRole("menuitem", { name: /Personal/ })).not.toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Use native credentials" })).toBeEnabled();
    await userEvent.click(screen.getByRole("menuitem", { name: "Use native credentials" }));
    await chooseMenu("Switch timing", "Wait for the current turn");
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeEnabled();
    expect(screen.queryByText("No other active accounts available")).not.toBeInTheDocument();
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("keeps native available for a managed current target when the inventory is unavailable", async () => {
    api.inventory.mockReturnValue({ data: { ...inventory.data, availability: "unavailable", stale: true }, isError: true });
    show();
    await chooseMenu("Target account", "Use native credentials");
    await chooseMenu("Switch timing", "Wait for the current turn");
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeEnabled();
    expect(screen.queryByText("No other active accounts available")).not.toBeInTheDocument();
  });

  it("hides the native current target and offers eligible managed accounts", async () => {
    mockState({ ...binding, mode: "native", accountId: "" });
    show();
    await userEvent.click(await screen.findByRole("button", { name: "Target account" }));
    expect(screen.queryByRole("menuitem", { name: "Use native credentials" })).not.toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /Personal/ })).toBeEnabled();
    expect(screen.queryByText("No other active accounts available")).not.toBeInTheDocument();
  });

  it.each(["empty", "disabled", "unverified", "unavailable", "inactive", "other provider"])("shows a neutral empty state for native current with %s managed alternatives", async reason => {
    const account = { ...inventory.data.accounts[0] };
    if (reason === "disabled") account.disabled = true;
    if (reason === "unverified") account.verification = "unverified";
    if (reason === "unavailable") account.unavailable = true;
    if (reason === "inactive") account.status = "inactive";
    if (reason === "other provider") account.provider = "other";
    api.inventory.mockReturnValue({ data: { ...inventory.data, accounts: reason === "empty" ? [] : [account] } });
    mockState({ ...binding, mode: "native", accountId: "" });
    show();
    expect(await screen.findByText("No other active accounts available")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Target account" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(api.POST).not.toHaveBeenCalled();
  });

  it.each(["managed", "native"])("clears a %s selection that becomes current after refresh", async mode => {
    show();
    await chooseMenu("Target account", mode === "native" ? "Use native credentials" : /^Work \(account-b\)/);
    await chooseMenu("Switch timing", "Wait for the current turn");
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeEnabled();
    mockState({ ...binding, mode, accountId: mode === "native" ? "" : "account-b", revision: 8 });
    fireEvent.click(screen.getByRole("button", { name: "Refresh account state" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Target account" })).toHaveTextContent("Choose explicitly"));
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Request account switch" }));
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("never resends a saved request whose target is now current", async () => {
    localStorage.setItem("ao:account-switch:session-a", JSON.stringify({ operationId: "saved-current", expectedRevision: 6, mode: "managed", accountId: "account-a", policy: "drain", newConversation: false }));
    show();
    const resend = await screen.findByRole("button", { name: "Resend same switch request" });
    expect(resend).toBeDisabled();
    fireEvent.click(resend);
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("settles a ready no-op response without polling its nonexistent journal", async () => {
    api.POST.mockImplementation(async (_path, input) => {
      mockState({ ...binding, accountId: "account-b", revision: 8 });
      return success({ ...operation, id: input.body.operationId, sourceAccountId: "account-b", sourceRevision: 8, targetRevision: 8, phase: "ready" });
    });
    show();
    await chooseMenu("Target account", /^Work \(account-b\)/);
    await chooseMenu("Switch timing", "Wait for the current turn");
    fireEvent.click(screen.getByRole("button", { name: "Request account switch" }));
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    await waitFor(() => expect(localStorage.getItem("ao:account-switch:session-a")).toBeNull());
    expect(api.GET.mock.calls.filter(([path]) => path.endsWith("/{operationId}"))).toEqual([]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
  });

  it("uses keyboard menus for account and timing without submitting on selection", async () => {
    const user = userEvent.setup();
    show();
    const target = await screen.findByRole("button", { name: "Target account" });
    const timing = screen.getByRole("button", { name: "Switch timing" });
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    target.focus();
    await user.keyboard("{Enter}{End}{Enter}");
    await waitFor(() => expect(target).toHaveFocus());
    expect(target).toHaveTextContent("Work");
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    timing.focus();
    await user.keyboard("{Enter}{Escape}");
    await waitFor(() => expect(timing).toHaveFocus());
    await user.keyboard("{Enter}{Home}{Enter}");
    await waitFor(() => expect(timing).toHaveFocus());
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeEnabled();
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("shows a durable failure after reload and clears it only on authoritative success", async () => {
    const failed = { ...operation, phase: "waiting", errorCode: "TARGET_REVALIDATION_UNAVAILABLE", canRetry: false };
    mockState({ ...binding, switch: failed });
    show();
    const region = await screen.findByRole("region", { name: "Switch operation" });
    expect(region).toHaveTextContent("Error code: TARGET_REVALIDATION_UNAVAILABLE");
    expect(screen.queryByRole("button", { name: "Retry account switch" })).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
    mockState({ ...binding, accountId: "account-b", revision: 8, switch: { ...failed, phase: "ready", targetRevision: 8 } });
    fireEvent.click(screen.getByRole("button", { name: "Refresh account state" }));
    await waitFor(() => expect(region).toHaveTextContent("Phase: ready"));
    expect(region).not.toHaveTextContent("TARGET_REVALIDATION_UNAVAILABLE");
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("requires explicit account and policy and keeps acknowledgement separate from committed state", async () => {
    show();
    expect(await screen.findByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
    const submit = screen.getByRole("button", { name: "Request account switch" });
    expect(submit).toBeDisabled();
    await chooseMenu("Target account", /^Work \(account-b\)/);
    expect(submit).toBeDisabled();
    await chooseMenu("Switch timing", "Wait for the current turn");
    let resolve!: (value: unknown) => void;
    api.POST.mockImplementation((_path, input) => new Promise((done) => {
      resolve = done;
      operation.id = input.body.operationId;
    }));
    fireEvent.click(submit);
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    expect(api.POST.mock.calls[0]).toEqual(["/api/v1/sessions/{sessionId}/account-switches", {
      params: { path: { sessionId: "session-a" } },
      body: { operationId: expect.any(String), mode: "managed", accountId: "account-b", expectedRevision: 7, policy: "drain", newConversation: false },
    }]);
    expect(within(screen.getByRole("region", { name: "Committed account" })).getByText("account-a")).toBeInTheDocument();
    expect(submit).toBeDisabled();
    mockState({ ...binding, switch: operation });
    resolve(success(operation));
    expect(await screen.findByRole("region", { name: "Switch operation" })).toHaveTextContent("waiting");
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
    expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent("account-b");
    expect(screen.queryByText("Account switched")).not.toBeInTheDocument();
    mockState({ ...binding, accountId: "account-b", revision: 8, switch: { ...operation, phase: "ready", targetRevision: 8 } });
    fireEvent.click(screen.getByRole("button", { name: "Refresh account state" }));
    await waitFor(() => expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-b"));
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("8");
    await waitFor(() => expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent("Phase: ready"));
  });

  it("keeps the target selector enabled during a background session refresh", async () => {
    show();
    const selector = await screen.findByLabelText("Target account");
    let resolve!: (value: unknown) => void;
    api.GET.mockImplementation(() => new Promise(done => { resolve = done; }));
    void queryClient.refetchQueries({ queryKey: ["accounts-manager", "session", "session-a"], type: "active" });
    await waitFor(() => expect(api.GET).toHaveBeenCalledTimes(2));
    expect(selector).toBeEnabled();
    resolve(success(binding));
    await waitFor(() => expect(queryClient.isFetching({ queryKey: ["accounts-manager", "session", "session-a"] })).toBe(0));
  });

  it.each([501, 503])("shows HTTP %s as unavailable without implicit native fallback", async (status) => {
    api.GET.mockResolvedValue({ error: { code: "NOT_IMPLEMENTED", requestId: "request-79", message: "private-token http://127.0.0.1:9999/internal" }, response: new Response(null, { status }) });
    show();
    expect(await screen.findByRole("alert")).toHaveTextContent("request-79");
    expect(screen.getByRole("alert")).toHaveTextContent("unavailable");
    expect(screen.queryByText("private-token", { exact: false })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Request account switch" })).not.toBeInTheDocument();
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("rejects stale revisions with the request ID without showing success", async () => {
    api.POST.mockResolvedValue({ error: { code: "ACCOUNTS_MANAGER_CONTROL_CONFLICT", requestId: "stale-79", message: "private-token" }, response: new Response(null, { status: 409 }) });
    show();
    await screen.findByRole("region", { name: "Committed account" });
    await chooseMenu("Target account", "Use native credentials");
    await chooseMenu("Switch timing", "Stop the current turn now");
    fireEvent.click(screen.getByRole("button", { name: "Request account switch" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("stale-79");
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
    expect(api.POST.mock.calls[0][1].body).toMatchObject({ mode: "native", expectedRevision: 7, policy: "interrupt" });
    expect(api.POST.mock.calls[0][1].body).not.toHaveProperty("accountId");
  });

  it("does not let an older terminal snapshot hide a newly accepted pending operation", async () => {
    const oldSwitch = { ...operation, id: "old-switch", phase: "ready", targetRevision: 7 };
    let accepted = oldSwitch;
    api.GET.mockImplementation(async path => success(path.endsWith("/{operationId}") ? accepted : { ...binding, switch: oldSwitch }));
    api.POST.mockImplementation(async (_path, input) => { accepted = { ...operation, id: input.body.operationId }; return success(accepted); });
    show();
    await screen.findByRole("region", { name: "Committed account" });
    await chooseMenu("Target account", /^Work \(account-b\)/);
    await chooseMenu("Switch timing", "Wait for the current turn");
    fireEvent.click(screen.getByRole("button", { name: "Request account switch" }));
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent(api.POST.mock.calls[0][1].body.operationId));
    expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent("waiting");
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
  });

  it("retains an unconfirmed request across remount without issuing a different operation", async () => {
    api.POST.mockRejectedValue(new Error("private-token http://127.0.0.1:9999/internal"));
    const view = show();
    await screen.findByRole("region", { name: "Committed account" });
    await chooseMenu("Target account", "Use native credentials");
    await chooseMenu("Switch timing", "Stop the current turn now");
    fireEvent.click(screen.getByRole("button", { name: "Request account switch" }));
    expect(await screen.findByRole("alert")).not.toHaveTextContent("private-token");
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    const submittedId = api.POST.mock.calls[0][1].body.operationId;
    view.unmount();
    api.GET.mockImplementation(async path => path.endsWith("/{operationId}")
      ? { error: { requestId: "missing-79" }, response: new Response(null, { status: 404 }) }
      : success(binding));
    show();
    await screen.findByRole("region", { name: "Committed account" });
    expect(await screen.findByText(`Unconfirmed operation ID: ${submittedId}`)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    expect(api.POST).toHaveBeenCalledOnce();
    expect(screen.queryByText("cancelled")).not.toBeInTheDocument();
  });

  it("resends only the original request after an authoritative missing-operation read", async () => {
    const intent = { operationId: "saved-switch", expectedRevision: 7, mode: "managed", accountId: "account-b", policy: "drain", newConversation: false };
    localStorage.setItem("ao:account-switch:session-a", JSON.stringify(intent));
    api.POST.mockImplementation(async (_path, input) => {
      mockState({ ...binding, switch: { ...operation, id: input.body.operationId } });
      return success({ ...operation, id: input.body.operationId });
    });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Resend same switch request" }));
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    expect(api.POST.mock.calls[0][1].body).toEqual(intent);
    expect(await screen.findByRole("region", { name: "Switch operation" })).toHaveTextContent("saved-switch");
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
  });

  it("does not let a saved completed intent hide a later pending switch observed by the daemon", async () => {
    const intent = { operationId: "completed-switch", expectedRevision: 7, mode: "managed", accountId: "account-b", policy: "drain", newConversation: false };
    localStorage.setItem("ao:account-switch:session-a", JSON.stringify(intent));
    const completed = { ...operation, id: intent.operationId, phase: "ready", targetRevision: 8 };
    const pending = { ...operation, id: "next-switch", sourceRevision: 8, targetAccountId: "account-a" };
    api.GET.mockImplementation(async (path, input) => success(path.endsWith("/{operationId}")
      ? input.params.path.operationId === completed.id ? completed : pending
      : { ...binding, accountId: "account-b", revision: 8, switch: pending }));
    show();
    await waitFor(() => expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent("next-switch"));
    expect(screen.getByRole("button", { name: "Request account switch" })).toBeDisabled();
    expect(api.POST).not.toHaveBeenCalled();
  });

  it.each([
    ["waiting", "cancel", "Cancel account switch", "cancelled"],
    ["recovery_required", "retry", "Retry account switch", "starting"],
  ])("uses the exact %s operation for %s and observes the returned phase", async (phase, action, label, nextPhase) => {
    const state = { ...operation, phase, canRetry: phase === "recovery_required", recoveryRequired: phase === "recovery_required" };
    mockState({ ...binding, switch: state });
    let resolve!: (value: unknown) => void;
    api.POST.mockImplementation(() => new Promise(done => { resolve = done; }));
    show();
    const control = await screen.findByRole("button", { name: label });
    await waitFor(() => expect(control).toBeEnabled());
    fireEvent.click(control);
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    expect(api.POST.mock.calls[0]).toEqual([`/api/v1/sessions/{sessionId}/account-switches/{operationId}/${action}`, { params: { path: { sessionId: "session-a", operationId: operation.id } } }]);
    expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent(`Phase: ${phase}`);
    expect(control).toBeDisabled();
    const next = { ...operation, phase: nextPhase };
    mockState({ ...binding, switch: next });
    resolve(success(next));
    await waitFor(() => expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent(`Phase: ${nextPhase}`));
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
  });

  it.each(["requested", "waiting"])("offers explicit retry for recovered %s without changing committed state", async phase => {
    const recovered = { ...operation, phase, canRetry: true };
    mockState({ ...binding, switch: recovered });
    api.POST.mockResolvedValue({ error: { code: "ACCOUNTS_MANAGER_CONTROL_CONFLICT", requestId: "retry-race-79" }, response: new Response(null, { status: 409 }) });
    show();
    const retry = await screen.findByRole("button", { name: "Retry account switch" });
    await waitFor(() => expect(retry).toBeEnabled());
    fireEvent.click(retry);
    expect(await screen.findByRole("alert")).toHaveTextContent("retry-race-79");
    expect(api.POST).toHaveBeenCalledExactlyOnceWith("/api/v1/sessions/{sessionId}/account-switches/{operationId}/retry", { params: { path: { sessionId: "session-a", operationId: operation.id } } });
    expect(screen.getByRole("region", { name: "Committed account" })).toHaveTextContent("account-a");
    expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent(operation.id);
    expect(screen.getByRole("button", { name: "Cancel account switch" })).toBeInTheDocument();
  });

  it.each([
    ["waiting", false], ["recovery_required", false], ["recovery_required", undefined],
    ["cancelled", true], ["failed", true], ["ready", true], ["stopping", true],
  ] as const)("does not infer retry for phase %s and capability %s", async (phase, canRetry) => {
    const state = { ...operation, phase, canRetry };
    mockState({ ...binding, switch: state });
    show();
    await screen.findByRole("region", { name: "Switch operation" });
    expect(screen.queryByRole("button", { name: "Retry account switch" })).not.toBeInTheDocument();
    expect(api.POST).not.toHaveBeenCalled();
  });
});
