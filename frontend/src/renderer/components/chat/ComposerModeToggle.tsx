import { Bot, MessageSquare } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

export type ComposerMode = "agent" | "aside";

/**
 * The composer's two modes in a shared cloud session: Agent writes to the
 * agent, Aside writes to the people here and never reaches the agent. Shift+Tab
 * in the editor switches too; each mode keeps its own draft.
 */
export function ComposerModeToggle({ mode, onChange, disabled }: {
	mode: ComposerMode;
	onChange: (mode: ComposerMode) => void;
	disabled?: boolean;
}) {
	const { t } = useTranslation();
	const options = [
		{ value: "agent" as const, label: t("aside.mode.agent"), Icon: Bot },
		{ value: "aside" as const, label: t("aside.mode.aside"), Icon: MessageSquare },
	];
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<div
					aria-label={t("aside.mode.label")}
					className="inline-flex shrink-0 items-center gap-0.5"
					data-composer-mode={mode}
					role="group"
				>
					{options.map(({ value, label, Icon }) => (
						<Button
							aria-pressed={mode === value}
							disabled={disabled}
							key={value}
							onClick={() => { if (mode !== value) onChange(value); }}
							// The editor keeps focus, so typing carries on in the other mode's draft.
							onMouseDown={(event) => event.preventDefault()}
							size="xs"
							type="button"
							variant="quiet"
						>
							<Icon aria-hidden="true" className="size-3" />
							{label}
						</Button>
					))}
				</div>
			</TooltipTrigger>
			<TooltipContent side="top">{t("aside.mode.shortcut")}</TooltipContent>
		</Tooltip>
	);
}
