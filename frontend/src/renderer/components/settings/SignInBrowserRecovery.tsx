import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import type { MessageKey } from "../../i18n";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

export function signInAuthorizationURL(raw: string | undefined): string | undefined {
  if (!raw) return undefined;
  try {
    const url = new URL(raw);
    return url.protocol === "https:" && !url.username && !url.password ? url.href : undefined;
  } catch { return undefined; }
}

export function SignInBrowserRecovery({ authorizationUrl, expiresAt, onOpened }: {
  authorizationUrl?: string;
  expiresAt: string;
  onOpened: () => void;
}) {
  const { t } = useTranslation();
  const url = signInAuthorizationURL(authorizationUrl);
  const expires = Date.parse(expiresAt);
  const [now, setNow] = useState(Date.now);
  const [busy, setBusy] = useState(false);
  const inFlight = useRef(false);
  const [message, setMessage] = useState<MessageKey | null>(null);
  useEffect(() => {
    setNow(Date.now());
    if (!Number.isFinite(expires)) return;
    const timer = setTimeout(() => setNow(Date.now()), Math.min(2_147_483_647, Math.max(0, expires - Date.now())));
    return () => clearTimeout(timer);
  }, [expires]);
  if (!url) return null;
  const expired = !Number.isFinite(expires) || expires <= now;
  const act = async (copy: boolean) => {
    if (expired || Date.now() >= expires || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setMessage(null);
    try {
      if (copy) {
        await navigator.clipboard.writeText(url);
        setMessage("browser.urlCopied");
      } else {
        await aoBridge.app.openExternal(url);
        onOpened();
      }
    } catch {
      setMessage(copy ? "browser.urlCopyFailed" : "accountsManager.browser.openFailed");
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };
  return <div className="space-y-2">
    {expired ? <p role="status">{t(Number.isFinite(expires) ? "accountsManager.browser.linkExpired" : "accountsManager.errors.start")}</p> : <>
      <Input aria-label={t("accountsManager.browser.link")} readOnly value={url} />
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => void act(false)}>{t("accountsManager.browser.open")}</Button>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => void act(true)}>{t("accountsManager.browser.copy")}</Button>
      </div>
    </>}
    {message ? <p role="status">{t(message)}</p> : null}
  </div>;
}
