import { joinWorkspaceRepositoryPath, splitWorkspaceRepositoryPath } from "../../lib/cloud-cp";
import type { ActiveFileAnnotationTarget, FileAnnotationModel } from "../WorkspaceDiffView";

export function repositoryAnnotationModel(model: FileAnnotationModel, repository: string | undefined): FileAnnotationModel {
	if (!repository) return model;
	const outward = (target: ActiveFileAnnotationTarget): ActiveFileAnnotationTarget => ({
		...target,
		path: joinWorkspaceRepositoryPath(repository, target.path),
		...(target.previousPath ? { previousPath: joinWorkspaceRepositoryPath(repository, target.previousPath) } : {}),
	});
	const inward = (target: ActiveFileAnnotationTarget): ActiveFileAnnotationTarget | undefined => {
		const split = splitWorkspaceRepositoryPath(target.path);
		if (split.repository !== repository) return undefined;
		const previous = target.previousPath ? splitWorkspaceRepositoryPath(target.previousPath) : undefined;
		return { ...target, path: split.path, ...(previous ? { previousPath: previous.path } : {}) };
	};
	return {
		...model,
		targets: model.targets.flatMap((target) => inward(target) ?? []),
		statusFor: (target) => model.statusFor(outward(target)),
		begin: (target) => model.begin(outward(target)),
		draftFor: (target) => model.draftFor(outward(target)),
		setDraft: (target, draft) => model.setDraft(outward(target), draft),
		cancel: (target) => model.cancel(target ? outward(target) : undefined),
		submit: (target, text) => model.submit(target ? outward(target) : undefined, text),
	};
}
