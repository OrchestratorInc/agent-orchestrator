import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAccountsManagerQuery } from "../hooks/useAccountsManagerQuery";
import {
  AccountControlError, accountControlMessage, accountSwitchIsActive, changeSessionAccountSwitch,
  fetchSessionAccountControl, fetchSessionAccountSwitch, readSessionSwitchIntent, saveSessionSwitchIntent, startSessionAccountSwitch,
  type AccountSwitch, type AccountSwitchRequest,
} from "../lib/accounts-manager-controls";
import { Button } from "./ui/button";
import { AccountUsage, accountUsageSummary, useAccountUsage } from "./settings/AccountUsage";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

type SessionAccountControlProps = { sessionId: string; compact?: boolean; onSwitchLockChange?: (locked: boolean) => void };

export function SessionAccountControl(props: SessionAccountControlProps) {
  return <SessionAccountPanel key={props.sessionId} {...props} />;
}

function SessionAccountPanel({ sessionId, compact = false, onSwitchLockChange }: SessionAccountControlProps) {
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const key = ["accounts-manager", "session", sessionId];
  const inventory = useAccountsManagerQuery();
  const current = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => fetchSessionAccountControl(sessionId, signal),
    retry: false,
    refetchInterval: 1500,
  });
  const [target, setTarget] = useState("");
  const [policy, setPolicy] = useState<"" | "drain" | "interrupt">("");
  const [newConversation, setNewConversation] = useState(false);
  const [initial] = useState(() => {
    try { return { body: readSessionSwitchIntent(sessionId), unreadable: false }; }
    catch { return { body: undefined, unreadable: true }; }
  });
  const [submitted, setSubmitted] = useState<AccountSwitchRequest | undefined>(initial.body);
  const [localError, setLocalError] = useState(initial.unreadable ? t("accountsManager.controls.switchSavedError") : "");
  const operationKey = (id?: string) => ["accounts-manager", "switch", sessionId, id];
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: (request: AccountSwitchRequest | { operationId: string; action: "retry" | "cancel" }) => "action" in request
      ? changeSessionAccountSwitch(sessionId, request.operationId, request.action)
      : startSessionAccountSwitch(sessionId, request),
    retry: false,
    onMutate: async (request) => {
      await client.cancelQueries({ queryKey: operationKey(request.operationId) });
    },
    onSuccess: (result) => client.setQueryData(operationKey(result.id), result),
    onError: (error, request) => {
      if (!("action" in request) && error instanceof AccountControlError && [400, 409].includes(error.status)) {
        try { saveSessionSwitchIntent(sessionId); setSubmitted(undefined); }
        catch { setLocalError(t("accountsManager.controls.switchSavedError")); }
      }
    },
    onSettled: async () => {
      await client.invalidateQueries({ queryKey: key });
      inFlight.current = false;
    },
  });
  const binding = current.data;
  const trackedId = submitted?.operationId ?? binding?.switch?.id;
  const observed = useQuery({
    queryKey: operationKey(trackedId),
    queryFn: ({ signal }) => fetchSessionAccountSwitch(sessionId, trackedId!, signal),
    enabled: Boolean(trackedId) && !mutation.isPending && !current.isError,
    retry: false,
    refetchInterval: query => query.state.data && !accountSwitchIsActive(query.state.data) ? false : 1500,
  });
  const operation: AccountSwitch | undefined = observed.data ?? (binding?.switch?.id === trackedId ? binding?.switch : undefined);
  const canRetry = operation?.canRetry === true && ["requested", "waiting", "recovery_required"].includes(operation.phase);
  const active = accountSwitchIsActive(operation);
  const serverActive = accountSwitchIsActive(binding?.switch);
  const unconfirmed = Boolean(submitted && !operation);
  const pendingCommit = operation?.phase === "ready" && (!binding || binding.revision < operation.targetRevision);
  const unavailable = current.isError || !binding;
  const busy = mutation.isPending || current.isPending;
  const controlsDisabled = busy || active || serverActive || unconfirmed || pendingCommit || Boolean(localError) || Boolean(binding?.blocked);
  const targetTrigger = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (compact && !unavailable) targetTrigger.current?.focus(); }, [compact, unavailable]);
  const switchLocked = mutation.isPending || active || serverActive || unconfirmed || pendingCommit || Boolean(localError) || Boolean(binding?.blocked);
  useEffect(() => { onSwitchLockChange?.(switchLocked); }, [onSwitchLockChange, switchLocked]);
  useEffect(() => {
    const completed = observed.data;
    if (!submitted || !completed || accountSwitchIsActive(completed) || !binding?.switch) return;
    const sameTerminal = binding.switch.id === completed.id && !accountSwitchIsActive(binding.switch);
    const nextPending = binding.switch.id !== completed.id && accountSwitchIsActive(binding.switch)
      && binding.revision >= Math.max(completed.sourceRevision, completed.targetRevision);
    if (!sameTerminal && !nextPending) return;
    try { saveSessionSwitchIntent(sessionId); setSubmitted(undefined); }
    catch { setLocalError(t("accountsManager.controls.switchSavedError")); }
  }, [submitted, observed.data, binding, sessionId]);
  const accounts = inventory.data?.accounts.filter(account => account.provider === binding?.provider) ?? [];
  const identity = (id?: string) => {
    const account = accounts.find(item => item.id === id);
    if (account?.email) return account.label && account.label !== account.id && account.label !== account.email
      ? `${account.label} (${account.email})` : account.email;
    return account?.label || id;
  };
  const usage = useAccountUsage(accounts);
  const inventoryReady = inventory.data?.availability === "ready" && !inventory.data.stale && !inventory.isError;
  const selected = target.startsWith("managed:") ? accounts.find(account => account.id === target.slice(8)) : undefined;
  const selectedReady = target === "native" || (inventoryReady && selected && selected.verification === "verified" && !selected.disabled && !selected.unavailable && selected.status === "active");
  const request = () => {
    if (!binding || busy || unavailable || active || serverActive || unconfirmed || pendingCommit || localError || binding.blocked || !selectedReady || !policy || inFlight.current) return;
    const body: AccountSwitchRequest = {
      operationId: crypto.randomUUID(), expectedRevision: binding.revision,
      mode: target === "native" ? "native" : "managed",
      ...(selected ? { accountId: selected.id } : {}), policy, newConversation,
    };
    try { saveSessionSwitchIntent(sessionId, body); }
    catch { setLocalError(t("accountsManager.controls.switchSaveError")); return; }
    inFlight.current = true;
    setSubmitted(body);
    mutation.mutate(body);
  };
  const change = (action: "retry" | "cancel") => {
    if (!operation || unavailable || busy || observed.isFetching || observed.isError || inFlight.current) return;
    if (action === "retry" && !canRetry) return;
    inFlight.current = true;
    mutation.mutate({ operationId: operation.id, action });
  };
  return (
    <div className="space-y-4 text-sm">
      {!compact ? <div className="flex items-center justify-between gap-3">
        <h3 className="font-medium">{t("accountsManager.controls.session")}</h3>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => { void current.refetch(); if (trackedId) void observed.refetch(); }}>{t("accountsManager.controls.refreshSession")}</Button>
      </div> : null}
      {!compact ? <p className="text-xs text-muted-foreground">{t("accountsManager.controls.noFallback")}</p> : null}
      {current.isPending ? <p role="status">{t("accountsManager.controls.checkingSession")}</p> : null}
      {current.error ? <p role="alert" className="text-destructive">{accountControlMessage(current.error, t)}</p> : null}
      {localError ? <p role="alert" className="text-destructive">{localError}</p> : null}
      {binding ? (
        <section aria-label={t("accountsManager.controls.committed")} className={compact ? "text-xs text-muted-foreground" : "rounded-md border border-border p-3 space-y-1"}>
          {compact ? <p>{t("switchAgent.current")}: {binding.mode === "native" ? t("accountsManager.controls.native") : identity(binding.accountId)}</p> : <>
            <h4 className="font-medium">{t("accountsManager.controls.committed")}</h4>
            <p>{binding.mode === "native" ? t("accountsManager.controls.native") : binding.accountId}</p>
            <p className="text-xs text-muted-foreground">{t("accountsManager.controls.binding", { mode: binding.mode, revision: binding.revision, provider: binding.provider })}</p>
            <p className="text-xs">{unavailable ? t("accountsManager.controls.stale") : t("accountsManager.controls.available")}</p>
          </>}
          {binding.blocked ? <p role="status">{t("accountsManager.controls.blocked")}</p> : null}
        </section>
      ) : null}
      {unconfirmed ? <section aria-label={t("accountsManager.controls.unknownSwitch")} className="rounded-md border border-border p-3 space-y-2">
        <p className="break-all">{t("accountsManager.controls.unknownId", { id: submitted!.operationId })}</p>
        <p>{t("accountsManager.controls.unknownRequest")}</p>
        <Button size="sm" disabled={busy || observed.isFetching || unavailable} onClick={() => void observed.refetch()}>{t("accountsManager.controls.checkSwitch")}</Button>
        {observed.error instanceof AccountControlError && observed.error.status === 404 ? <Button size="sm" variant="outline" disabled={busy || unavailable} onClick={() => {
          if (inFlight.current) return;
          inFlight.current = true;
          mutation.mutate(submitted!);
        }}>{t("accountsManager.controls.resendSwitch")}</Button> : null}
      </section> : null}
      {observed.error ? <p role="alert" className="text-destructive">{accountControlMessage(observed.error, t)}</p> : null}
      {operation && (!compact || active || pendingCommit || submitted || mutation.variables || operation.recoveryRequired || operation.phase === "failed") ? (
        <section aria-label={t("accountsManager.controls.switchOperation")} className="rounded-md border border-border p-3 space-y-2" aria-live="polite">
          <h4 className="font-medium">{active || pendingCommit ? t("accountsManager.controls.pendingSwitch") : t("accountsManager.controls.lastSwitch")}</h4>
          {!compact ? <p className="break-all">{t("accountsManager.controls.operationId", { id: operation.id })}</p> : null}
          <p>{compact && pendingCommit ? t("accountsManager.controls.accepted") : t("accountsManager.controls.phase", { phase: operation.phase })}</p>
          {operation.errorCode ? <p>{t("accountsManager.controls.failureCode", { code: operation.errorCode })}</p> : null}
          {!compact ? <>
          <p>{t("accountsManager.controls.target", { target: operation.targetMode === "native" ? t("accountsManager.controls.native") : operation.targetAccountId })}</p>
          <p>{t("accountsManager.controls.revisions", { policy: operation.policy, source: operation.sourceRevision, target: operation.targetRevision })}</p>
          <p>{operation.newConversation ? t("accountsManager.controls.newConversation") : t("accountsManager.controls.preserveConversation")}</p>
          <p className="text-xs text-muted-foreground">{t("accountsManager.controls.accepted")}</p>
          </> : null}
          {operation.recoveryRequired ? <p role="status">{t("accountsManager.controls.switchRecovery")}</p> : null}
          <div className="flex gap-2">
            {canRetry ? <Button size="sm" disabled={busy || unavailable || observed.isFetching || observed.isError} onClick={() => change("retry")}>{t("accountsManager.controls.retrySwitch")}</Button> : null}
            {["requested", "waiting"].includes(operation.phase) ? <Button size="sm" variant="outline" disabled={busy || unavailable || observed.isFetching || observed.isError} onClick={() => change("cancel")}>{t("accountsManager.controls.cancelSwitch")}</Button> : null}
          </div>
        </section>
      ) : null}
      {mutation.error ? <div role="alert" className="text-destructive space-y-1"><p>{accountControlMessage(mutation.error, t)}</p><p className="break-all">{t("accountsManager.controls.submitted", { id: mutation.variables?.operationId })}</p></div> : null}
      {!unavailable ? (
        <fieldset className="space-y-3" disabled={controlsDisabled}>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">{t("accountsManager.controls.targetLabel")}</p>
            <SettingsOptionMenu
              aria-label={t("accountsManager.controls.targetLabel")}
              triggerRef={targetTrigger}
              triggerClassName="w-full justify-between"
              menuClassName="min-w-64 max-w-[calc(100vw-2rem)]"
              menuAlign="start"
              value={target}
              disabled={controlsDisabled}
              placeholder={t("accountsManager.controls.choose")}
              onChange={value => { if (!controlsDisabled) setTarget(value); }}
              options={[
                { value: "native", label: t("accountsManager.controls.chooseNative"), disabled: controlsDisabled },
                ...accounts.map((account,index) => ({ value: `managed:${account.id}`, disabled: controlsDisabled || !inventoryReady || account.verification !== "verified" || account.disabled || account.unavailable || account.status !== "active", label: compact
                ? `${identity(account.id)}${account.quotaSupported && usage[index] ? ` | ${accountUsageSummary(account, usage[index], t, i18n.resolvedLanguage)}` : ""}`
                : `${account.label || account.id} (${account.id}) | ${accountUsageSummary(account, usage[index], t, i18n.resolvedLanguage)}` })),
              ]}
            />
          </div>
          {!inventoryReady ? <p>{t("accountsManager.controls.inventoryUnavailable")}</p> : null}
          {selected && !compact ? <AccountUsage key={`${selected.id}:${selected.generation}`} account={selected} /> : null}
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">{t("accountsManager.controls.timing")}</p>
            <SettingsOptionMenu<typeof policy>
              aria-label={t("accountsManager.controls.timing")}
              triggerClassName="w-full justify-between"
              menuClassName="min-w-64 max-w-[calc(100vw-2rem)]"
              menuAlign="start"
              value={policy}
              disabled={controlsDisabled}
              placeholder={t("accountsManager.controls.chooseTiming")}
              onChange={value => { if (!controlsDisabled) setPolicy(value); }}
              options={[
                { value: "drain", label: t("accountsManager.controls.drain"), disabled: controlsDisabled },
                { value: "interrupt", label: t("accountsManager.controls.interrupt"), disabled: controlsDisabled },
              ]}
            />
          </div>
          {compact ? <details className="text-xs text-muted-foreground space-y-2">
            <summary className="cursor-pointer">{t("switchAgent.accountDetails")}</summary>
            <p className="break-all">{binding.accountId}</p>
            <p>{t("accountsManager.controls.binding", { mode: binding.mode, revision: binding.revision, provider: binding.provider })}</p>
            {operation ? <p className="break-all">{t("accountsManager.controls.operationId", { id: operation.id })}</p> : null}
            <label className="flex gap-2"><input type="checkbox" checked={newConversation} onChange={event => setNewConversation(event.target.checked)} />{t("accountsManager.controls.startNew")}</label>
            <Button size="sm" variant="outline" disabled={busy} onClick={() => { void current.refetch(); if (trackedId) void observed.refetch(); }}>{t("accountsManager.controls.refreshSession")}</Button>
          </details> : <label className="flex gap-2"><input type="checkbox" checked={newConversation} onChange={event => setNewConversation(event.target.checked)} />{t("accountsManager.controls.startNew")}</label>}
          <Button disabled={!selectedReady || !policy} onClick={request}>{t(compact ? "switchAgent.accountAction" : "accountsManager.controls.requestSwitch")}</Button>
        </fieldset>
      ) : null}
    </div>
  );
}
