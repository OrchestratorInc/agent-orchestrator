import { File, X } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { dropSharedArtifactDraft } from "../lib/shared-artifact-draft";
import { useSharedArtifactDraftStore } from "../stores/shared-artifact-draft-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";

/**
 * The shared artifact this draft's standalone session will start with: copied
 * into its artifact folder when it is sent. Shown only where it goes with the
 * send, a standalone session on this computer; removing it sends none, and
 * takes its prompt out of the draft.
 */
export function SharedArtifactDraftChip({ projectId, hostId }: { projectId: string | undefined; hostId: string | undefined }) {
	const { t } = useTranslation();
	const pending = useSharedArtifactDraftStore((state) => state.pending);
	if (!pending || hostId || projectId !== STANDALONE_WORKSPACE_ID) return null;
	const label = t("artifacts.shared.attached", { name: pending.name });
	return (
		<Badge className="h-6 max-w-64 gap-1 pe-0.5" title={pending.copy.entry} variant="outline">
			<File aria-hidden="true" />
			<span className="min-w-0 truncate">{label}</span>
			<Button aria-label={t("artifacts.shared.removeCopy")} className="size-5" onClick={dropSharedArtifactDraft} size="icon-sm" type="button" variant="quiet">
				<X aria-hidden="true" className="size-3" />
			</Button>
		</Badge>
	);
}
