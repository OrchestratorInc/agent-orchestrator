// Refuse to package under node versions that silently break electron-forge:
// brew node 26 exits 0 with an empty out/ (the truncation only surfaces later
// at the ditto step — rebuild 2026-10-25). Wired as the first command of the
// "prepackage" npm script, so every entry path into `npm run package`
// (rebuild-ao.sh, a manual run, anything future) fails loudly here instead.
// Allowlist the majors proven to package: 20/22 LTS and 24 (CI's setup-node).
const major = Number(process.versions.node.split(".")[0]);
if (major !== 20 && major !== 22 && major !== 24) {
	console.error(
		`!! node ${process.versions.node} is not supported for packaging — ` +
			"this node major silently breaks electron-forge package (exit 0, empty out/). " +
			"Run under nvm v20/v22/v24; rebuild-ao.sh pins this automatically. " +
			"If a new LTS is proven to package, add its major here.",
	);
	process.exit(1);
}
