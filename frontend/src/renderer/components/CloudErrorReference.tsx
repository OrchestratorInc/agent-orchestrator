import { useTranslation } from "react-i18next";
import type { CloudErrorReference as Reference } from "../lib/error-origin";
import { cn } from "../lib/utils";
import { CopyButton } from "./chat/CopyButton";
import { TooltipProvider } from "./ui/tooltip";

/**
 * Beside an AO Cloud error: whether AO Cloud answered with it or could not be
 * reached, and the start of the request id AO Cloud's logs carry, with a copy
 * of the whole id. Text inherits the surrounding error's color.
 */
export function CloudErrorReference({ reference, className }: { reference: Reference; className?: string }) {
	const { t } = useTranslation();
	return (
		<span className={cn("inline-flex min-w-0 flex-wrap items-center gap-x-1.5 text-xs opacity-80", className)} data-testid="cloud-error-reference">
			<span>{reference.origin === "network" ? t("cloud.errorOrigin.network") : t("cloud.errorOrigin.cloud")}</span>
			{reference.requestId ? (
				<span className="inline-flex items-center gap-0.5">
					<span className="font-mono tabular-nums">{t("cloud.errorOrigin.reference", { id: reference.requestId.slice(0, 8) })}</span>
					{/* Its own provider: toasts and alerts can render outside the app's. */}
					<TooltipProvider>
						<CopyButton
							compact
							size="icon-xs"
							text={reference.requestId}
							label={t("cloud.errorOrigin.copyReference")}
							copiedLabel={t("cloud.errorOrigin.referenceCopied")}
						/>
					</TooltipProvider>
				</span>
			) : null}
		</span>
	);
}
