import { CheckCircle2, TriangleAlert, XCircle } from "@/components/app-icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import type { MessageKey } from "../i18n";

export type SystemRequirement = components["schemas"]["SystemRequirement"];

const STAGGER_STEP_MS = 150;

export function requirementDisplayLabel(requirement: SystemRequirement, t: TFunction): string {
	return requirement.id === "harness" ? t("startup.codingAgentLabel") : requirement.label;
}

const MISSING_DETAIL_KEYS: Record<string, MessageKey> = {
	git: "startup.detailGitMissing",
	tmux: "startup.detailTmuxMissing",
	harness: "startup.detailHarnessMissing",
	gh: "startup.detailGhMissing",
};

export function requirementDetailText(requirement: SystemRequirement, t: TFunction): string | undefined {
	if (requirement.satisfied) return requirement.detail;
	const key = MISSING_DETAIL_KEYS[requirement.id];
	return key ? t(key) : requirement.detail;
}

export function SystemRequirementsChecklist({
	requirements,
	ready,
}: {
	requirements: SystemRequirement[];
	ready: boolean;
}) {
	const { t } = useTranslation();
	return (
		<div
			aria-live="polite"
			className="ao-startup-checklist mt-4 flex w-full max-w-content-max flex-col gap-2 text-left"
			role="status"
		>
			{requirements.map((requirement, index) => (
				<div
					key={requirement.id}
					className="ao-startup-checklist__row flex items-start gap-2"
					style={{ animationDelay: `${index * STAGGER_STEP_MS}ms` }}
				>
					<RequirementGlyph requirement={requirement} />
					<div className="min-w-0">
						<p className="text-control font-medium text-foreground">{requirementDisplayLabel(requirement, t)}</p>
						{requirementDetailText(requirement, t) ? (
							<p className="text-caption leading-snug text-muted-foreground">{requirementDetailText(requirement, t)}</p>
						) : null}
					</div>
				</div>
			))}
			{ready ? (
				<p className="ao-startup-checklist__row mt-0.5 text-caption font-medium text-success">
					{t("startup.allChecksPassed")}
				</p>
			) : null}
		</div>
	);
}

function RequirementGlyph({ requirement }: { requirement: SystemRequirement }) {
	if (requirement.satisfied) {
		return <CheckCircle2 className="mt-0.5 size-icon-base shrink-0 text-success" aria-hidden="true" />;
	}
	if (requirement.required) {
		return <XCircle className="mt-0.5 size-icon-base shrink-0 text-destructive" aria-hidden="true" />;
	}
	return <TriangleAlert className="mt-0.5 size-icon-base shrink-0 text-warning" aria-hidden="true" />;
}
