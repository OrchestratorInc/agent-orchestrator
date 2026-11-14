import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { RadioGroup } from "radix-ui";
import { Switch } from "../ui/switch";
import { cn } from "../../lib/utils";

export type SetupMode = "lan" | "tailscale";

export function reasonMessage(reason: string, t: TFunction): string | undefined {
	switch (reason) {
		case "no_cli":
		case "no_magicdns":
			return t("mobile.securePairing.noCli");
		case "no_certs":
			return t("mobile.securePairing.noCerts");
		case "serve_failed":
			return t("mobile.securePairing.serveFailed");
		case "port_mismatch":
			return t("mobile.securePairing.portMismatch");
		case "clear_failed":
			return t("mobile.securePairing.clearFailed");
		default:
			return undefined;
	}
}

interface ConnectMobileSetupProps {
	mode: SetupMode;
	onModeChange: (mode: SetupMode) => void;
	enabled: boolean;
	busy?: boolean;
	secure: { enabled: boolean; reason: string };
	onSecureChange: (on: boolean) => void;
}

export function ConnectMobileSetup({ mode, onModeChange, enabled, busy = false, secure, onSecureChange }: ConnectMobileSetupProps) {
	const { t } = useTranslation();
	const reasonText = reasonMessage(secure.reason, t);

	return (
		<div className="flex w-full flex-col items-center">
			<RadioGroup.Root
				value={mode}
				onValueChange={(value) => onModeChange(value as SetupMode)}
				aria-label={t("mobile.connectionMethod")}
				className="settings-segment rounded-md"
			>
				<RadioGroup.Item value="lan" tabIndex={enabled ? 0 : -1} className="settings-segment-item rounded-md">
					{t("mobile.lan")}
				</RadioGroup.Item>
				<RadioGroup.Item value="tailscale" tabIndex={enabled ? 0 : -1} className="settings-segment-item rounded-md">
					{t("mobile.tailscale")}
				</RadioGroup.Item>
			</RadioGroup.Root>

			{mode === "lan" ? (
				<div className="mt-3 w-full px-(--size-settings-mobile-details-pad-x)">
					<ol className="settings-mobile-steps">
						<li>{t("mobile.lan.step1")}</li>
						<li>{t("mobile.lan.step2")}</li>
						<li>{t("mobile.lan.step3")}</li>
					</ol>
				</div>
			) : (
				<div className="mt-3 w-full px-(--size-settings-mobile-details-pad-x)">
					<div className="relative flex items-start justify-between gap-3 rounded-(--radius-settings-dialog-lg) bg-[var(--color-bg-settings-input)] px-3.5 py-2.5">
						<div className="flex min-w-0 flex-col gap-1 pr-2">
							<span className="text-subtitle leading-(--leading-settings-mobile-title) text-settings-label">
								{t("mobile.securePairing")}
							</span>
							<span className="text-caption leading-(--leading-settings-mobile-hint) text-settings-muted">
								{t("mobile.securePairing.hint")}
							</span>
						</div>
						<Switch
							checked={secure.enabled}
							onCheckedChange={onSecureChange}
							disabled={!enabled || busy}
							aria-label={t("mobile.securePairing")}
							tabIndex={enabled ? 0 : -1}
							className={cn(
								"h-(--size-settings-mobile-switch-h) w-(--size-settings-mobile-switch-w) shrink-0",
								"**:data-[slot=switch-thumb]:size-5",
								"data-[state=checked]:**:data-[slot=switch-thumb]:translate-x-(--size-settings-mobile-switch-travel)",
								"data-[state=unchecked]:**:data-[slot=switch-thumb]:translate-x-0.5",
							)}
						/>
					</div>

					<p className="mt-3 text-caption leading-(--leading-settings-mobile-hint) text-settings-muted">
						{t("mobile.tailscale.iosHint")}
					</p>

					{secure.enabled && reasonText && (
						<p className="mt-3 text-caption leading-(--leading-settings-mobile-hint) text-warning">{reasonText}</p>
					)}

					<ol className="settings-mobile-steps mt-3">
						<li>{t("mobile.tailscale.step1")}</li>
						<li>
							{t("mobile.tailscale.step2Lead")}{" "}
							<span className="tracking-settings-mono text-settings-label">tailscale ip -4</span>{" "}
							{t("mobile.tailscale.step2Trail")}
						</li>
						<li>{t("mobile.tailscale.step3")}</li>
					</ol>
				</div>
			)}
		</div>
	);
}
