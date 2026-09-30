import { useQueries, type QueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { fetchAccountsManagerQuota, type AccountsManagerAccount } from "../../hooks/useAccountsManagerQuery";
import { accountControlMessage } from "../../lib/accounts-manager-controls";
import { Button } from "../ui/button";

type Quota = NonNullable<Awaited<ReturnType<typeof fetchAccountsManagerQuota>>>;

function canReadUsage(account: AccountsManagerAccount): boolean {
  return Boolean(account.kind !== "access_token" && account.quotaSupported && account.verification === "verified" && !account.disabled);
}

export function useAccountUsage(accounts: AccountsManagerAccount[]) {
  return useQueries({ queries: accounts.map(account => ({
    ...accountUsageQueryOptions(account),
    enabled: canReadUsage(account),
  })) });
}

function accountUsageQueryOptions(account: AccountsManagerAccount) {
  return {
    queryKey: ["accounts-manager", "quota", account.id, account.generation, account.kind, account.updatedAt, account.verifiedAt],
    queryFn: ({ signal }: { signal: AbortSignal }) => fetchAccountsManagerQuota(account.id, signal),
    retry: false,
    staleTime: 30_000,
    gcTime: 60_000,
    refetchOnWindowFocus: false,
  };
}

export function prefetchAccountUsage(queryClient: QueryClient, account: AccountsManagerAccount) {
  if (!canReadUsage(account)) return;
  return queryClient.prefetchQuery(accountUsageQueryOptions(account));
}

function remaining(value: number, locale?: string) {
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(value);
}

export function accountUsageSummary(account: AccountsManagerAccount, query: UseQueryResult<Quota | undefined>, t: TFunction, locale?: string): string {
  if (account.verification !== "verified") return t(account.verification === "invalid" ? "accountsManager.verification.invalid" : "accountsManager.verification.unverified");
  if (account.disabled) return t("accountsManager.usage.unavailable");
  if (account.kind === "access_token") return t("accountsManager.usage.tokenPermission");
  if (!account.quotaSupported) return t(account.kind === "api_key" ? "accountsManager.usage.apiKey" : "accountsManager.usage.unavailable");
  if (query.isError) return t("accountsManager.usage.unavailable");
  const fraction = query.data?.groups?.[0]?.buckets?.[0]?.remainingFraction;
  if (typeof fraction !== "number" || !Number.isFinite(fraction) || fraction < 0 || fraction > 1) return t(query.isFetching ? "accountsManager.usage.checking" : "accountsManager.usage.unavailable");
  return t("accountsManager.usage.remaining", { percent: remaining(fraction, locale) });
}

export function AccountUsage({ account }: { account: AccountsManagerAccount }) {
  const { t, i18n } = useTranslation();
  const [query] = useAccountUsage([account]);
  const summary = accountUsageSummary(account, query, t, i18n.resolvedLanguage);
  const supported = canReadUsage(account);
  const stamp = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString(i18n.resolvedLanguage) : t("accountsManager.status.unknown");
  return <section aria-label={t("accountsManager.usage.title")} className="space-y-3">
    <div className="flex items-center justify-between gap-2">
      <h4 className="font-medium">{t("accountsManager.usage.title")}</h4>
      {supported ? <Button size="sm" variant="ghost" disabled={query.isFetching} onClick={() => void query.refetch()}>{t("accountsManager.usage.refresh")}</Button> : null}
    </div>
    {!query.data || query.isError || !supported ? <p role="status">{summary}</p> : null}
    {query.error && supported ? <p role="alert">{accountControlMessage(query.error, t)}</p> : null}
    {query.data && supported ? <>
      {query.isError ? <p>{t("accountsManager.usage.stale")}</p> : null}
      {query.data.groups.map((group, groupIndex) => group.buckets.map((bucket, index) => {
        const value = bucket.remainingFraction;
        if (!Number.isFinite(value) || value < 0 || value > 1) return <p key={`${groupIndex}:${index}`}>{t("accountsManager.usage.unavailable")}</p>;
        const window = bucket.window === "18000s" || bucket.window === "five_hour" ? t("accountsManager.usage.fiveHour")
          : bucket.window === "604800s" || bucket.window === "seven_day" ? t("accountsManager.usage.week")
            : bucket.window;
        return <div key={`${groupIndex}:${index}`} className="space-y-1">
          <p>{window}: {t("accountsManager.usage.remaining", { percent: remaining(value, i18n.resolvedLanguage) })}</p>
          <progress aria-label={t("accountsManager.usage.remaining", { percent: remaining(value, i18n.resolvedLanguage) })} value={value} max={1} className="h-2 w-full" />
          {bucket.resetTime ? <p>{t("accountsManager.usage.reset", { time: stamp(bucket.resetTime) })}</p> : null}
        </div>;
      }))}
      <p>{t("accountsManager.usage.checked", { time: stamp(query.data.observedAt) })}</p>
    </> : null}
    <p className="text-muted-foreground">{t("accountsManager.usage.description")}</p>
  </section>;
}
