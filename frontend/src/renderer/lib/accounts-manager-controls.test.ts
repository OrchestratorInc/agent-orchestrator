import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountControlMessage, accountRequestError, changeAccountRemoval, changeSessionAccountSwitch, fetchAccountRemoval, fetchSessionAccountControl, fetchSessionAccountSwitch, readAccountRemovalReferences, saveAccountRemovalReference, startAccountRemoval, startSessionAccountSwitch } from "./accounts-manager-controls";
import type { AccountSwitch, AccountRemoval } from "./accounts-manager-controls";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("./api-client", () => ({ apiClient: api }));
const operation = { id: "switch-a", sessionId: "session-a", provider: "codex", sourceMode: "native", sourceRevision: 1, targetMode: "managed", targetAccountId: "account-a", targetRevision: 0, policy: "drain", phase: "waiting", newConversation: false, recoveryRequired: false };
const success = (data: unknown) => ({ data, response: new Response(null, { status: 200 }) });

describe("account control HTTP boundary", () => {
  beforeEach(() => { vi.resetAllMocks(); localStorage.clear(); });

  it.each(["true", 1, null, {}, []])("rejects malformed retry capability %j", async canRetry => {
    api.GET.mockResolvedValue(success({ ...operation, canRetry }));
    await expect(fetchSessionAccountSwitch("session-a", "switch-a")).rejects.toMatchObject({ status: 502 });
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("accepts an older response without inferring retry capability", async () => {
    api.GET.mockResolvedValue(success(operation));
    expect((await fetchSessionAccountSwitch("session-a", "switch-a")).canRetry).toBeUndefined();
  });

  it("preserves the supported credential-method category without private response fields", () => {
    const code = "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED";
    const error = accountRequestError({ code, requestId: "credential-format-79", message: "private-token", endpoint: "http://127.0.0.1:9999" }, 400);
    expect(error).toMatchObject({ status: 400, code, requestId: "credential-format-79" });
    expect(JSON.stringify(error)).not.toContain("private-token");
    expect(JSON.stringify(error)).not.toContain("127.0.0.1");
  });

  it.each([
    "ACCOUNTS_MANAGER_USAGE_AUTHENTICATION_REQUIRED", "ACCOUNTS_MANAGER_USAGE_ACCESS_DENIED",
    "ACCOUNTS_MANAGER_USAGE_RATE_LIMITED", "ACCOUNTS_MANAGER_USAGE_UNAVAILABLE", "ACCOUNTS_MANAGER_USAGE_RESPONSE_INVALID",
  ])("preserves safe usage category %s without retaining provider details", code => {
    const error = accountRequestError({code,requestId:"usage-79",message:"private-token",endpoint:"http://127.0.0.1:9999"},503);
    expect(error).toMatchObject({code,requestId:"usage-79"});
    expect(JSON.stringify(error)).not.toContain("private-token");
    expect(JSON.stringify(error)).not.toContain("127.0.0.1");
  });

  it.each(["private-token", "__proto__", "constructor"])("drops unrecognized usage category %s", code => {
    expect(accountRequestError({code},503).code).toBe("");
  });

  it.each(["retry", "cancel"] as const)("sends %s without a body and checks operation ownership", async action => {
    api.POST.mockResolvedValue(success(operation));
    await changeSessionAccountSwitch("session-a", "switch-a", action);
    expect(api.POST).toHaveBeenCalledExactlyOnceWith(`/api/v1/sessions/{sessionId}/account-switches/{operationId}/${action}`, { params: { path: { sessionId: "session-a", operationId: "switch-a" } } });
    api.POST.mockResolvedValue(success({ ...operation, sessionId: "foreign-session" }));
    await expect(changeSessionAccountSwitch("session-a", "switch-a", action)).rejects.toMatchObject({ status: 502 });
  });

  it.each(["slot:1", "with.dot", "../foreign", "", "a".repeat(129)])("rejects invalid path identity %j before making any request", async id => {
    api.GET.mockResolvedValue(success({ sessionId: id, mode: "native", revision: 1 }));
    api.POST.mockResolvedValue(success({ ...operation, id }));
    await expect(fetchSessionAccountControl(id)).rejects.toMatchObject({ status: 400 });
    await expect(fetchSessionAccountSwitch("session-a", id)).rejects.toMatchObject({ status: 400 });
    await expect(changeSessionAccountSwitch("session-a", id, "retry")).rejects.toMatchObject({ status: 400 });
    await expect(startSessionAccountSwitch("session-a", { operationId: id, mode: "managed", accountId: "account-a", expectedRevision: 1, policy: "drain" })).rejects.toMatchObject({ status: 400 });
    expect(api.GET).not.toHaveBeenCalled();
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("does not echo endpoints masquerading as a request ID", async () => {
    api.GET.mockResolvedValue({ error: { requestId: "http://127.0.0.1:9988/token", message: "private-token" }, response: new Response(null, { status: 503 }) });
    const error = await fetchSessionAccountControl("session-a").catch(value => value);
    expect(accountControlMessage(error)).not.toContain("127.0.0.1");
    expect(accountControlMessage(error)).not.toContain("private-token");
  });

  const removal = { id: "remove-a", accountId: "account-a", impact: { accountId: "account-a", revision: 0, sessions: [] }, phase: "recovery_required", canCancel: true, recoveryRequired: true };
  it("preserves durable failure codes through the typed client without inventing retry permission", async () => {
    const switchCode: AccountSwitch["errorCode"] = "TARGET_REVALIDATION_UNAVAILABLE";
    const removalCode: AccountRemoval["errorCode"] = "SOURCE_STOP_UNCONFIRMED";
    api.GET.mockResolvedValue(success({ ...operation, errorCode: switchCode, canRetry: false }));
    expect(await fetchSessionAccountSwitch("session-a", "switch-a")).toMatchObject({ errorCode: switchCode, canRetry: false });
    api.GET.mockResolvedValue(success({ ...removal, errorCode: removalCode }));
    expect(await fetchAccountRemoval("account-a", "remove-a")).toMatchObject({ errorCode: removalCode });
  });

  it("keeps accepted and retried failures separate from successful cancellation", async () => {
    const failed = Object.freeze({ ...operation, errorCode: "TARGET_UNAVAILABLE", canRetry: false });
    api.POST.mockResolvedValue(success(failed));
    const body = { operationId: "switch-a", expectedRevision: 1, mode: "managed", accountId: "account-a", policy: "drain" } as const;
    expect(await startSessionAccountSwitch("session-a", body)).toMatchObject({ errorCode: "TARGET_UNAVAILABLE", canRetry: false });
    expect(await changeSessionAccountSwitch("session-a", "switch-a", "retry")).toMatchObject({ errorCode: "TARGET_UNAVAILABLE", canRetry: false });
    api.POST.mockResolvedValue(success({ ...failed, phase: "cancelled" }));
    expect(await changeSessionAccountSwitch("session-a", "switch-a", "cancel")).not.toHaveProperty("errorCode");
    expect(failed.errorCode).toBe("TARGET_UNAVAILABLE");

    const stopped = Object.freeze({ ...removal, errorCode: "SOURCE_STOP_UNCONFIRMED" });
    api.GET.mockResolvedValue(success(stopped));
    api.POST.mockResolvedValue(success(stopped));
    expect(await startAccountRemoval("account-a", { operationId: "remove-a", expectedRevision: 0, confirmed: true })).toMatchObject({ errorCode: "SOURCE_STOP_UNCONFIRMED" });
    expect(await changeAccountRemoval("account-a", "remove-a", "retry")).toMatchObject({ errorCode: "SOURCE_STOP_UNCONFIRMED" });
    api.POST.mockResolvedValue(success({ ...stopped, phase: "cancelled" }));
    expect(await changeAccountRemoval("account-a", "remove-a", "cancel")).not.toHaveProperty("errorCode");
    expect(stopped.errorCode).toBe("SOURCE_STOP_UNCONFIRMED");
  });

  it.each([undefined, "private-token http://127.0.0.1:54321", "__proto__", "constructor", 7, null, { token: "secret" }])("omits unknown durable failure %j without discarding the operation", async errorCode => {
    api.GET.mockResolvedValue(success({ ...operation, errorCode }));
    expect((await fetchSessionAccountSwitch("session-a", "switch-a"))).not.toHaveProperty("errorCode");
    api.GET.mockResolvedValue(success({ ...removal, errorCode }));
    expect((await fetchAccountRemoval("account-a", "remove-a"))).not.toHaveProperty("errorCode");
  });

  it.each(["ready", "cancelled"])("suppresses stale switch diagnostics on %s including nested session state", async phase => {
    const done = { ...operation, phase, errorCode: "TARGET_UNAVAILABLE" };
    api.GET.mockResolvedValue(success(done));
    expect(await fetchSessionAccountSwitch("session-a", "switch-a")).not.toHaveProperty("errorCode");
    api.GET.mockResolvedValue(success({ sessionId: "session-a", provider: "codex", mode: "native", revision: 1, blocked: false, switch: done }));
    expect((await fetchSessionAccountControl("session-a")).switch).not.toHaveProperty("errorCode");
  });

  it.each(["complete", "cancelled"])("suppresses stale removal diagnostics on %s", async phase => {
    api.GET.mockResolvedValue(success({ ...removal, phase, errorCode: "REVOCATION_UNCONFIRMED" }));
    expect(await fetchAccountRemoval("account-a", "remove-a")).not.toHaveProperty("errorCode");
  });
  it.each(["retry", "cancel"] as const)("checks removal account ownership before and after %s", async action => {
    for (const foreign of ["accountId", "id"] as const) {
      api.GET.mockResolvedValue(success({ ...removal, [foreign]: "foreign" }));
      await expect(changeAccountRemoval("account-a", "remove-a", action)).rejects.toMatchObject({ status: 502 });
      expect(api.POST).not.toHaveBeenCalled();
    }
    api.GET.mockResolvedValue(success(removal));
    api.POST.mockResolvedValue(success({ ...removal, accountId: "foreign" }));
    await expect(changeAccountRemoval("account-a", "remove-a", action)).rejects.toMatchObject({ status: 502 });
    expect(api.POST).toHaveBeenCalledExactlyOnceWith(`/api/v1/accounts-manager/removals/{operationId}/${action}`, { params: { path: { operationId: "remove-a" } } });
  });

  it.each([undefined, -1, 0.5, Number.MAX_SAFE_INTEGER + 1])("rejects removal revision %j before mutation", async expectedRevision => {
    await expect(startAccountRemoval("account-a", { operationId: "remove-a", expectedRevision: expectedRevision as number, confirmed: true })).rejects.toMatchObject({ status: 400 });
    expect(api.POST).not.toHaveBeenCalled();
  });

  it("retains explicit zero and rejects an unconfirmed removal", async () => {
    api.POST.mockResolvedValue(success(removal));
    await startAccountRemoval("account-a", { operationId: "remove-a", expectedRevision: 0, confirmed: true });
    await expect(startAccountRemoval("account-a", { operationId: "remove-a", expectedRevision: 0, confirmed: false })).rejects.toMatchObject({ status: 400 });
    expect(api.POST).toHaveBeenCalledOnce();
  });

  it("preserves independent recovery references and never persists unknown secret fields", () => {
    saveAccountRemovalReference("account-a", { accountId: "account-a", operationId: "remove-a" });
    saveAccountRemovalReference("account-b", { accountId: "account-b", operationId: "remove-b" });
    expect(readAccountRemovalReferences()).toHaveLength(2);
    expect(() => saveAccountRemovalReference("account-a", { accountId: "account-a", operationId: "remove-a", token: "private-token" } as never)).toThrow();
    expect(localStorage.getItem("ao:account-removals:v1")).not.toContain("private-token");
    saveAccountRemovalReference("account-a");
    expect(readAccountRemovalReferences()).toEqual([{ accountId: "account-b", operationId: "remove-b" }]);
  });
});
