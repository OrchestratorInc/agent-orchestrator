import type { components } from "../../api/schema";
import type { TFunction } from "i18next";
import type { MessageKey } from "../i18n";
import { apiClient } from "./api-client";

export type SessionAccountState = components["schemas"]["AccountsManagerSessionResponse"];
export type AccountSwitch = components["schemas"]["AccountsManagerSwitchResponse"];
export type AccountSwitchRequest = components["schemas"]["AccountsManagerSwitchRequest"];
export type AccountRemoval = components["schemas"]["AccountsManagerRemovalResponse"];
export type AccountRemovalImpact = components["schemas"]["AccountsManagerRemovalImpactResponse"];
export type AccountRemovalRequest = components["schemas"]["AccountsManagerRemovalRequest"];
export type AccountRemovalReference = { accountId: string; operationId: string; request?: AccountRemovalRequest };

const operationErrorCodes = {
  ADMISSION_CHANGED: true,
  SOURCE_CHANGED: true,
  NATIVE_HISTORY_UNAVAILABLE: true,
  SOURCE_INTAKE_UNAVAILABLE: true,
  SOURCE_OWNERSHIP_UNCONFIRMED: true,
  SOURCE_NOT_QUIESCENT: true,
  TARGET_UNAVAILABLE: true,
  TARGET_REVALIDATION_UNAVAILABLE: true,
  REVOCATION_UNCONFIRMED: true,
  SOURCE_STOP_UNCONFIRMED: true,
  STOP_RECORD_UNCONFIRMED: true,
  BINDING_COMMIT_UNCONFIRMED: true,
  BINDING_SYNC_UNCONFIRMED: true,
  START_RECORD_UNCONFIRMED: true,
  SESSION_UNAVAILABLE: true,
  PROJECT_UNAVAILABLE: true,
  TARGET_START_UNCONFIRMED: true,
  TARGET_NOT_READY: true,
  OUTCOME_UNCONFIRMED: true,
  DAEMON_RESTARTED: true,
  CONTROLLER_CHANGED: true,
  TARGET_STOP_UNCONFIRMED: true,
  RETRY_COMMIT_UNCONFIRMED: true,
  SESSION_INTAKE_UNAVAILABLE: true,
  STOP_ADMISSION_CHANGED: true,
  REVOCATION_RECORD_UNCONFIRMED: true,
  FINAL_ADMISSION_CHANGED: true,
  CREDENTIAL_REMOVAL_UNCONFIRMED: true,
} satisfies Record<NonNullable<AccountSwitch["errorCode"] | AccountRemoval["errorCode"]>, true>;

function safeOperationFailure<T extends AccountSwitch | AccountRemoval>(operation: T): T {
  const { errorCode, ...state } = operation;
  if (!["ready", "complete", "cancelled"].includes(operation.phase) && typeof errorCode === "string" && Object.hasOwn(operationErrorCodes, errorCode)) {
    return { ...state, errorCode } as T;
  }
  return state as T;
}


const usageErrorKeys: Record<string, MessageKey> = {
  ACCOUNTS_MANAGER_USAGE_AUTHENTICATION_REQUIRED: "accountsManager.usage.authenticationRequired",
  ACCOUNTS_MANAGER_USAGE_ACCESS_DENIED: "accountsManager.usage.accessDenied",
  ACCOUNTS_MANAGER_USAGE_RATE_LIMITED: "accountsManager.usage.rateLimited",
  ACCOUNTS_MANAGER_USAGE_UNAVAILABLE: "accountsManager.usage.serviceUnavailable",
  ACCOUNTS_MANAGER_USAGE_RESPONSE_INVALID: "accountsManager.usage.responseInvalid",
};
const accountErrorCodes = new Set([
  "ACCOUNTS_MANAGER_INVALID_CREDENTIAL", "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED", "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE", ...Object.keys(usageErrorKeys),
]);

export class AccountControlError extends Error {
  constructor(public readonly status: number, public readonly requestId = "", public readonly code = "") {
    const detail = status === 409
      ? "Account state changed. Refresh before trying again."
      : status === 404
        ? "The account operation was not found. Refresh its state."
        : status === 501 || status === 503 || status === 0
          ? "Account controls are unavailable on this daemon."
          : "The account operation could not be confirmed. Refresh its state.";
    super(detail + (requestId ? ` Request ID: ${requestId}` : ""));
  }
}

export function accountControlMessage(error: unknown, t?: TFunction): string {
  const safe = error instanceof AccountControlError ? error : new AccountControlError(0);
  if (!t) return safe.message;
  const usageKey = Object.hasOwn(usageErrorKeys, safe.code) ? usageErrorKeys[safe.code] : undefined;
  const detail = t(usageKey ?? (safe.status === 409 ? "accountsManager.controls.errorChanged"
    : safe.status === 404 ? "accountsManager.controls.errorMissing"
      : [0, 501, 503].includes(safe.status) ? "accountsManager.controls.errorUnavailable"
        : "accountsManager.controls.errorUnconfirmed"));
  return detail + (safe.requestId ? " " + t("accountsManager.controls.requestId", { id: safe.requestId }) : "");
}

export function accountRequestError(error: unknown, status = 0): AccountControlError {
  const code = error && typeof error === "object" && "code" in error && typeof error.code === "string" && accountErrorCodes.has(error.code) ? error.code : "";
  return new AccountControlError(status, requestID(error),code);
}

function requestID(error: unknown): string {
  const value = error && typeof error === "object" && "requestId" in error ? error.requestId : undefined;
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9_./-]{0,127}$/.test(value) ? value : "";
}

function observed<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error || !result.response.ok || !result.data) {
    throw new AccountControlError(result.response.status, requestID(result.error));
  }
  return result.data;
}

function validID(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(value);
}

const terminalPhases = new Set(["ready", "cancelled", "failed"]);
const switchPhases = new Set(["requested", "waiting", "stopping", "stopped", "committed", "starting", ...terminalPhases, "recovery_required"]);
export function accountSwitchIsActive(operation?: AccountSwitch): boolean {
  return Boolean(operation && !terminalPhases.has(operation.phase));
}

export function accountSwitchIsNoop(operation: AccountSwitch): boolean {
  return operation.phase === "ready" && !operation.recoveryRequired && operation.sourceRevision === operation.targetRevision
    && operation.sourceMode === operation.targetMode && (operation.sourceAccountId ?? "") === (operation.targetAccountId ?? "");
}

function ownSwitch(operation: AccountSwitch, sessionId: string, operationId?: string): AccountSwitch {
  if (!operation || operation.sessionId !== sessionId || !validID(operation.id) || (operationId && operation.id !== operationId)
    || !validID(operation.provider) || !switchPhases.has(operation.phase) || !["drain", "interrupt"].includes(operation.policy)
    || !validMode(operation.sourceMode, operation.sourceAccountId) || !validMode(operation.targetMode, operation.targetAccountId)
    || !validRevision(operation.sourceRevision) || !validRevision(operation.targetRevision, true)
    || typeof operation.newConversation !== "boolean" || typeof operation.recoveryRequired !== "boolean"
    || (operation.canRetry !== undefined && typeof operation.canRetry !== "boolean")) {
    throw new AccountControlError(502);
  }
  return safeOperationFailure(operation);
}

export async function fetchSessionAccountControl(sessionId: string, signal?: AbortSignal): Promise<SessionAccountState> {
  if (!validID(sessionId)) throw new AccountControlError(400);
  const data = observed(await apiClient.GET("/api/v1/sessions/{sessionId}/account", { params: { path: { sessionId } }, signal }));
  if (data.sessionId !== sessionId || !validMode(data.mode, data.accountId) || !validRevision(data.revision) || !validID(data.provider) || typeof data.blocked !== "boolean") {
    throw new AccountControlError(502);
  }
  return data.switch ? { ...data, switch: ownSwitch(data.switch, sessionId) } : data;
}

export async function startSessionAccountSwitch(sessionId: string, body: AccountSwitchRequest): Promise<AccountSwitch> {
  if (!validID(sessionId) || !validSwitchRequest(body)) {
    throw new AccountControlError(400);
  }
  const operation = ownSwitch(observed(await apiClient.POST("/api/v1/sessions/{sessionId}/account-switches", { params: { path: { sessionId } }, body })), sessionId, body.operationId);
  if (operation.targetMode !== body.mode || (operation.targetAccountId ?? "") !== (body.accountId ?? "") || (!accountSwitchIsNoop(operation) && operation.sourceRevision !== body.expectedRevision) || operation.policy !== body.policy || operation.newConversation !== Boolean(body.newConversation)) {
    throw new AccountControlError(502);
  }
  return operation;
}

export async function fetchSessionAccountSwitch(sessionId: string, operationId: string, signal?: AbortSignal): Promise<AccountSwitch> {
  if (!validID(sessionId) || !validID(operationId)) throw new AccountControlError(400);
  return ownSwitch(observed(await apiClient.GET("/api/v1/sessions/{sessionId}/account-switches/{operationId}", { params: { path: { sessionId, operationId } }, signal })), sessionId, operationId);
}

export async function changeSessionAccountSwitch(sessionId: string, operationId: string, action: "retry" | "cancel"): Promise<AccountSwitch> {
  if (!validID(sessionId) || !validID(operationId)) throw new AccountControlError(400);
  const path = action === "retry" ? "/api/v1/sessions/{sessionId}/account-switches/{operationId}/retry" : "/api/v1/sessions/{sessionId}/account-switches/{operationId}/cancel";
  return ownSwitch(observed(await apiClient.POST(path, { params: { path: { sessionId, operationId } } })), sessionId, operationId);
}

function validRevision(value: unknown, zero = false): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= (zero ? 0 : 1);
}

function validMode(mode: unknown, accountId: unknown): boolean {
  return mode === "managed" ? validID(accountId) : mode === "native" && (accountId === undefined || accountId === "");
}

function validSwitchRequest(value: unknown): value is AccountSwitchRequest {
  if (!value || typeof value !== "object") return false;
  const body = value as Record<string, unknown>;
  return Object.keys(body).every(key => ["operationId", "expectedRevision", "mode", "accountId", "policy", "newConversation"].includes(key))
    && validID(body.operationId) && validRevision(body.expectedRevision) && validMode(body.mode, body.accountId)
    && (body.policy === "drain" || body.policy === "interrupt") && (body.newConversation === undefined || typeof body.newConversation === "boolean");
}

export function readSessionSwitchIntent(sessionId: string): AccountSwitchRequest | undefined {
  const saved = localStorage.getItem(`ao:account-switch:${sessionId}`);
  if (saved === null) return undefined;
  if (saved.length > 2048) throw new AccountControlError(502);
  const body: unknown = JSON.parse(saved);
  if (!validSwitchRequest(body)) throw new AccountControlError(502);
  return body;
}

export function saveSessionSwitchIntent(sessionId: string, body?: AccountSwitchRequest): void {
  if (!validID(sessionId) || (body && !validSwitchRequest(body))) throw new AccountControlError(400);
  const key = `ao:account-switch:${sessionId}`;
  if (body) localStorage.setItem(key, JSON.stringify(body));
  else localStorage.removeItem(key);
}

export function accountRemovalIsActive(operation?: AccountRemoval): boolean {
  return Boolean(operation && !["complete", "cancelled"].includes(operation.phase));
}

function ownImpact(impact: AccountRemovalImpact, accountId: string): AccountRemovalImpact {
  if (!impact || impact.accountId !== accountId || !validRevision(impact.revision, true) || !Array.isArray(impact.sessions)
    || impact.sessions.some(session => !session || !validID(session.sessionId) || !validID(session.provider) || !validRevision(session.bindingRevision) || typeof session.stopped !== "boolean")) {
    throw new AccountControlError(502);
  }
  return impact;
}

function ownRemoval(operation: AccountRemoval, accountId: string, operationId: string): AccountRemoval {
  if (!operation || operation.id !== operationId || operation.accountId !== accountId
    || !["requested", "stopping", "revoked", "complete", "recovery_required", "cancelled"].includes(operation.phase)
    || typeof operation.canCancel !== "boolean" || typeof operation.recoveryRequired !== "boolean") throw new AccountControlError(502);
  ownImpact(operation.impact, accountId);
  return safeOperationFailure(operation);
}

export async function fetchAccountRemovalImpact(accountId: string, signal?: AbortSignal): Promise<AccountRemovalImpact> {
  if (!validID(accountId)) throw new AccountControlError(400);
  return ownImpact(observed(await apiClient.GET("/api/v1/accounts-manager/accounts/{accountId}/removal-impact", { params: { path: { accountId } }, signal })), accountId);
}

export async function fetchAccountRemoval(accountId: string, operationId: string, signal?: AbortSignal): Promise<AccountRemoval> {
  if (!validID(accountId) || !validID(operationId)) throw new AccountControlError(400);
  return ownRemoval(observed(await apiClient.GET("/api/v1/accounts-manager/removals/{operationId}", { params: { path: { operationId } }, signal })), accountId, operationId);
}

function validRemovalRequest(value: unknown): value is AccountRemovalRequest {
  if (!value || typeof value !== "object") return false;
  const body = value as Record<string, unknown>;
  return Object.keys(body).every(key => ["operationId", "expectedRevision", "confirmed"].includes(key))
    && validID(body.operationId) && validRevision(body.expectedRevision, true) && body.confirmed === true;
}

export async function startAccountRemoval(accountId: string, body: AccountRemovalRequest): Promise<AccountRemoval> {
  if (!validID(accountId) || !validRemovalRequest(body)) throw new AccountControlError(400);
  const operation = ownRemoval(observed(await apiClient.POST("/api/v1/accounts-manager/accounts/{accountId}/removals", { params: { path: { accountId } }, body })), accountId, body.operationId);
  if (operation.impact.revision !== body.expectedRevision) throw new AccountControlError(502);
  return operation;
}

export async function changeAccountRemoval(accountId: string, operationId: string, action: "retry" | "cancel"): Promise<AccountRemoval> {
  const current = await fetchAccountRemoval(accountId, operationId);
  if (action === "cancel" && !current.canCancel) throw new AccountControlError(409);
  if (!accountRemovalIsActive(current)) return current;
  const path = action === "retry" ? "/api/v1/accounts-manager/removals/{operationId}/retry" : "/api/v1/accounts-manager/removals/{operationId}/cancel";
  return ownRemoval(observed(await apiClient.POST(path, { params: { path: { operationId } } })), accountId, operationId);
}

const removalReferenceKey = "ao:account-removals:v1";
export function readAccountRemovalReferences(): AccountRemovalReference[] {
  const saved = localStorage.getItem(removalReferenceKey);
  if (saved === null) return [];
  if (saved.length > 65_536) throw new AccountControlError(502);
  const values: unknown = JSON.parse(saved);
  if (!Array.isArray(values) || values.length > 128 || values.some(value => !value || typeof value !== "object"
    || Object.keys(value).some(key => !["accountId", "operationId", "request"].includes(key))
    || !validID(value.accountId) || !validID(value.operationId)
    || (value.request !== undefined && (!validRemovalRequest(value.request) || value.request.operationId !== value.operationId)))
    || new Set(values.map(value => value.accountId)).size !== values.length) throw new AccountControlError(502);
  return values;
}

export function saveAccountRemovalReference(accountId: string, reference?: AccountRemovalReference): AccountRemovalReference[] {
  if (!validID(accountId) || (reference && (reference.accountId !== accountId || !validID(reference.operationId)
    || Object.keys(reference).some(key => !["accountId", "operationId", "request"].includes(key))
    || (reference.request && (!validRemovalRequest(reference.request) || reference.request.operationId !== reference.operationId))))) throw new AccountControlError(400);
  const values = readAccountRemovalReferences().filter(value => value.accountId !== accountId);
  if (reference) values.push(reference);
  if (values.length > 128) throw new AccountControlError(507);
  localStorage.setItem(removalReferenceKey, JSON.stringify(values));
  return values;
}
