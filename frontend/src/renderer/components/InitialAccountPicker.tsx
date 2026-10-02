import { useTranslation } from "react-i18next";
import { initialAccountReady, type useInitialAccountChoice } from "../hooks/useInitialAccountChoice";
import { accountControlMessage } from "../lib/accounts-manager-controls";
import { accountUsageSummary, useAccountUsage } from "./settings/AccountUsage";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

type Props = {
  state: ReturnType<typeof useInitialAccountChoice>;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
};

export function InitialAccountPicker({ state, value, disabled, onChange }: Props) {
  const { t, i18n } = useTranslation();
  const usage = useAccountUsage(state.inventoryReady ? state.accounts : []);
  if (!state.enabled || state.capability.data === false) return null;
  const locked = disabled || state.capability.isPending || state.capability.isError || state.capability.data !== true;
  return <SettingsOptionMenu
    aria-label={t("accountsManager.initial.title")}
    value={value}
    disabled={locked}
    placeholder={state.capability.isPending ? t("accountsManager.initial.checking") : t("accountsManager.initial.shortChoose")}
    triggerClassName="composer-chip composer-toolbar-option w-full justify-between"
    menuClassName="min-w-64 max-w-[calc(100vw-2rem)]"
    menuAlign="start"
    onChange={next => { if (!locked) onChange(next); }}
    options={[
      { value: "native", label: t("accountsManager.controls.native"), disabled: locked },
      ...(value.startsWith("managed:") && !state.selected ? [{ value, label: t("accountsManager.initial.missing", { id: value.slice(8) }), disabled: true }] : []),
      ...state.accounts.map((account, index) => {
        const identity = account.email
          ? account.label && account.label !== account.email ? `${account.label} (${account.email})` : account.email
          : account.label ? `${account.label} (${account.id})` : account.id;
        const quota = account.quotaSupported && usage[index] ? `: ${accountUsageSummary(account, usage[index], t, i18n.resolvedLanguage)}` : "";
        return { value: "managed:" + account.id, label: identity + quota, disabled: locked || !state.inventoryReady || !initialAccountReady(account) };
      }),
    ]}
  />;
}

export function InitialAccountStatus({ state, value, disabled }: Omit<Props, "onChange">) {
  const { t } = useTranslation();
  if (!state.enabled || state.capability.data === false) return null;
  const capabilityError = state.capability.error;
  const busy = disabled || state.capability.isPending;
  const needsRefresh = capabilityError || state.capability.data === true && !state.inventoryReady;
  return (
    <div className="px-3 text-xs text-muted-foreground" aria-live="polite">
      {needsRefresh ? <div className="flex items-center justify-end">
        <button type="button" className="text-xs underline" disabled={busy || (state.inventory.isPending && state.inventory.isEnabled)} onClick={() => {
          void state.capability.refetch();
          if (state.capability.data) void state.inventory.refetch();
        }}>{t("accountsManager.controls.refreshSession")}</button>
      </div> : null}
      {state.capability.isPending ? <p role="status">{t("accountsManager.initial.checking")}</p> : null}
      {capabilityError ? <p role="alert">{accountControlMessage(capabilityError, t)}</p> : null}
      {state.capability.data === true ? <>
        {state.inventory.error ? <p role="alert">{accountControlMessage(state.inventory.error, t)}</p> : null}
        {!state.inventoryReady ? <p role="status">{t("accountsManager.controls.inventoryUnavailable")}</p> : null}
        {value.startsWith("managed:") && !state.ready && !busy ? <p role="status">{t("accountsManager.initial.changed")}</p> : null}
      </> : null}
    </div>
  );
}
