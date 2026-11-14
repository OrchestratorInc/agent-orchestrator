import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";

/** The agent's mark at the size used beside a model name: the picker, its chip and the handoff line. */
export function ProviderMark({ provider, className }: { provider: string; className?: string }) {
	return <AgentAvatar className={cn("size-icon-base shrink-0", className)} decorative provider={provider} />;
}
