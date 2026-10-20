import { Plus, X } from "lucide-react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { CloudCpGitHubAppRepository } from "../lib/cloud-cp";
import {
	additionalReposProblem,
	canonicalGitHubUrl,
	gitHubFullName,
	MAX_PROJECT_ADDITIONAL_REPOS,
	repoDirName,
	sameRepository,
} from "../lib/cloud-project-repos";
import { SearchablePicker } from "./SearchablePicker";
import { Button } from "./ui/button";

/** The user-facing reason a set of additional repositories cannot be saved. */
export function additionalReposProblemMessage(t: TFunction, problem: ReturnType<typeof additionalReposProblem>): string | undefined {
	if (!problem) return undefined;
	const name = gitHubFullName(problem.url) ?? problem.url;
	switch (problem.kind) {
		case "main":
			return t("cloudRepos.problemMain", { defaultValue: "{{name}} is already the main repository.", name });
		case "duplicate":
			return t("cloudRepos.problemDuplicate", { defaultValue: "{{name}} is listed twice.", name });
		case "reserved":
			return t("cloudRepos.problemReserved", { defaultValue: "{{name}} would check out into the folder {{folder}}, which AO uses itself.", name, folder: problem.folder });
		case "hidden":
			return t("cloudRepos.problemHidden", { defaultValue: "{{name}} would check out into the hidden folder {{folder}}, which is not allowed.", name, folder: problem.folder });
		case "folder":
			return t("cloudRepos.problemFolder", { defaultValue: "{{name}} would check out into the same folder as another repository ({{folder}}).", name, folder: problem.folder });
		case "limit":
			return t("cloudRepos.problemLimit", { defaultValue: "A project can have at most {{count}} additional repositories.", count: MAX_PROJECT_ADDITIONAL_REPOS });
	}
}

/**
 * The project's additional repositories: every session clones each one beside
 * the main repository, in the folder named after it, on the session branch.
 * Rows hold canonical GitHub URLs; "" is a row still to be picked.
 */
export function CloudAdditionalReposEditor({
	available,
	mainUrl,
	value,
	onChange,
	onMakeMain,
	disabled = false,
	loading = false,
}: {
	available: readonly CloudCpGitHubAppRepository[];
	mainUrl?: string;
	value: readonly string[];
	onChange: (next: string[]) => void;
	/** Swaps a row with the main repository (create flow only). */
	onMakeMain?: (url: string) => void;
	disabled?: boolean;
	loading?: boolean;
}) {
	const { t } = useTranslation();
	const problem = additionalReposProblem(mainUrl, value);
	const message = additionalReposProblemMessage(t, problem);
	const optionsFor = (index: number) => {
		const current = value[index] ?? "";
		const taken = value.filter((url, i) => i !== index && url !== "");
		const options = available
			.map((repo) => ({ value: canonicalGitHubUrl(repo.fullName), label: repo.fullName, private: repo.isPrivate }))
			.filter((option) => !(mainUrl && sameRepository(option.value, mainUrl)) && !taken.some((url) => sameRepository(url, option.value)));
		// A stored repository the installation no longer lists stays selectable so
		// the row does not silently go blank.
		if (current !== "" && !options.some((option) => sameRepository(option.value, current))) {
			options.unshift({ value: current, label: gitHubFullName(current) ?? current, private: false });
		}
		return options;
	};
	const canAdd = !disabled && value.length < MAX_PROJECT_ADDITIONAL_REPOS && !value.includes("");
	return (
		<div className="space-y-2">
			{value.map((url, index) => (
				// eslint-disable-next-line react/no-array-index-key -- rows are positional; a URL can be blank while it is being picked.
				<div key={index} className="flex min-w-0 items-center gap-2">
					<SearchablePicker
						ariaLabel={t("cloudRepos.additionalRepository", { defaultValue: "Additional repository {{number}}", number: index + 1 })}
						fixedScroll
						className="min-w-0 flex-1"
						disabled={disabled || loading}
						placeholder={loading
							? t("createProject.githubReposLoading", { defaultValue: "Loading repositories..." })
							: t("cloudRepos.selectAdditional", { defaultValue: "Select a repository" })}
						searchPlaceholder={t("createProject.searchRepositories", { defaultValue: "Search repositories" })}
						value={url}
						options={optionsFor(index)}
						onChange={(next) => onChange(value.map((item, i) => (i === index ? next : item)))}
					/>
					{url !== "" ? (
						<span className="hidden shrink-0 font-mono text-[12px] text-muted-foreground sm:inline" title={t("cloudRepos.folderHint", { defaultValue: "Checked out beside the main repository" })}>
							{`../${repoDirName(url)}`}
						</span>
					) : null}
					{onMakeMain && url !== "" ? (
						<Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => onMakeMain(url)}>
							{t("cloudRepos.makeMain", { defaultValue: "Make main" })}
						</Button>
					) : null}
					<Button
						type="button"
						variant="ghost"
						size="icon-sm"
						disabled={disabled}
						aria-label={t("cloudRepos.remove", { defaultValue: "Remove {{name}}", name: gitHubFullName(url) ?? t("cloudRepos.additionalRepository", { defaultValue: "Additional repository {{number}}", number: index + 1 }) })}
						onClick={() => onChange(value.filter((_, i) => i !== index))}
					>
						<X aria-hidden="true" className="size-icon-sm" />
					</Button>
				</div>
			))}
			<Button type="button" variant="outline" size="sm" className="gap-1.5" disabled={!canAdd} onClick={() => onChange([...value, ""])}>
				<Plus aria-hidden="true" className="size-icon-sm" />
				{t("cloudRepos.add", { defaultValue: "Add a repository" })}
			</Button>
			{message ? (
				<p className="text-[12px] leading-5 text-destructive" role="alert">{message}</p>
			) : null}
		</div>
	);
}
