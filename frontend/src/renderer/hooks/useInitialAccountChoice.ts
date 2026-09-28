import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { AccountControlError, accountRequestError } from "../lib/accounts-manager-controls";
import { useAccountsManagerQuery, type AccountsManagerAccount } from "./useAccountsManagerQuery";

export type InitialAccountChoice = components["schemas"]["SpawnAccountChoice"];

export function initialAccountProvider(harness: string): string {
  if (harness === "codex") return "codex";
  if (harness === "claude-code") return "claude";
  return "";
}

export function initialAccountReady(account: AccountsManagerAccount): boolean {
  return account.status === "active" && account.verification === "verified" && !account.disabled && !account.unavailable;
}

export function useInitialAccountChoice(harness: string, active: boolean, value: string) {
  const provider = initialAccountProvider(harness);
  const enabled = active && provider !== "";
  const capability = useQuery({
    queryKey: ["initial-account-selection"],
    enabled: active,
    retry: false,
    staleTime: 10_000,
    refetchOnMount: "always",
    queryFn: async ({ signal }) => {
      const { data, error, response } = await apiClient.GET("/api/v1/sessions/account-selection", { signal });
      if (error) throw accountRequestError(error, response?.status);
      if (typeof data?.initialSelection !== "boolean") throw new AccountControlError(502);
      return data.initialSelection;
    },
  });
  const inventory = useAccountsManagerQuery(active && capability.data === true);
  const accounts = inventory.data?.accounts.filter(account => account.provider === provider) ?? [];
  const selected = accounts.find(account => value === "managed:" + account.id);
  const inventoryReady = !inventory.isError && inventory.data?.availability === "ready" && !inventory.data.stale;
  const managedProviders = active && !capability.isError && !capability.isFetching && capability.data === true && inventoryReady
    ? [...new Set(inventory.data?.accounts.filter(initialAccountReady).map(account => account.provider))]
    : [];
  const ready = !enabled || (!capability.isError && !capability.isFetching && (
    capability.data === false ? value === "" : capability.data === true && (
      value === "native" || Boolean(inventoryReady && selected && initialAccountReady(selected))
    )
  ));

  const confirm = async (): Promise<InitialAccountChoice | undefined> => {
    if (!enabled) return undefined;
    const supported = await capability.refetch();
    if (supported.isError) throw supported.error;
    if (supported.data === false && value === "") return undefined;
    if (supported.data !== true) throw new AccountControlError(501);
    if (value === "native") return { mode: "native" };
    const latest = await inventory.refetch();
    if (latest.isError) throw latest.error;
    const account = latest.data?.accounts.find(item => item.provider === provider && value === "managed:" + item.id);
    if (!latest.data || latest.data.stale || latest.data.availability !== "ready" || !account || !initialAccountReady(account)) {
      throw new InitialAccountChoiceUnavailable();
    }
    return { mode: "managed", accountId: account.id };
  };

  return { enabled, capability, inventory, accounts, selected, inventoryReady, managedProviders, ready, confirm };
}

export class InitialAccountChoiceUnavailable extends Error {}
