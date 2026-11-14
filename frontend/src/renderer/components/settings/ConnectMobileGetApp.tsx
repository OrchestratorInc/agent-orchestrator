import { QrCode } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { QrCanvas } from "./QrCanvas";

export const IOS_APP_STORE_URL = "https://apps.apple.com/app/ao-mobile/id6792552173";

export const ANDROID_PLAY_STORE_URL = "https://play.google.com/store/apps/details?id=aoagents.dev&pcampaignid=web_share";

const STORE_QR_SIZE = 140;

export function ConnectMobileGetApp() {
	const { t } = useTranslation();
	const [showQR, setShowQR] = useState(false);

	return (
		<div className="flex flex-col">
			<span className="px-3 py-3 text-subtitle leading-(--leading-settings-mobile-title) text-settings-label">{t("mobile.getApp")}</span>

			<div className="flex items-center justify-between gap-3 px-3 py-3">
				<div className="flex min-w-0 flex-col">
					<span className="text-sm leading-5 text-settings-label">{t("mobile.ios")}</span>
					<span className="text-caption leading-(--leading-settings-mobile-hint) text-settings-muted">
						{t("mobile.iosHint")}
					</span>
				</div>
				<div className="flex shrink-0 items-center gap-1.5">
					<Button
						type="button"
						variant="secondary"
						aria-label={t("mobile.iosStoreAria")}
						onClick={() => void aoBridge.app.openExternal(IOS_APP_STORE_URL)}
					>
						{t("mobile.getApp")}
					</Button>
					<Button
						type="button"
						variant="quiet"
						size="icon"
						aria-label={showQR ? t("mobile.hideQR") : t("mobile.showQR")}
						aria-expanded={showQR}
						data-active={showQR}
						onClick={() => setShowQR((v) => !v)}
					>
						<QrCode className="size-4" aria-hidden="true" />
					</Button>
				</div>
			</div>

			<div
				data-testid="ios-store-qr"
				className={cn(
					"grid transition-[grid-template-rows] duration-300 ease-out",
					showQR ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
				)}
				aria-hidden={!showQR}
			>
				<div className="overflow-hidden px-4">
					<div
						className={cn(
							"flex flex-col items-center pt-3 transition-opacity duration-300 ease-out",
							showQR ? "opacity-100" : "opacity-0",
						)}
					>
						<div className="rounded-md border border-(--color-border-settings-input) bg-white p-2">
							<QrCanvas value={IOS_APP_STORE_URL} size={STORE_QR_SIZE} showLogo={false} cache className="block" />
						</div>
						<p className="mt-2 text-caption text-settings-muted">{t("mobile.qrHint")}</p>
					</div>
				</div>
			</div>

			<div className="flex items-center justify-between gap-3 px-3 py-3">
				<div className="flex min-w-0 flex-col">
					<span className="text-sm leading-5 text-settings-label">{t("mobile.android")}</span>
					<span className="text-caption leading-(--leading-settings-mobile-hint) text-settings-muted">
						{t("mobile.androidHint")}
					</span>
				</div>
				<Button
					type="button"
					variant="secondary"
					aria-label={t("mobile.androidSignupAria")}
					onClick={() => void aoBridge.app.openExternal(ANDROID_PLAY_STORE_URL)}
				>
					{t("mobile.getApp")}
				</Button>
			</div>
		</div>
	);
}
