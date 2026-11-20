## Keeping the docs up to date

The live docs ship with the app, so they must move with each release. This page is the single checklist maintainers follow.

### When a release ships

- Update the version line on the landing page (`content/index.mdx`, `index.ja.mdx`, `index.zh-CN.mdx`).
- Update the npm quick-start version in `content/installation.mdx` and its translations.
- Check the [changelog](/changelog) entries since the last docs pass for user-visible changes.

### When a feature lands

- New capability: add or update the matching page under `content/`.
- Changed behavior: edit the existing page, do not add a new one.
- Screenshots: retake any screenshot the change makes stale.

### Automation that helps

- `scripts/check-docs-version.mjs --mode review` fails CI when the docs name an older release than the newest tag. The scheduled `docs-gap-reminder` workflow comments on the draft docs PR when the docs lag.
- The weekly `docs-freshness` workflow opens an issue listing merged PRs that did not touch the docs.
- The `docs-links` workflow checks links on every docs PR.
- `npm run check:i18n` keeps the Japanese and Chinese pages in step with the English ones.
