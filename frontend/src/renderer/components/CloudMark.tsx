import { CloudFilled } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";

/**
 * Marks a Cloud project, session or notification with a quiet filled cloud
 * instead of a pill: no border, no fill, no hover state and no tooltip, only
 * a "Cloud" accessible label.
 */
export function CloudMark({ className }: { className?: string }) {
	const { t } = useTranslation();
	return (
		<span
			aria-label={t("shell.cloudProjectBadge")}
			className={cn("inline-grid size-icon-md shrink-0 place-items-center text-muted-foreground", className)}
			data-cloud-mark=""
			role="img"
		>
			<CloudFilled aria-hidden="true" className="size-icon-md" />
		</span>
	);
}
