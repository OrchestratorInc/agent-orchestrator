#!/usr/bin/env bash
# Полный squash дельты форка в 1 коммит поверх origin/main.
#
# Дерево сохраняется бит-в-бит (reset --soft + проверка tree-id), переписывается
# только история. Лечит гейт дублей subject-строк в ao-sync.sh (инцидент
# 2026-09-22: regen-коммиты с фиксированным subject копятся, гейт блокирует
# каждый 2-й синк). Профилактика: regen-коммиты называть с датой в subject.
#
# Пуш НЕ делает: после переписывания истории нужен force-with-lease в fork —
# по правилам форка только с явного разрешения пользователя.
set -euo pipefail

# $0 может быть симлинком из ~/bin (dirname даст $HOME) — разворачиваем.
SCRIPT="$(readlink -f "$0" 2>/dev/null || echo "$0")"
REPO="$(cd "$(dirname "$SCRIPT")/../.." && pwd)"
cd "$REPO"

BRANCH="$(git branch --show-current)"
if [ "$BRANCH" != "main" ]; then
  echo "!! ветка $BRANCH, а не main — squash только из main" >&2
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "!! дерево грязное — сначала закоммить или убери изменения" >&2
  exit 1
fi

if [ -e .git/MERGE_HEAD ] || [ -d .git/rebase-merge ] || [ -d .git/rebase-apply ]; then
  echo "!! идёт merge/rebase — заверши или прерви" >&2
  exit 1
fi

# КРИТИЧНО: origin/main должен уже быть влит в текущее дерево, иначе
# reset --soft молча выкинет чужие коммиты из дерева.
if ! git merge-base --is-ancestor origin/main HEAD; then
  echo "!! origin/main НЕ предок HEAD — сначала долей его (~/bin/ao-sync.sh)" >&2
  exit 1
fi

DELTA_N="$(git rev-list --count origin/main..HEAD)"
if [ "$DELTA_N" -lt 2 ]; then
  echo "!! дельта уже $DELTA_N коммит(ов) — сжимать нечего" >&2
  exit 1
fi

OLD="$(git rev-parse --short HEAD)"
OLD_TREE="$(git rev-parse HEAD^{tree})"
BACKUP_TAG="backup/pre-squash-$(date +%Y%m%d-%H%M)"
git tag "$BACKUP_TAG"

git reset --soft origin/main
git commit -q -m "chore(fork): fork delta ahead of origin/main (squash $(date +%F))" \
  -m "Co-Authored-By: Claude Code <noreply@anthropic.com>"

# Самопроверка: дерево нового коммита обязано совпасть со старым.
NEW_TREE="$(git rev-parse HEAD^{tree})"
if [ "$OLD_TREE" != "$NEW_TREE" ]; then
  git reset --hard "$BACKUP_TAG"
  echo "!! tree-id разошёлся — откатился на $BACKUP_TAG, разберись руками" >&2
  exit 1
fi

echo "==> дельта ужата: $OLD (${DELTA_N} коммитов) -> $(git rev-parse --short HEAD) (1 коммит), дерево идентично"
echo "    откат: git reset --hard $BACKUP_TAG"
echo "    пуш (переписывает fork/main, нужно твоё явное разрешение):"
echo "      git push --force-with-lease fork main"
