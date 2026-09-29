import { useEffect } from "react";
import { queryOptions, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl } from "../lib/api-client";
import { accountRequestError } from "../lib/accounts-manager-controls";

export type AccountsManagerSnapshot =
  components["schemas"]["AccountsManagerAccountsResponse"];
export type AccountsManagerAccount =
  components["schemas"]["AccountsManagerAccountResponse"];
export const accountsManagerQueryKey = [
  "accounts-manager",
  "accounts",
] as const;

export function selectAccountsManagerSnapshot(
  current: AccountsManagerSnapshot | undefined,
  incoming: AccountsManagerSnapshot,
): AccountsManagerSnapshot {
  if (
    !current ||
    !Number.isSafeInteger(current.revision) ||
    !Number.isSafeInteger(incoming.revision)
  ) {
    return incoming;
  }
  return incoming.revision > current.revision ? incoming : current;
}

export async function fetchAccountsManager(): Promise<AccountsManagerSnapshot> {
  const { data, error, response } = await apiClient.GET(
    "/api/v1/accounts-manager/accounts",
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}

const inventoryReads = new WeakMap<AccountsManagerSnapshot, {
  query: object | undefined;
  updates: number | undefined;
}>();

function accountsManagerQueryOptions(client: QueryClient) {
  const cachedQuery = () => client.getQueryCache().find({ queryKey: accountsManagerQueryKey, exact: true });
  return queryOptions({
    queryKey: accountsManagerQueryKey,
    queryFn: async () => {
      const query = cachedQuery();
      const updates = query?.state.dataUpdateCount;
      const snapshot = { ...await fetchAccountsManager() };
      inventoryReads.set(snapshot, { query, updates });
      return snapshot;
    },
    structuralSharing: (current, incoming) => {
      const snapshot = incoming as AccountsManagerSnapshot;
      const read = inventoryReads.get(snapshot);
      inventoryReads.delete(snapshot);
      const query = cachedQuery();
      // Check at cache commit, including writes between promise continuations.
      if (read && current !== undefined &&
          (read.query !== query || read.updates !== query?.state.dataUpdateCount)) {
        return current;
      }
      return selectAccountsManagerSnapshot(current as AccountsManagerSnapshot | undefined, snapshot);
    },
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnMount: "always",
    retry: 1,
  });
}

export function useAccountsManagerQuery(enabled = true) {
  return useQuery({ ...accountsManagerQueryOptions(useQueryClient()), enabled });
}

export function useAccountsManagerEvents(): void {
  const client = useQueryClient();
  useEffect(() => {
    let closed = false;
    let stream: EventSource | null = null;
    let reconnect: number | undefined;
    const apply = (snapshot: AccountsManagerSnapshot) =>
      client.setQueryData<AccountsManagerSnapshot>(
        accountsManagerQueryKey,
        (current) => selectAccountsManagerSnapshot(current, snapshot),
      );
    const refresh = () => client.fetchQuery({ ...accountsManagerQueryOptions(client), staleTime: 0 });
    const connect = async (refreshFirst: boolean) => {
      if (refreshFirst)
        try {
          await refresh();
        } catch {
          /* keep the last safe snapshot */
        }
      if (closed) return;
      const base = getApiBaseUrl();
      if (!base) return;
      stream = new EventSource(
        `${base}/api/v1/accounts-manager/accounts/events`,
      );
      stream.addEventListener("accounts_manager", (event) => {
        try {
          if (!closed) apply(
            JSON.parse(
              (event as MessageEvent<string>).data,
            ) as AccountsManagerSnapshot,
          );
        } catch {
          /* ignore malformed events */
        }
      });
      stream.onerror = () => {
        stream?.close();
        stream = null;
        if (!closed)
          reconnect = window.setTimeout(() => void connect(true), 1_000);
      };
    };
    void connect(false);
    const focus = () =>
      void refresh()
        .catch(() => undefined);
    window.addEventListener("focus", focus);
    return () => {
      closed = true;
      stream?.close();
      if (reconnect) window.clearTimeout(reconnect);
      window.removeEventListener("focus", focus);
    };
  }, [client]);
}

export async function startAccountsManagerOAuth(
  provider: "codex" | "claude",
  mode: "callback" | "device",
  target?: { accountId: string; generation: number },
) {
  const { data, error, response } = await apiClient.POST(
    "/api/v1/accounts-manager/oauth-sessions",
    { body: { provider, mode, ...target } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data;
}
export async function cancelAccountsManagerOAuth(operationId: string) {
  const { error, response } = await apiClient.DELETE(
    "/api/v1/accounts-manager/oauth-sessions/{operationId}",
    { params: { path: { operationId } } },
  );
  if (error) throw accountRequestError(error, response?.status);
}
export async function addAccountsManagerAPIKey(
  provider: "codex" | "claude",
  key: string,
  baseUrl?: string,
  operationId = crypto.randomUUID(),
) {
  const { data, error, response } = await apiClient.POST(
    "/api/v1/accounts-manager/accounts/api-key",
    { body: { provider, key, operationId, ...(baseUrl ? { baseUrl } : {}) } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
export async function importAccountsManagerCredential(
  provider: "codex" | "claude",
  filename: string,
  credential: Record<string, unknown>,
  operationId = crypto.randomUUID(),
) {
  const { data, error, response } = await apiClient.POST(
    "/api/v1/accounts-manager/accounts/import",
    { body: { provider, filename, credential, operationId } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
export async function setAccountsManagerDisabled(
  accountId: string,
  disabled: boolean,
) {
  const { data, error, response } = await apiClient.PATCH(
    "/api/v1/accounts-manager/accounts/{accountId}",
    { params: { path: { accountId } }, body: { disabled } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
export async function refreshAccountsManagerAccount(accountId: string) {
  const { data, error, response } = await apiClient.POST(
    "/api/v1/accounts-manager/accounts/{accountId}/refresh",
    { params: { path: { accountId } } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
export async function renameAccountsManagerAccount(
  accountId: string,
  label: string,
  generation: number,
) {
  const { data, error, response } = await apiClient.PATCH(
    "/api/v1/accounts-manager/accounts/{accountId}",
    { params: { path: { accountId } }, body: { label, generation } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
export async function removeAccountsManagerAccount(accountId: string) {
  const { data, error, response } = await apiClient.DELETE(
    "/api/v1/accounts-manager/accounts/{accountId}",
    { params: { path: { accountId } } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
export async function fetchAccountsManagerModels(accountId: string, signal?: AbortSignal) {
  const { data, error, response } = await apiClient.GET(
    "/api/v1/accounts-manager/accounts/{accountId}/models",
    { params: { path: { accountId } }, signal },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data;
}
export async function fetchAccountsManagerQuota(accountId: string, signal?: AbortSignal) {
  const { data, error, response } = await apiClient.GET(
    "/api/v1/accounts-manager/accounts/{accountId}/quota",
    { params: { path: { accountId } }, signal },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data;
}
export async function resetAccountsManagerQuota(accountId: string) {
  const { error, response } = await apiClient.POST(
    "/api/v1/accounts-manager/accounts/{accountId}/quota/reset",
    { params: { path: { accountId } } },
  );
  if (error) throw accountRequestError(error, response?.status);
}

export async function updateAccountsManagerRouting(
  provider: "codex" | "claude",
  enabled: boolean,
  accountIds: string[],
) {
  const { data, error, response } = await apiClient.PUT(
    "/api/v1/accounts-manager/routing/{provider}",
    { params: { path: { provider } }, body: { enabled, accountIds } },
  );
  if (error) throw accountRequestError(error, response?.status);
  return data as AccountsManagerSnapshot;
}
