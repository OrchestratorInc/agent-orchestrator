import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AccountControlError, accountControlMessage, accountRemovalIsActive,
  fetchAccountRemoval, fetchAccountRemovalImpact, readAccountRemovalReferences, saveAccountRemovalReference, startAccountRemoval,
  type AccountRemovalReference, type AccountRemovalRequest,
} from "../../lib/accounts-manager-controls";
import { accountsManagerQueryKey } from "../../hooks/useAccountsManagerQuery";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  settingsDialogBodyClass,
  settingsDialogContentClass,
  settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";

const referencesKey = ["accounts-manager", "removal-references"];

export function AccountRemovalControl({
  accountId,
  onOptimisticRemove,
  onRemovalFailed,
  onRemovalAccepted,
  onCancel,
}: {
  accountId: string;
  onOptimisticRemove?: () => void;
  onRemovalFailed?: () => void;
  onRemovalAccepted?: () => void;
  onCancel?: () => void;
}) {
  return <RemovalPanel key={accountId} accountId={accountId} onOptimisticRemove={onOptimisticRemove} onRemovalFailed={onRemovalFailed} onRemovalAccepted={onRemovalAccepted} onCancel={onCancel} />;
}

function RemovalPanel({ accountId, onOptimisticRemove, onRemovalFailed, onRemovalAccepted, onCancel }: {
  accountId: string;
  onOptimisticRemove?: () => void;
  onRemovalFailed?: () => void;
  onRemovalAccepted?: () => void;
  onCancel?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [initial] = useState(() => {
    try { return { reference: readAccountRemovalReferences().find(value => value.accountId === accountId), unreadable: false }; }
    catch { return { reference: undefined, unreadable: true }; }
  });
  const [reference, setReference] = useState(initial.reference);
  const [localError, setLocalError] = useState(initial.unreadable ? t("accountsManager.controls.removalSavedError") : "");
  const inFlight = useRef(false);
  const persist = (next?: AccountRemovalReference) => {
    try {
      client.setQueryData(referencesKey, saveAccountRemovalReference(accountId, next));
      setReference(next);
      return true;
    } catch { setLocalError(t("accountsManager.controls.removalSaveError")); return false; }
  };
  const impactKey = ["accounts-manager", "removal-impact", accountId];
  const operationKey = (id?: string) => ["accounts-manager", "removal", accountId, id];
  const mutation = useMutation({
    mutationFn: (request: AccountRemovalRequest) => startAccountRemoval(accountId, request),
    retry: false,
    onMutate: async request => { await client.cancelQueries({ queryKey: operationKey(request.operationId) }); },
    onSuccess: result => {
      client.setQueryData(operationKey(result.id), result);
      onRemovalAccepted?.();
      onCancel?.();
    },
    onError: (error) => {
      onRemovalFailed?.();
      if (error instanceof AccountControlError && [400, 409].includes(error.status)) {
        persist(undefined);
      }
    },
    onSettled: async () => {
      await client.invalidateQueries({ queryKey: impactKey });
      inFlight.current = false;
    },
  });
  const operationQuery = useQuery({
    queryKey: operationKey(reference?.operationId),
    queryFn: ({ signal }) => fetchAccountRemoval(accountId, reference!.operationId, signal),
    enabled: Boolean(reference) && !mutation.isPending,
    retry: false,
    refetchInterval: query => query.state.data && !accountRemovalIsActive(query.state.data) ? false : 1500,
  });
  const operation = operationQuery.data;
  const impactQuery = useQuery({
    queryKey: impactKey,
    queryFn: ({ signal }) => fetchAccountRemovalImpact(accountId, signal),
    enabled: !reference && !localError,
    retry: false,
  });
  const impact = operation?.impact ?? impactQuery.data;
  const busy = mutation.isPending || impactQuery.isFetching;
  useEffect(() => {
    if (operation?.phase === "complete") void client.invalidateQueries({ queryKey: ["accounts-manager", "accounts"] });
  }, [client, operation?.id, operation?.phase]);
  const request = (body: AccountRemovalRequest) => {
    if (inFlight.current || busy || localError || !impact) return;
    if (!persist({ accountId, operationId: body.operationId, request: body })) return;
    inFlight.current = true;
    onOptimisticRemove?.();
    mutation.mutate(body);
  };
  const requestRemoval = () => {
    const body = reference?.request ?? (impact ? { operationId: crypto.randomUUID(), expectedRevision: impact.revision, confirmed: true as const } : undefined);
    if (body) request(body);
  };
  const error = localError || (reference ? operationQuery.error : impactQuery.error) || mutation.error;
  return <div className="space-y-3 text-sm">
    {error ? <p role="alert" className="text-sm text-destructive">{localError || accountControlMessage(error, t)}</p> : null}
    {operation ? <section aria-label={t("accountsManager.controls.removalOperation")} className="space-y-2" aria-live="polite">
      <h4 className="font-medium">{operation.phase === "complete" ? t("accountsManager.controls.removalComplete") : t("accountsManager.controls.removalOperation")}</h4>
      <p>{t("accountsManager.controls.phase", { phase: operation.phase })}</p>
      <p>{operation.canCancel ? t("accountsManager.controls.canCancel") : t("accountsManager.controls.cannotCancel")}</p>
      {operation.errorCode ? <p>{t("accountsManager.controls.failureCode", { code: operation.errorCode })}</p> : null}
      {operation.recoveryRequired ? <p>{t("accountsManager.controls.removalRecoveryRequired")}</p> : null}
      <Button type="button" size="sm" variant="outline" disabled={operationQuery.isFetching} onClick={() => void operationQuery.refetch()}>{t("accountsManager.controls.refreshRemoval")}</Button>
    </section> : null}
    <div className="flex justify-end gap-2">
      <Button type="button" size="sm" variant="outline" disabled={mutation.isPending} onClick={onCancel}>{t("confirm.cancel")}</Button>
      <Button type="button" size="sm" variant="primary" disabled={Boolean(reference) || busy || Boolean(error) || !impact} onClick={requestRemoval}>{t("accountsManager.controls.requestRemoval")}</Button>
    </div>
  </div>;
}

export function AccountRemovalDialog({ accountId, open, onOpenChange, onOptimisticRemove, onRemovalFailed, onRemovalAccepted }: {
  accountId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOptimisticRemove?: () => void;
  onRemovalFailed?: () => void;
  onRemovalAccepted?: () => void;
}) {
  const { t } = useTranslation();
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className={`${settingsDialogContentClass} max-h-[85vh]`}>
      <DialogHeader className={`${settingsDialogHeaderClass} !p-(--space-4) border-b-0`}><DialogTitle>{t("accountsManager.controls.removalTitle")}</DialogTitle><DialogDescription>{t("accountsManager.controls.removalDialogDescription")}</DialogDescription></DialogHeader>
      {open ? <div className={`${settingsDialogBodyClass} !p-(--space-4) pt-0`}><AccountRemovalControl accountId={accountId} onOptimisticRemove={onOptimisticRemove} onRemovalFailed={onRemovalFailed} onRemovalAccepted={onRemovalAccepted} onCancel={() => onOpenChange(false)} /></div> : null}
    </DialogContent>
  </Dialog>;
}

export function AccountRemovalReconciler({
  onPending,
  onSettled,
}: {
  onPending: (accountId: string) => void;
  onSettled: (accountId: string) => void;
}) {
  const client = useQueryClient();
  const references = useQuery({ queryKey: referencesKey, queryFn: readAccountRemovalReferences, retry: false });
  const saved = references.data ?? [];
  const operations = useQueries({
    queries: saved.map(reference => ({
      queryKey: ["accounts-manager", "removal", reference.accountId, reference.operationId],
      queryFn: ({ signal }: { signal: AbortSignal }) => fetchAccountRemoval(reference.accountId, reference.operationId, signal),
      retry: false,
      refetchInterval: (query: { state: { data?: Awaited<ReturnType<typeof fetchAccountRemoval>> } }) =>
        query.state.data && !accountRemovalIsActive(query.state.data) ? false : 1500,
    })),
  });
  const handled = useRef(new Set<string>());
  const announced = useRef(new Set<string>());
  useEffect(() => {
    saved.forEach((reference, index) => {
      const query = operations[index];
      const key = `${reference.accountId}:${reference.operationId}`;
      if (query?.data && accountRemovalIsActive(query.data)) {
        if (query.data.phase === "recovery_required") {
          const recoveryKey = `${key}:recovery`;
          if (!handled.current.has(recoveryKey)) {
            handled.current.add(recoveryKey);
            void client.invalidateQueries({ queryKey: accountsManagerQueryKey }).finally(() => onSettled(reference.accountId));
          }
        } else {
          if (!announced.current.has(key)) {
            announced.current.add(key);
            onPending(reference.accountId);
          }
        }
        return;
      }
      const missing = query?.error instanceof AccountControlError && query.error.status === 404 && query.failureCount >= 3;
      if (!missing && !(query?.data && !accountRemovalIsActive(query.data))) return;
      if (handled.current.has(key)) return;
      handled.current.add(key);
      void (async () => {
        if (query?.data) await client.invalidateQueries({ queryKey: accountsManagerQueryKey });
        try {
          const current = readAccountRemovalReferences();
          if (current.some(value => value.accountId === reference.accountId && value.operationId === reference.operationId)) {
            client.setQueryData(referencesKey, saveAccountRemovalReference(reference.accountId));
          }
        } catch {
          /* Preserve an unreadable reference for a later recovery attempt. */
        }
        onSettled(reference.accountId);
      })();
    });
  }, [client, onPending, onSettled, operations, saved]);
  return null;
}

export function AccountRemovalRecovery() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const references = useQuery({ queryKey: referencesKey, queryFn: readAccountRemovalReferences, retry: false });
  const [selected, setSelected] = useState("");
  const [accountId, setAccountId] = useState("");
  const [operationId, setOperationId] = useState("");
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const inspect = async () => {
    if (checking) return;
    setChecking(true); setError("");
    try {
      await fetchAccountRemoval(accountId, operationId);
      const existing = readAccountRemovalReferences().find(value => value.accountId === accountId);
      if (existing && existing.operationId !== operationId) throw new AccountControlError(409);
      client.setQueryData(referencesKey, saveAccountRemovalReference(accountId, existing ?? { accountId, operationId }));
      setSelected(accountId);
    } catch (cause) { setError(accountControlMessage(cause, t)); }
    finally { setChecking(false); }
  };
  return <details className="rounded-md border border-border p-3 text-sm">
    <summary className="cursor-pointer font-medium">{t("accountsManager.controls.recoverySummary", { saved: references.data?.length ?? 0 })}</summary>
    <div className="mt-3 space-y-3">
      <p className="text-xs text-muted-foreground">{t("accountsManager.controls.recoveryDescription")}</p>
      {references.error ? <p role="alert">{t("accountsManager.controls.referencesUnavailable")}</p> : null}
      {references.data?.map(reference => <Button key={reference.accountId} size="sm" variant="outline" className="max-w-full break-all" onClick={() => setSelected(reference.accountId)}>{t("accountsManager.controls.inspectReference", { account: reference.accountId, operation: reference.operationId })}</Button>)}
      <Input aria-label={t("accountsManager.controls.recoveryAccount")} value={accountId} onChange={event => setAccountId(event.target.value)} autoComplete="off" />
      <Input aria-label={t("accountsManager.controls.recoveryRemoval")} value={operationId} onChange={event => setOperationId(event.target.value)} autoComplete="off" />
      <Button size="sm" disabled={checking || !accountId || !operationId} onClick={() => void inspect()}>{t("accountsManager.controls.inspectRemoval")}</Button>
      {error ? <p role="alert">{error}</p> : null}
      {selected ? <AccountRemovalDialog accountId={selected} open onOpenChange={open => { if (!open) setSelected(""); }} /> : null}
    </div>
  </details>;
}
