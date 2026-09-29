import {
  ChevronDown,
  ChevronRight,
  LoaderCircle,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { useEffect, useRef, useState, type RefObject } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../../i18n";
import { aoBridge } from "../../lib/bridge";
import { AccountControlError, accountControlMessage } from "../../lib/accounts-manager-controls";
import {
  accountsManagerQueryKey,
  addAccountsManagerAPIKey,
  cancelAccountsManagerOAuth,
  fetchAccountsManagerModels,
  importAccountsManagerCredential,
  refreshAccountsManagerAccount,
  renameAccountsManagerAccount,
  selectAccountsManagerSnapshot,
  setAccountsManagerDisabled,
  startAccountsManagerOAuth,
  updateAccountsManagerRouting,
  useAccountsManagerEvents,
  useAccountsManagerQuery,
  type AccountsManagerAccount,
  type AccountsManagerSnapshot,
} from "../../hooks/useAccountsManagerQuery";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Switch } from "../ui/switch";
import { AgentProviderGroup } from "./AgentProviderGroup";
import { SettingsSection } from "./SettingsSection";
import { AccountRemovalDialog, AccountRemovalRecovery } from "./AccountRemovalControl";
import { AccountUsage } from "./AccountUsage";
import { SignInBrowserRecovery, signInAuthorizationURL } from "./SignInBrowserRecovery";

type Provider = "codex" | "claude";
type AddMethod = "device" | "browser" | "api-key" | "json";

function providerLabel(provider: string): string {
  return provider.charAt(0).toUpperCase() + provider.slice(1);
}

export function AccountsManagerSection({
  titleHidden,
}: {
  titleHidden?: boolean;
}) {
  const { t } = useTranslation();
  const query = useAccountsManagerQuery();
  useAccountsManagerEvents();
  const client = useQueryClient();
  const [expanded, setExpanded] = useState<Record<Provider, boolean>>({
    codex: true,
    claude: true,
  });
  const [adding, setAdding] = useState<Provider | null>(null);
  const [reconnecting, setReconnecting] =
    useState<AccountsManagerAccount | null>(null);
  const [method, setMethod] = useState<AddMethod>("device");
  const [secret, setSecret] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [search, setSearch] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [stateFilter, setStateFilter] = useState("all");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MessageKey | null>(null);
  const [cancellationReceipt, setCancellationReceipt] = useState("");
  const [requestId, setRequestId] = useState("");
  const showError = (message: MessageKey, cause: unknown) => {
    setError(cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED" ? "accountsManager.credentials.wrongMethod"
      : cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_INVALID_CREDENTIAL" ? "accountsManager.verification.rejected"
      : cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE" ? "accountsManager.verification.unavailable" : message);
    setRequestId(cause instanceof AccountControlError ? cause.requestId : "");
  };
  const fileRef = useRef<HTMLInputElement>(null);
  const cancelledOAuth = useRef(new Set<string>());
  const cancellations = useRef(new Map<string, Promise<void>>());
  const addGeneration = useRef(0);
  const activeOAuthID = useRef<string | null>(null);
  const [startedOAuth, setStartedOAuth] = useState<AccountsManagerSnapshot["oauthSessions"][number] | null>(null);
  const data = query.data;
  const update = (next: AccountsManagerSnapshot) =>
    client.setQueryData<AccountsManagerSnapshot>(
      accountsManagerQueryKey,
      (current) => selectAccountsManagerSnapshot(current, next),
    );
  const clearSensitive = () => {
    setSecret("");
    if (fileRef.current) fileRef.current.value = "";
  };
  const cancelOAuthOnce = async (id: string) => {
    if (cancelledOAuth.current.has(id)) return;
    let pending = cancellations.current.get(id);
    if (!pending) {
      pending = cancelAccountsManagerOAuth(id)
        .then(() => {
          cancelledOAuth.current.add(id);
          setCancellationReceipt(id);
        })
        .finally(() => cancellations.current.delete(id));
      cancellations.current.set(id, pending);
    }
    await pending;
  };

  const startOAuth = async (
    provider: Provider,
    mode: "device" | "callback",
  ) => {
    setBusy(true);
    setError(null);
    const generation = addGeneration.current;
    try {
      const session = reconnecting
        ? await startAccountsManagerOAuth(provider, mode, {
            accountId: reconnecting.id,
            generation: reconnecting.generation,
          })
        : await startAccountsManagerOAuth(provider, mode);
      if (generation !== addGeneration.current) {
        try {
          await cancelOAuthOnce(session.id);
        } catch (cause) {
          activeOAuthID.current = session.id;
          setAdding(provider);
          showError("accountsManager.errors.cancel", cause);
        }
        return;
      }
      activeOAuthID.current = session.id;
      const authorizationURL = signInAuthorizationURL(session.authorizationUrl);
      if (!authorizationURL) {
        await cancelOAuthOnce(session.id).catch(() => undefined);
        throw new Error("Missing authorization URL");
      }
      setStartedOAuth(session);
      try {
        await aoBridge.app.openExternal(authorizationURL);
      } catch {
        if (generation === addGeneration.current) {
          setError("accountsManager.browser.openFailed");
          setRequestId("");
        }
      }
    } catch (cause) {
      if (generation === addGeneration.current) showError("accountsManager.errors.start", cause);
    } finally {
      if (generation === addGeneration.current) setBusy(false);
    }
  };
  const submitKey = async (provider: Provider) => {
    setBusy(true);
    setError(null);
    try {
      update(
        await addAccountsManagerAPIKey(provider, secret, baseURL || undefined),
      );
      setAdding(null);
    } catch (cause) {
      showError("accountsManager.errors.addKey", cause);
    } finally {
      clearSensitive();
      setBusy(false);
    }
  };
  const submitFile = async (provider: Provider, file?: File) => {
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      if (file.size > 1_048_576 || !file.name.toLowerCase().endsWith(".json"))
        throw new Error("invalid");
      const parsed: unknown = JSON.parse(await file.text());
      if (!parsed || Array.isArray(parsed) || typeof parsed !== "object")
        throw new Error("invalid");
      update(
        await importAccountsManagerCredential(
          provider,
          file.name,
          parsed as Record<string, unknown>,
        ),
      );
      setAdding(null);
    } catch (cause) {
      showError("accountsManager.errors.import", cause);
    } finally {
      clearSensitive();
      setBusy(false);
    }
  };

  const waiting = data?.oauthSessions.find(
    (session) => session.status === "pending",
  ) ?? (startedOAuth && !data?.oauthSessions.some(session => session.id === startedOAuth.id) ? startedOAuth : undefined);
  useEffect(() => {
    if (startedOAuth && data?.oauthSessions.some(session => session.id === startedOAuth.id)) setStartedOAuth(null);
  }, [data?.oauthSessions, startedOAuth]);
  useEffect(() => {
    if (waiting) {
      activeOAuthID.current ??= waiting.id;
      setAdding(waiting.provider as Provider);
      setReconnecting(
        data?.accounts.find((account) => account.id === waiting.accountId) ??
          null,
      );
      setMethod(waiting.mode === "device" ? "device" : "browser");
    }
  }, [waiting?.id]);
  useEffect(() => {
    const active = data?.oauthSessions.find(
      (session) => session.id === activeOAuthID.current,
    );
    if (
      active?.status === "completed" ||
      (active?.status === "expired" && active.failureCode === "cancelled")
    ) {
      activeOAuthID.current = null;
      setStartedOAuth(null);
      setAdding(null);
      setReconnecting(null);
      setError(null);
    }
    if (
      active?.status === "failed" ||
      (active?.status === "expired" && active.failureCode !== "cancelled")
    ) {
      activeOAuthID.current = null;
      setStartedOAuth(null);
      setError(
        active.status === "expired"
          ? "accountsManager.errors.expired"
          : active.failureCode === "identity_mismatch"
            ? "accountsManager.errors.identityMismatch"
            : active.failureCode === "credential_changed"
              ? "accountsManager.errors.credentialChanged"
              : active.failureCode === "storage_unavailable"
                ? "accountsManager.errors.storage"
                : "accountsManager.errors.signIn",
      );
    }
  }, [data?.oauthSessions]);
  const closeAdd = async (provider: Provider) => {
    addGeneration.current++;
    const pending = data?.oauthSessions.find(
      (session) =>
        session.provider === provider && session.status === "pending",
    );
    const id = pending?.id ?? activeOAuthID.current;
    if (id) {
      setBusy(true);
      try {
        await cancelOAuthOnce(id);
      } catch (cause) {
        showError("accountsManager.errors.cancel", cause);
        return;
      } finally {
        setBusy(false);
      }
    }
    activeOAuthID.current = null;
    setStartedOAuth(null);
    setBusy(false);
    setAdding(null);
    setReconnecting(null);
    setError(null);
    clearSensitive();
  };

  return (
    <SettingsSection
      title={t("accountsManager.title")}
      titleHidden={titleHidden}
    >
      <div className="space-y-4">
        {query.error ? <p role="alert" className="text-sm text-destructive">{accountControlMessage(query.error, t)}</p> : null}
        {error && requestId ? <p role="alert" className="text-xs text-destructive">{t("accountsManager.controls.requestId", { id: requestId })}</p> : null}
        <AccountRemovalRecovery />
        {cancellationReceipt ? <section aria-label={t("accountsManager.controls.cancelAckTitle")} className="rounded-md border border-border p-3 text-xs space-y-1" aria-live="polite">
          <p>{t("accountsManager.controls.cancelAck", { id: cancellationReceipt })}</p>
          <p>{t("accountsManager.controls.cancelAckDescription")}</p>
          <p>{data?.oauthSessions.some(session => session.id === cancellationReceipt)
            ? t("accountsManager.controls.observedLogin", { state: data.oauthSessions.find(session => session.id === cancellationReceipt)!.status })
            : t("accountsManager.controls.loginUnknown")}</p>
          <Button size="sm" variant="ghost" onClick={() => setCancellationReceipt("")}>{t("accountsManager.controls.dismissAck")}</Button>
        </section> : null}
        <div className="flex flex-wrap gap-2">
          <Input
            className="min-w-40 flex-1"
            aria-label={t("accountsManager.search")}
            placeholder={t("accountsManager.search")}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <select
            className="rounded-md border border-input bg-background px-2 text-sm"
            aria-label={t("accountsManager.filter.provider")}
            value={providerFilter}
            onChange={(event) => setProviderFilter(event.target.value)}
          >
            <option value="all">{t("accountsManager.filter.providers")}</option>
            {(["codex", "claude"] as const).map((provider) => (
              <option key={provider} value={provider}>
                {providerLabel(provider)}
              </option>
            ))}
          </select>
          <select
            className="rounded-md border border-input bg-background px-2 text-sm"
            aria-label={t("accountsManager.filter.state")}
            value={stateFilter}
            onChange={(event) => setStateFilter(event.target.value)}
          >
            <option value="all">{t("accountsManager.filter.states")}</option>
            <option value="ready">{t("accountsManager.filter.available")}</option>
            <option value="disabled">
              {t("accountsManager.status.disabled")}
            </option>
            <option value="attention">
              {t("accountsManager.status.error")}
            </option>
          </select>
        </div>
        {data?.stale ? (
          <p className="text-xs text-muted-foreground">
            {t("accountsManager.stale")}
          </p>
        ) : null}
        {(["codex", "claude"] as const)
          .filter(
            (provider) =>
              providerFilter === "all" ||
              providerFilter === provider ||
              adding === provider,
          )
          .map((provider) => {
            const accounts =
              data?.accounts.filter(
                (account) => account.provider === provider,
              ) ?? [];
            const visible = accounts.filter((account) => {
              const matchesSearch =
                `${account.label ?? ""} ${account.email ?? ""} ${account.id}`
                  .toLocaleLowerCase()
                  .includes(search.trim().toLocaleLowerCase());
              const state = account.disabled
                ? "disabled"
                : account.unavailable || account.status !== "active"
                  ? "attention"
                  : "ready";
              return (
                matchesSearch &&
                (stateFilter === "all" || stateFilter === state)
              );
            });
            const unavailable =
              !data || data.availability !== "ready" || data.stale || query.isError;
            const routing = data?.routing?.find(
              (policy) => policy.provider === provider,
            ) ?? { provider, enabled: false, accountIds: [] };
            return (
              <AgentProviderGroup
                key={provider}
                provider={provider}
                name={providerLabel(provider)}
                summary={t("accountsManager.saved", { count: accounts.length })}
                expanded={expanded[provider]}
                onExpandedChange={(value) =>
                  setExpanded((current) => ({ ...current, [provider]: value }))
                }
                action={
                  <Button
                    size="icon"
                    variant="ghost"
                    disabled={unavailable || busy || Boolean(waiting)}
                    aria-label={t("accountsManager.addProvider", { provider })}
                    onClick={() => {
                      clearSensitive();
                      setBaseURL("");
                      addGeneration.current++;
                      setAdding(provider);
                      setReconnecting(null);
                      setMethod(provider === "codex" ? "device" : "browser");
                      setError(null);
                    }}
                  >
                    <Plus className="size-4" />
                  </Button>
                }
              >
                <RoutingPanel
                  provider={provider}
                  accounts={accounts}
                  policy={routing}
                  disabled={unavailable}
                  update={update}
                />
                {adding === provider ? (
                  <AddAccountPanel
                    provider={provider}
                    reconnecting={reconnecting}
                    method={method}
                    setMethod={(next) => {
                      if (next !== "api-key") clearSensitive();
                      setMethod(next);
                    }}
                    secret={secret}
                    setSecret={setSecret}
                    baseURL={baseURL}
                    setBaseURL={setBaseURL}
                    busy={busy || unavailable}
                    waiting={
                      waiting?.provider === provider ? waiting : undefined
                    }
                    error={error}
                    dismissError={() => {
                      setError(null);
                      clearSensitive();
                    }}
                    browserOpened={() => {
                      if (activeOAuthID.current === waiting?.id) setError(null);
                    }}
                    close={() => void closeAdd(provider)}
                    startOAuth={(mode) => void startOAuth(provider, mode)}
                    submitKey={() => void submitKey(provider)}
                    fileRef={fileRef}
                    submitFile={(file) => void submitFile(provider, file)}
                  />
                ) : null}
                {visible.length ? (
                  visible.map((account) => (
                    <AccountRow
                      key={account.id}
                      account={account}
                      disabled={unavailable}
                      signInDisabled={busy || Boolean(waiting)}
                      update={update}
                      reconnect={() => {
                        clearSensitive();
                        addGeneration.current++;
                        setReconnecting(account);
                        setAdding(provider);
                        setMethod(provider === "codex" ? "device" : "browser");
                        setError(null);
                      }}
                    />
                  ))
                ) : (
                  <p className="px-4 py-5 text-sm text-muted-foreground">
                    {unavailable && !accounts.length
                      ? t("accountsManager.loading")
                      : accounts.length
                        ? t("accountsManager.noMatches")
                        : t("accountsManager.emptyProvider", {
                            provider: providerLabel(provider),
                          })}
                  </p>
                )}
              </AgentProviderGroup>
            );
          })}
      </div>
    </SettingsSection>
  );
}

function RoutingPanel({
  provider,
  accounts,
  policy,
  disabled,
  update,
}: {
  provider: Provider;
  accounts: AccountsManagerAccount[];
  policy: { enabled: boolean; accountIds: string[] };
  disabled: boolean;
  update: (next: AccountsManagerSnapshot) => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MessageKey | null>(null);
  const [requestId, setRequestId] = useState("");
  const byID = new Map(accounts.map((account) => [account.id, account]));
  const selectedID =
    policy.accountIds.length === 1 ? policy.accountIds[0] : undefined;
  const selected = selectedID ? byID.get(selectedID) : undefined;
  const eligible = (account: AccountsManagerAccount) =>
    account.verification === "verified" && !account.disabled && !account.unavailable && account.status === "active";
  const save = async (enabled: boolean, accountIds: string[]) => {
    setBusy(true);
    setError(null);
    try {
      update(await updateAccountsManagerRouting(provider, enabled, accountIds));
    } catch (cause) {
      setError("accountsManager.errors.routing");
      setRequestId(cause instanceof AccountControlError ? cause.requestId : "");
    } finally {
      setBusy(false);
    }
  };
  const toggle = (checked: boolean) => {
    if (checked && (!selected || !eligible(selected))) return;
    void save(checked, selected ? [selected.id] : []);
  };

  return (
    <div className="border-b border-border bg-muted/10 px-4 py-3">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-sm font-medium">
            {t("accountsManager.routing.title")}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("accountsManager.routing.description")}
          </p>
          {provider === "codex" ? (
            <p className="mt-1 text-xs text-muted-foreground">
              {t("accountsManager.routing.nativeChat")}
            </p>
          ) : null}
        </div>
        <Switch
          aria-label={t("accountsManager.routing.toggle", { provider })}
          checked={policy.enabled}
          disabled={
            disabled ||
            busy ||
            (!policy.enabled && (!selected || !eligible(selected)))
          }
          onCheckedChange={toggle}
        />
      </div>
      {accounts.length ? (
        <div className="mt-3 space-y-1">
          {accounts.map((account) => {
            const included = selectedID === account.id;
            const selectable = eligible(account);
            const label =
              account.label ||
              account.email ||
              t(
                account.kind === "api_key"
                  ? "accountsManager.keyAccount"
                  : account.kind === "access_token" ? "accountsManager.tokenAccount"
                  : "accountsManager.oauthAccount",
                { provider: providerLabel(provider), id: account.id.slice(-6) },
              );
            return (
              <div
                key={account.id}
                className="flex min-h-9 items-center gap-2 rounded-md px-2 text-sm"
              >
                <span className="min-w-0 flex-1 truncate">{label}</span>
                {included ? (
                  <span className="text-xs text-muted-foreground">
                    {t("accountsManager.routing.preferred")}
                  </span>
                ) : null}
                {included ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={disabled || busy || policy.enabled}
                    onClick={() => void save(false, [])}
                  >
                    {t("shell.remove")}
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={disabled || busy || !selectable}
                    onClick={() => void save(policy.enabled, [account.id])}
                  >
                    {t("accountsManager.routing.use")}
                  </Button>
                )}
              </div>
            );
          })}
        </div>
      ) : null}
      {error ? (
        <p className="mt-2 text-xs text-destructive">{t(error)}</p>
      ) : null}
      {error && requestId ? <p role="alert" className="text-xs text-destructive">{t("accountsManager.controls.requestId", { id: requestId })}</p> : null}
    </div>
  );
}

function AddAccountPanel(props: {
  provider: Provider;
  reconnecting: AccountsManagerAccount | null;
  method: AddMethod;
  setMethod: (v: AddMethod) => void;
  secret: string;
  setSecret: (v: string) => void;
  baseURL: string;
  setBaseURL: (v: string) => void;
  busy: boolean;
  waiting?: AccountsManagerSnapshot["oauthSessions"][number];
  error: MessageKey | null;
  dismissError: () => void;
  browserOpened: () => void;
  close: () => void;
  startOAuth: (mode: "device" | "callback") => void;
  submitKey: () => void;
  fileRef: RefObject<HTMLInputElement | null>;
  submitFile: (file?: File) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="border-b border-border bg-muted/20 p-4">
      {props.reconnecting ? (
        <p className="mb-3 text-sm" role="status">
          {t("accountsManager.reconnect.description", {
            account:
              props.reconnecting.label ||
              props.reconnecting.email ||
              props.reconnecting.id.slice(-6),
          })}
        </p>
      ) : null}
      <div className="mb-3 flex flex-wrap gap-2">
        {(
          [
            ...(props.provider === "codex" ? (["device"] as const) : []),
            "browser",
            ...(props.reconnecting ? [] : (["api-key", "json"] as const)),
          ] as const
        ).map((method) => (
          <Button
            key={method}
            size="sm"
            variant={props.method === method ? "secondary" : "ghost"}
            disabled={props.busy || Boolean(props.waiting)}
            onClick={() => props.setMethod(method)}
          >
            {method === "device"
              ? t("accountsManager.method.device")
              : method === "browser"
                ? t("accountsManager.method.browser")
                : method === "api-key"
                  ? t("accountsManager.apiKey")
                  : t("accountsManager.method.json")}
          </Button>
        ))}
      </div>
      {props.waiting ? (
        <DeviceOrBrowserWaiting key={props.waiting.id} session={props.waiting} browserOpened={props.browserOpened} />
      ) : props.method === "device" ? (
        <div className="space-y-2">
          <p className="text-sm text-muted-foreground">
            {t("accountsManager.device.description")}
          </p>
          <Button
            disabled={props.busy}
            onClick={() => props.startOAuth("device")}
          >
            {props.busy ? (
              <LoaderCircle className="mr-2 size-4 animate-spin" />
            ) : null}
            {t("accountsManager.device.continue")}
          </Button>
        </div>
      ) : props.method === "browser" ? (
        <Button
          disabled={props.busy}
          onClick={() => props.startOAuth("callback")}
        >
          {props.busy ? (
            <LoaderCircle className="mr-2 size-4 animate-spin" />
          ) : null}
          {t("accountsManager.browser.continue")}
        </Button>
      ) : props.method === "api-key" ? (
        <div className="space-y-2">
          <Input
            aria-label={t("accountsManager.apiKey")}
            autoComplete="off"
            placeholder={t("accountsManager.apiKey")}
            type="password"
            value={props.secret}
            onChange={(e) => props.setSecret(e.target.value)}
          />
          <Input
            aria-label={t("accountsManager.baseURL")}
            placeholder={t("accountsManager.baseURLPlaceholder")}
            value={props.baseURL}
            onChange={(e) => props.setBaseURL(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t("accountsManager.credentials.keyFormHint")}</p>
          <Button
            disabled={props.busy || !props.secret.trim()}
            onClick={props.submitKey}
          >
            {t("accountsManager.add")}
          </Button>
        </div>
      ) : (
        <input
          ref={props.fileRef}
          type="file"
          aria-label={t("accountsManager.method.json")}
          accept="application/json,.json"
          disabled={props.busy}
          onChange={(e) => props.submitFile(e.currentTarget.files?.[0])}
        />
      )}
      {props.error ? (
        <div className="mt-3 flex items-center gap-2 text-sm text-destructive">
          <span>{t(props.error)}</span>
          <Button size="sm" variant="ghost" onClick={props.dismissError}>
            {t("accountsManager.dismiss")}
          </Button>
        </div>
      ) : null}
      <Button
        className="mt-2"
        size="sm"
        variant="ghost"
        onClick={props.close}
        disabled={
          props.busy && (props.method === "api-key" || props.method === "json")
        }
      >
        {t("confirm.cancel")}
      </Button>
    </div>
  );
}

function DeviceOrBrowserWaiting({
  session,
  browserOpened,
}: {
  session: AccountsManagerSnapshot["oauthSessions"][number];
  browserOpened: () => void;
}) {
  const { t } = useTranslation();
  const userCode = session?.userCode;
  return (
    <div className="space-y-2">
      {userCode ? <>
      <p className="text-sm text-muted-foreground">
        {t("accountsManager.device.enterCode")}
      </p>
      <div className="flex items-center gap-2">
        <code className="rounded-md bg-background px-3 py-2 text-base font-semibold tracking-wider">
          {userCode}
        </code>
        <Button
          size="sm"
          variant="secondary"
          onClick={() =>
            void navigator.clipboard.writeText(userCode).catch(() => undefined)
          }
        >
          {t("diffSelection.copy")}
        </Button>
      </div>
      </> : null}
      <SignInBrowserRecovery authorizationUrl={session.authorizationUrl} expiresAt={session.expiresAt} onOpened={browserOpened} />
      <p className="text-sm">{t("accountsManager.waiting")}</p>
    </div>
  );
}

function AccountRow({
  account,
  disabled,
  update,
  reconnect,
  signInDisabled,
}: {
  account: AccountsManagerAccount;
  disabled: boolean;
  update: (next: AccountsManagerSnapshot) => void;
  reconnect: () => void;
  signInDisabled: boolean;
}) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MessageKey | null>(null);
  const [requestId, setRequestId] = useState("");
  const [details, setDetails] = useState<{
    models?: string[];
  } | null>(null);
  const [editing, setEditing] = useState(false);
  const [draftLabel, setDraftLabel] = useState(account.label ?? "");
  useEffect(() => {
    if (!editing) setDraftLabel(account.label ?? "");
  }, [account.label, editing]);
  const label =
    account.label ||
    account.email ||
    t(
      account.kind === "api_key"
        ? "accountsManager.keyAccount"
        : account.kind === "access_token" ? "accountsManager.tokenAccount"
        : "accountsManager.oauthAccount",
      { provider: providerLabel(account.provider), id: account.id.slice(-6) },
    );
  const status = account.disabled
    ? t("accountsManager.status.disabled")
    : account.verification !== undefined && account.verification !== "verified"
      ? t(account.verification === "invalid" ? "accountsManager.verification.invalid" : "accountsManager.verification.unverified")
    : account.unavailable
      ? t("accountsManager.status.unavailable")
      : account.status === "refreshing"
        ? t("accountsManager.status.refreshing")
        : account.status === "error"
          ? t("accountsManager.status.error")
          : account.status === "active"
            ? t(
                account.kind === "api_key" || account.kind === "access_token"
                  ? account.verification === "verified" ? "accountsManager.verification.verified" : "accountsManager.status.saved"
                  : "accountsManager.status.ready",
              )
            : account.status === "pending"
              ? t("accountsManager.status.pending")
              : t("accountsManager.status.unknown");
  const action = async (task: () => Promise<AccountsManagerSnapshot>) => {
    setBusy(true);
    setError(null);
    try {
      update(await task());
      return true;
    } catch (cause) {
      setError(cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_INVALID_CREDENTIAL" ? "accountsManager.verification.rejected"
        : cause instanceof AccountControlError && cause.code === "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE" ? "accountsManager.verification.unavailable" : "accountsManager.errors.action");
      setRequestId(cause instanceof AccountControlError ? cause.requestId : "");
      return false;
    } finally {
      setBusy(false);
    }
  };
  const expand = async () => {
    const next = !open;
    setOpen(next);
    if (next && !details) {
      const [models] = await Promise.allSettled([
        fetchAccountsManagerModels(account.id),
      ]);
      setDetails({
        models:
          models.status === "fulfilled"
            ? models.value.models.map((model) => model.displayName || model.id)
            : undefined,
      });
    }
  };
  return (
    <div className="border-b border-border last:border-b-0">
      <div className="flex min-h-16 items-center gap-3 px-4 py-3">
        <button
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          onClick={() => void expand()}
        >
          {open ? (
            <ChevronDown className="size-4" />
          ) : (
            <ChevronRight className="size-4" />
          )}
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{label}</p>
            <p className="text-xs text-muted-foreground">{status}</p>
            {error ? (
              <p className="text-xs text-destructive">{t(error)}</p>
            ) : null}
            {error && requestId ? <p role="alert" className="text-xs text-destructive">{t("accountsManager.controls.requestId", { id: requestId })}</p> : null}
          </div>
        </button>
        <Button
          size="icon"
          variant="ghost"
          disabled={disabled || busy}
          aria-label={t(account.kind === "api_key" || account.kind === "access_token" || account.verification === "unverified" || account.verification === "invalid" ? "accountsManager.verification.verify" : "accountsManager.refresh")}
          onClick={() =>
            void action(() => refreshAccountsManagerAccount(account.id))
          }
        >
          {busy ? (
            <LoaderCircle className="size-4 animate-spin" />
          ) : (
            <RefreshCw className="size-4" />
          )}
        </Button>
        {account.kind === "oauth" ? (
          <Button
            size="sm"
            variant="ghost"
            disabled={
              disabled || busy || signInDisabled || !account.reconnectSupported
            }
            title={
              account.reconnectSupported
                ? undefined
                : t("accountsManager.reconnect.unavailable")
            }
            onClick={reconnect}
          >
            {t("accountsManager.reconnect")}
          </Button>
        ) : null}
        <Button
          size="sm"
          variant="ghost"
          disabled={disabled || busy}
          onClick={() =>
            void action(() =>
              setAccountsManagerDisabled(account.id, !account.disabled),
            )
          }
        >
          {account.disabled
            ? t("accountsManager.enable")
            : t("accountsManager.disable")}
        </Button>
        <Button
            size="icon"
            variant="ghost"
            disabled={disabled || busy}
            aria-label={t("accountsManager.remove")}
            onClick={() => setConfirm(true)}
          >
            <Trash2 className="size-4" />
          </Button>
        <AccountRemovalDialog accountId={account.id} open={confirm} onOpenChange={setConfirm} />
      </div>
      {open ? (
        <div className="space-y-2 border-t border-border px-10 py-3 text-xs text-muted-foreground">
          {editing ? (
            <form
              className="flex items-center gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                if (!busy)
                  void action(() =>
                    renameAccountsManagerAccount(
                      account.id,
                      draftLabel,
                      account.generation,
                    ),
                  ).then((saved) => {
                    if (saved) setEditing(false);
                  });
              }}
            >
              <Input
                autoFocus
                aria-label={t("accountsManager.label")}
                maxLength={80}
                value={draftLabel}
                disabled={disabled || busy}
                onChange={(event) => setDraftLabel(event.target.value)}
              />
              <Button type="submit" size="sm" disabled={disabled || busy}>
                {t("accountsManager.saveLabel")}
              </Button>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => setEditing(false)}
              >
                {t("confirm.cancel")}
              </Button>
            </form>
          ) : (
            <Button
              size="sm"
              variant="outline"
              disabled={disabled || busy}
              onClick={() => setEditing(true)}
            >
              {t("accountsManager.rename")}
            </Button>
          )}
          {account.kind === "oauth" && !account.reconnectSupported ? (
            <p>{t("accountsManager.reconnect.unavailable")}</p>
          ) : null}
          {account.kind === "access_token" ? <p>{t("accountsManager.credentials.legacyToken")}</p> : null}
          <p>
            {details?.models === undefined && details
              ? t("accountsManager.status.unknown")
              : details
                ? t("accountsManager.models", {
                      count: details.models?.length ?? 0,
                    })
                : t("accountsManager.loadingDetails")}
          </p>
          {account.cooldowns?.[0]?.retryAt ? (
            <p>
              {t("accountsManager.cooldown", {
                time: new Date(account.cooldowns[0].retryAt).toLocaleString(
                  i18n.resolvedLanguage,
                ),
              })}
            </p>
          ) : null}
          <AccountUsage key={`${account.id}:${account.generation}:${account.updatedAt}`} account={account} />
        </div>
      ) : null}
    </div>
  );
}
