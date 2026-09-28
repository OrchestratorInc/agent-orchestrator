import { useTranslation } from "react-i18next";
import type { useAccountsManagerQuery } from "../../hooks/useAccountsManagerQuery";
import { initialAccountProvider, initialAccountReady } from "../../hooks/useInitialAccountChoice";

type Inventory = Pick<ReturnType<typeof useAccountsManagerQuery>, "data" | "isError" | "isFetching">;

export function ManagedAccountAvailability({ harness, installed, inventory }: {
  harness: string;
  installed: boolean;
  inventory: Inventory;
}) {
  const { t } = useTranslation();
  const provider = initialAccountProvider(harness);
  if (!provider) return null;
  const snapshot = inventory.data;
  const fresh = !inventory.isError && !inventory.isFetching && snapshot?.availability === "ready" && snapshot.stale === false && Array.isArray(snapshot.accounts);
  const count = fresh ? snapshot.accounts.filter(account => account.provider === provider && initialAccountReady(account)).length : 0;
  const status = !installed ? t("accountsManager.harness.installRequired")
    : inventory.isFetching ? t("accountsManager.harness.checking")
      : !fresh ? t("accountsManager.harness.unavailable")
        : count > 0 ? t(count === 1 ? "accountsManager.harness.one" : "accountsManager.harness.many", { count }) : t("accountsManager.harness.none");
  return <div className="mt-1 space-y-1 text-xs text-settings-muted">
    <p>{status}</p>
    <p>{t("accountsManager.harness.scope")}</p>
  </div>;
}
