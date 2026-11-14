import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";

export function ProviderMark({ provider, className }: { provider: string; className?: string }) {
	return <AgentAvatar className={cn("size-icon-base shrink-0", className)} decorative provider={provider} />;
}
