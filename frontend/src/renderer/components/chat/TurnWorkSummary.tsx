import { useTranslation } from "react-i18next";
import { type ReactNode, useState } from "react";
import { ChevronRight } from "lucide-react";
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
	return children ? (
		<Accordion
			type="single"
			collapsible
			className="-mx-1 border-b border-border"
			value={open ? "worked" : ""}
			onValueChange={(value) => onOpenChange?.(value === "worked")}
		>
			<AccordionItem value="worked" className="border-0">
				<AccordionTrigger
					className="chat-worked-trigger h-7 select-none gap-1 px-1 py-0 text-sm font-normal text-muted-foreground transition-colors hover:text-foreground active:transform-none"
					headerClassName="hover:bg-transparent data-[state=open]:bg-transparent"
					trailing={null}
				>
					<span className="inline-flex w-fit items-center gap-1">
						{t("chat.workedFor")}
						{durationMs !== undefined ? <TurnDuration durationMs={durationMs} inline /> : null}
						<ChevronRight
							aria-hidden="true"
							className="size-3.5 shrink-0 transition-transform duration-200 group-data-[state=open]/row:rotate-90"
						/>
					</span>
				</AccordionTrigger>
				{/* Padding lives on the inner div: the content element's height is what animates,
					    so any padding on it itself would stay put while it opens and closes. */}
				<AccordionContent className="chat-worked-accordion-content">
					<div className="space-y-2 px-1 pb-2 pt-1">{children}</div>
				</AccordionContent>
			</AccordionItem>
		</Accordion>
	) : (
		<div className="-mx-1 flex h-7 select-none items-center border-b border-border px-1 py-0 text-sm font-normal text-muted-foreground">
			<span className="inline-flex w-fit items-center gap-1">
				{t("chat.workedFor")}
				{durationMs !== undefined ? <TurnDuration durationMs={durationMs} inline /> : null}
			</span>
		</div>
	);
}
