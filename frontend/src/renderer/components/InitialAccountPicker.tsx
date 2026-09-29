import { useId } from "react";
import { useTranslation } from "react-i18next";
import { initialAccountReady, type useInitialAccountChoice } from "../hooks/useInitialAccountChoice";
import { accountControlMessage } from "../lib/accounts-manager-controls";
import { accountUsageSummary, useAccountUsage } from "./settings/AccountUsage";

type Props = {
  state: ReturnType<typeof useInitialAccountChoice>;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
};

export function InitialAccountPicker({ state, value, disabled, onChange }: Props) {
  const { t, i18n } = useTranslation();
  const id = useId();
  const usage = useAccountUsage(state.inventoryReady ? state.accounts : []);
  if (!state.enabled || state.capability.data === false) return null;
  return <>
    <label className="sr-only" htmlFor={id}>{t("accountsManager.initial.title")}</label>
    <select id={id} className="composer-chip composer-toolbar-option w-full truncate" title={t("accountsManager.initial.description")} value={value}
      disabled={disabled || state.capability.isFetching || state.capability.isError || state.capability.data !== true} onChange={event => onChange(event.target.value)}>
      <option value="" disabled>{t("accountsManager.initial.shortChoose")}</option>
      <option value="native">{t("accountsManager.controls.native")}</option>
      {value.startsWith("managed:") && !state.selected ? <option value={value} disabled>{t("accountsManager.initial.missing", { id: value.slice(8) })}</option> : null}
      {state.accounts.map((account, index) => {
        const identity = account.email
          ? account.label && account.label !== account.email ? `${account.label} (${account.email})` : account.email
          : account.label ? `${account.label} (${account.id})` : account.id;
        const quota = account.quotaSupported && usage[index] ? `: ${accountUsageSummary(account, usage[index], t, i18n.resolvedLanguage)}` : "";
        return <option key={account.id} value={"managed:" + account.id} disabled={!state.inventoryReady || !initialAccountReady(account)}>{identity}{quota}</option>;
      })}
    </select>
  </>;
}

export function InitialAccountStatus({ state, value, disabled }: Omit<Props, "onChange">) {
  const { t } = useTranslation();
  if (!state.enabled || state.capability.data === false) return null;
  const capabilityError = state.capability.error;
  const busy = disabled || state.capability.isFetching;
  const needsRefresh = capabilityError || state.capability.data === true && !state.inventoryReady;
  return (
    <div className="px-3 text-xs text-muted-foreground" aria-live="polite">
      {needsRefresh ? <div className="flex items-center justify-end">
        <button type="button" className="text-xs underline" disabled={busy || state.inventory.isFetching} onClick={() => {
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
