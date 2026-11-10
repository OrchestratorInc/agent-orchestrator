import { ChevronLeft, ChevronRight } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { ConversationBranchPoint } from "../../types/conversation";
import { Button } from "../ui/button";

export function ConversationBranchNavigator({
	point,
	pending,
	error,
	onActivate,
}: {
	point: ConversationBranchPoint;
	pending: boolean;
	error?: string;
	onActivate: (branchId: string) => Promise<unknown> | void;
}) {
	const { t } = useTranslation();
	if (point.total <= 1) return null;
	return (
		<div className="flex min-w-0 items-center gap-0.5 text-caption text-muted-foreground">
		{point.previousBranchId ? (
			<span className="inline-flex">
				<Button
					type="button"
					variant="quiet"
					size="icon-sm"
					disabled={pending}
					onClick={() => {
						void Promise.resolve(onActivate(point.previousBranchId as string)).catch(() => {});
					}}
					aria-label={t("chat.branch.previous")}
				>
					<ChevronLeft aria-hidden="true" className="size-3.5" />
				</Button>
			</span>
		) : null}
		<span
			className="px-0.5 tabular-nums"
			aria-label={t("chat.branch.position", { position: point.position, total: point.total })}
		>
			{point.position} / {point.total}
		</span>
		{point.nextBranchId ? (
			<span className="inline-flex">
				<Button
					type="button"
					variant="quiet"
					size="icon-sm"
					disabled={pending}
					onClick={() => {
						void Promise.resolve(onActivate(point.nextBranchId as string)).catch(() => {});
					}}
					aria-label={t("chat.branch.next")}
				>
					<ChevronRight aria-hidden="true" className="size-3.5" />
				</Button>
			</span>
		) : null}
		{error ? <span role="alert" className="ml-1 truncate text-destructive">{error}</span> : null}
	</div>
	);
}
