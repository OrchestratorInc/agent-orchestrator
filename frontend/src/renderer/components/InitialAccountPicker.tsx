import { useId } from "react";
import { useTranslation } from "react-i18next";
import { initialAccountReady, type useInitialAccountChoice } from "../hooks/useInitialAccountChoice";
import { accountControlMessage } from "../lib/accounts-manager-controls";

type Props = {
  state: ReturnType<typeof useInitialAccountChoice>;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
};

export function InitialAccountPicker({ state, value, disabled, onChange }: Props) {
  const { t } = useTranslation();
  const id = useId();
  if (!state.enabled) return null;
  const capabilityError = state.capability.error;
  const busy = disabled || state.capability.isFetching;
  return (
    <div className="space-y-2 rounded-md border border-border p-3 text-sm">
      <div className="flex items-center justify-between gap-2">
        <label htmlFor={id}>{t("accountsManager.initial.title")}</label>
        <button type="button" className="text-xs underline" disabled={busy || state.inventory.isFetching} onClick={() => {
          void state.capability.refetch();
          if (state.capability.data) void state.inventory.refetch();
        }}>{t("accountsManager.controls.refreshSession")}</button>
      </div>
      {state.capability.isPending ? <p role="status">{t("accountsManager.initial.checking")}</p> : null}
      {capabilityError ? <p role="alert">{accountControlMessage(capabilityError, t)}</p> : null}
      {state.capability.data === false ? <p>{t("accountsManager.initial.unsupported")}</p> : null}
      {state.capability.data === true ? <>
        <select id={id} className="w-full rounded border border-border bg-background p-2" value={value} disabled={busy} onChange={event => onChange(event.target.value)}>
          <option value="" disabled>{t("accountsManager.initial.choose")}</option>
          <option value="native">{t("accountsManager.controls.native")}</option>
          {value.startsWith("managed:") && !state.selected ? <option value={value} disabled>{t("accountsManager.initial.missing", { id: value.slice(8) })}</option> : null}
          {state.accounts.map(account => <option key={account.id} value={"managed:" + account.id} disabled={!state.inventoryReady || !initialAccountReady(account)}>
            {account.label ? `${account.label} (${account.id})` : account.id}
          </option>)}
        </select>
        <p className="text-xs text-muted-foreground">{t("accountsManager.initial.description")}</p>
        {state.inventory.error ? <p role="alert">{accountControlMessage(state.inventory.error, t)}</p> : null}
        {!state.inventoryReady ? <p role="status">{t("accountsManager.controls.inventoryUnavailable")}</p> : null}
        {value.startsWith("managed:") && !state.ready && !busy ? <p role="status">{t("accountsManager.initial.changed")}</p> : null}
      </> : null}
    </div>
  );
}
