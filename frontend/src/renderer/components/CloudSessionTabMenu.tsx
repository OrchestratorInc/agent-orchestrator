import { MoreVertical } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useTeamCollabSupport } from "../hooks/useTeamCollab";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { terminalTabActionClassName } from "./TerminalTabFrame";
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Pressable } from "./ui/pressable";

/**
 * A cloud session tab's menu, on a control plane with asides: Hide asides
 * leaves the timeline to the agent's work, remembered on this device. Nothing
 * shows where asides don't exist.
 */
export function CloudSessionTabMenu() {
	const { t } = useTranslation();
	const supported = useTeamCollabSupport() === "supported";
	const hideAsides = useUiStore((state) => state.hideAsides);
	const setHideAsides = useUiStore((state) => state.setHideAsides);
	if (!supported) return null;
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				{/* A native title: a Radix tooltip would reopen when the menu hands focus back. */}
				<Pressable
					aria-label={t("session.actionsMenu")}
					className={cn(terminalTabActionClassName, "data-[state=open]:bg-interactive-hover data-[state=open]:text-foreground")}
					data-session-actions-trigger
					title={t("session.actionsMenu")}
					type="button"
				>
					<MoreVertical aria-hidden="true" className="size-icon-sm" />
				</Pressable>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="min-w-48">
				<DropdownMenuCheckboxItem checked={hideAsides} onCheckedChange={(checked) => setHideAsides(checked === true)}>
					{t("session.hideAsides")}
				</DropdownMenuCheckboxItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
