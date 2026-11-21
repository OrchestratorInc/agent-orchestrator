import { useState } from "react";
import { useTranslation } from "react-i18next";

const labels = [
	"session.generatingBranchName",
	"session.namingBranch",
	"session.preparingBranch",
	"session.initializingBranch",
	"session.resolvingBranchName",
] as const;

export function BranchNamingLabel() {
	const { t } = useTranslation();
	const [label] = useState(() => labels[Math.floor(Math.random() * labels.length)]);
	return <span className="chat-working-shimmer min-w-0 truncate" role="status">{t(label)}</span>;
}
