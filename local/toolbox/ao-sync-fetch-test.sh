#!/bin/bash
# ao-sync-fetch-test.sh — регресс-тест ao-sync.sh: резюме начатого синка обязано
# фетчить origin. Воспроизводит инцидент 2026-10-07: дозавершение синка пропустило
# `git fetch` (ветка «база уже зафиксирована тегом, fetch пропускаю»), merge прошёл
# против устаревшего origin/main — 18 апстрим-коммитов остались незамеченными.
#
# Фикстура: локальный bare-origin + клон-«форк» с маркером незавершённого синка
# (backup-тег + ao-sync-backup-ref). Апстрим уезжает вперёд ПОСЛЕ постановки тега,
# форк fetch не делает. Запуск скрипта-под копией в той же tmp-директории:
# REPO_ROOT ищется по .git+frontend+backend от cwd, поэтому копия подхватывает
# фикстуру, а не настоящий репо.
#
# Зелёный = все три ассерта:
#   1. в выводе есть «git fetch origin» (fetch выполнен на resume);
#   2. origin/main в форке догнал апстрим (merge пошёл против свежего ref);
#   3. backup-тег не сдвинулся со старого main (точка отката цела).
set -e

HERE="$(cd "$(dirname "$0")" && pwd)"
SRC="$HERE/ao-sync.sh"
WORK="$(mktemp -d /tmp/ao-sync-fetch-test.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }

git init --bare -q -b main "$WORK/origin.git"
git clone -q "$WORK/origin.git" "$WORK/repo"
cd "$WORK/repo"
git config user.email test@example.com
git config user.name test
mkdir frontend backend
echo A > f.txt
git add -A
git commit -qm A
git push -q origin main

# Апстрим уезжает на B уже после «начала синка» (второй клон как пушер).
git clone -q "$WORK/origin.git" "$WORK/pusher"
cd "$WORK/pusher"
echo B > f.txt
git add -A
git commit -qm B
git push -q origin main

# Форк: маркер незавершённого синка + backup-тег на A. Fetch НЕ делаем —
# локальный origin/main остаётся на A.
cd "$WORK/repo"
git tag backup/pre-sync-TEST main
printf 'backup/pre-sync-TEST\n' > "$(git rev-parse --git-dir)/ao-sync-backup-ref"
OLD_MAIN="$(git rev-parse main)"

# Копия скрипта рядом с фикстурой: SCRIPT_DIR/.. не содержит .git, поэтому
# REPO_ROOT разрешится в $PWD (фикстуру), а не в настоящий репо.
cp "$SRC" "$WORK/ao-sync.sh"
OUT="$(bash "$WORK/ao-sync.sh" 2>&1 || true)"

printf '%s' "$OUT" | grep -q 'git fetch origin' || fail "резюме не выполнило git fetch. Вывод:
$OUT"
[ "$(git rev-parse origin/main)" = "$(git -C "$WORK/pusher" rev-parse main)" ] || fail "origin/main в форке остался на устаревшем коммите. Вывод:
$OUT"
[ "$(git rev-parse backup/pre-sync-TEST)" = "$OLD_MAIN" ] || fail "backup-тег сдвинулся — точка отката потеряна."

echo "OK: resume fetch -> origin/main свежий, backup-тег на месте"
