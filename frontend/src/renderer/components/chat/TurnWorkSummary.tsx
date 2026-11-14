import { useTranslation } from "react-i18next";
import { type ReactNode, useState } from "react";
import { ChevronRight } from "@/components/app-icons";
import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from "../ui/accordion";
import { TurnDuration } from "./ChatTimelineItems";

export function TurnWorkSummary({
	durationMs,
	children,
	open: controlledOpen,
	onOpenChange: controlledChange,
}: {
	durationMs?: number;
	children?: ReactNode;
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const [localOpen, setLocalOpen] = useState(false);
	const open = controlledOpen ?? localOpen;
	const onOpenChange = controlledChange ?? setLocalOpen;
	const label = (
		<>
			{t("chat.workedFor")}
			{durationMs !== undefined ? <>{" "}<TurnDuration durationMs={durationMs} /></> : null}
		</>
	);
	if (children === undefined || children === null) {
		return (
			<div className="flex h-7 select-none items-center gap-1 text-sm text-muted-foreground" data-turn-worked-label="">
				{label}
			</div>
		);
	}
	return (
		<Accordion
			type="single"
			collapsible
			className="-mx-1"
			value={open ? "worked" : ""}
			onValueChange={(value) => onOpenChange?.(value === "worked")}
		>
			<AccordionItem value="worked" className="border-0">
				<AccordionTrigger
					className="chat-worked-trigger h-7 select-none gap-1 px-1 py-0 text-sm font-normal text-muted-foreground hover:text-foreground"
					headerClassName="hover:bg-transparent data-[state=open]:bg-transparent"
					trailing={null}
				>
					<span className="inline-flex w-fit items-center gap-1">
						{label}
						<ChevronRight
							aria-hidden="true"
							className="size-3.5 shrink-0 transition-transform duration-200 group-data-[state=open]/row:rotate-90"
						/>
					</span>
				</AccordionTrigger>
				<AccordionContent className="chat-worked-accordion-content">
					<div className="space-y-2 px-1 pb-2 pt-1">{children}</div>
				</AccordionContent>
			</AccordionItem>
		</Accordion>
	);
}
