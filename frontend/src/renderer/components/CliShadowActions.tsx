import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CliShadowStatus } from "../../shared/cli-shadow";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { isMacPlatform } from "../lib/platform";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";

type Shadowed = Extract<CliShadowStatus, { kind: "shadowed" }>;

// Details and the actions sit below the hook that holds the status, and a
// version check or a fix from either one has to reach it.
const refreshers = new Set<(status: CliShadowStatus) => void>();
let refreshSequence = 0;

async function refreshCliShadowStatus(identity?: string): Promise<void> {
	const request = ++refreshSequence;
	const status = await aoBridge.cliShadow.inspect(identity);
	if (request !== refreshSequence) return;
	for (const refresh of refreshers) refresh(status);
}

export function useCliShadowStatus(): CliShadowStatus | null {
	const [status, setStatus] = useState<CliShadowStatus | null>(null);
	const latest = useRef(0);
	const load = useCallback(async (identity?: string) => {
		const request = ++latest.current;
		try {
			const next = await aoBridge.cliShadow.inspect(identity);
			if (request === latest.current) setStatus(next);
		} catch {
			// An advisory check that fails has nothing to tell the user.
		}
	}, []);
	useEffect(() => {
		void load();
		const refresh = (next: CliShadowStatus) => {
			latest.current += 1;
			setStatus(next);
		};
		refreshers.add(refresh);
		return () => {
			refreshers.delete(refresh);
			latest.current += 1;
		};
	}, [load]);
	return status;
}

export function useCliShadowTitle(status: CliShadowStatus | null): string | null {
	const { t } = useTranslation();
	if (status?.kind !== "shadowed") return null;
	const version = status.shadowVersion ?? "";
	if (status.relation === "older") return t("cliShadow.titleOlder", { version });
	if (status.relation === "newer") return t("cliShadow.titleNewer", { version });
	return t("cliShadow.titleDifferent");
}

// Keeps each path segment whole on a line of its own when it fits, so a line
// only ends at a slash and never inside a file name.
function PathText({ path }: { path: string }) {
	return (
		<span className="font-mono">
			{path.split(/(?<=\/)/).map((segment, index) => (
				// biome-ignore lint/suspicious/noArrayIndexKey: segments of one fixed string, never reordered.
				<span key={index} className="inline-block max-w-full wrap-anywhere">{segment}</span>
			))}
		</span>
	);
}

export function CliShadowDetails({ status, className }: { status: Shadowed; className?: string }) {
	const { t } = useTranslation();
	const unknown = t("cliShadow.versionUnknown");
	const [checking, setChecking] = useState(false);
	return (
		<details className={cn("text-xs", className)}>
			<summary className="cursor-pointer select-none">{t("cliShadow.details")}</summary>
			{!status.versionChecked ? (
				<Button type="button" variant="link" disabled={checking} onClick={() => {
					setChecking(true);
					void refreshCliShadowStatus(status.shadowIdentity).catch(() => undefined).finally(() => setChecking(false));
				}}>
					{t("cliShadow.checkVersion")}
				</Button>
			) : null}
			{!status.versionChecked ? <p>{t("cliShadow.checkVersionDescription")}</p> : null}
			<dl className="mt-1 flex flex-col gap-1.5 leading-relaxed">
				<div>
					<dt>{t("cliShadow.terminalRuns", { version: status.shadowVersion ?? unknown })}</dt>
					<dd><PathText path={status.shadowPath} /></dd>
				</div>
				<div>
					<dt>{t("cliShadow.appHas", { version: status.bundledVersion ?? unknown })}</dt>
					<dd><PathText path={status.bundledPath} /></dd>
				</div>
				{status.fix.kind === "manual" ? (
					<div>
						<dt>{t("cliShadow.removeWith")}</dt>
						<dd className="font-mono wrap-anywhere">{status.fix.command}</dd>
					</div>
				) : null}
			</dl>
		</details>
	);
}

export function CliShadowActions({ status }: { status: Shadowed }) {
	const { t } = useTranslation();
	const [confirming, setConfirming] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [copied, setCopied] = useState(false);
	const { fix } = status;

	const replace = async () => {
		setBusy(true);
		setError(null);
		try {
			const result = await aoBridge.cliShadow.replace(status.shadowPath, status.shadowTarget, status.shadowIdentity);
			if (result.ok) setConfirming(false);
			else setError(result.message);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("cliShadow.replaceFailed"));
		} finally {
			setBusy(false);
			void refreshCliShadowStatus().catch(() => undefined);
		}
	};

	return (
		<div className="flex flex-wrap items-center gap-2">
			{fix.kind === "replace" ? (
				<Button type="button" variant="primary" disabled={busy} onClick={() => { setError(null); setConfirming(true); }}>
					{t("cliShadow.useAppAo")}
				</Button>
			) : (
				<>
					<Button type="button" variant="secondary" onClick={() => void aoBridge.cliShadow.reveal().catch(() => undefined)}>
						{isMacPlatform() ? t("cliShadow.showInFinder") : t("cliShadow.showInFolder")}
					</Button>
					<Button
						type="button"
						variant="secondary"
						onClick={() => {
							void aoBridge.clipboard.writeText(fix.command).then(() => setCopied(true)).catch(() => undefined);
						}}
					>
						{copied ? t("cliShadow.copied") : t("cliShadow.copyCommand")}
					</Button>
				</>
			)}
			<ConfirmDialog
				open={confirming}
				title={t("cliShadow.confirmTitle")}
				description={
					fix.kind === "replace" && fix.replaces === "link"
						? t("cliShadow.confirmLink", { path: status.shadowPath, target: status.shadowTarget })
						: t("cliShadow.confirmFile", { path: status.shadowPath })
				}
				confirmLabel={t("cliShadow.useAppAo")}
				destructive={fix.kind === "replace" && fix.replaces === "file"}
				busy={busy}
				confirmDisabled={fix.kind !== "replace"}
				error={error}
				onOpenChange={(open) => {
					if (!open) { setConfirming(false); setError(null); }
				}}
				onConfirm={() => void replace()}
			/>
		</div>
	);
}
