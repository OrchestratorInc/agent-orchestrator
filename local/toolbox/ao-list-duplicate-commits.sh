#!/bin/bash
# ao-list-duplicate-commits.sh — найти дублирующиеся коммиты внутри дельты форка
# (origin/main..HEAD) и, опционально, сгенерировать todo-файл для git rebase -i,
# который человек сам просматривает и запускает.
#
# Зачем: дельта форка может задваивать одну и ту же логическую фичу (тот же
# subject коммита встречается >1 раза) — обычно из-за повторного cherry-pick/merge
# уже присутствующей feature-ветки (см. память ao-lost-fixes-2026-08-13). Это не
# ловится `git cherry origin/main fork/main` (он сравнивает только против
# origin/main, не коммиты дельты друг с другом) и раздувает площадь конфликтов на
# каждом будущем rebase без всякой пользы.
#
# Что делает:
#   (без флагов / --dry-run)  печатает таблицу дублирующихся subject-строк с
#                              patch-id сравнением каждой пары (EQUAL/DIFFERENT);
#   --fix                     дополнительно генерирует todo-файл в формате
#                              git rebase -i (pick/drop с комментариями) в
#                              $(git rev-parse --git-dir)/ao-sync-squash-todo.txt.
#
# НИЧЕГО не коммитит, НЕ запускает rebase сам — только показывает и предлагает
# готовый todo, который нужно просмотреть и запустить вручную. patch-id EQUAL
# значит патчи байт-в-байт совпадают (безопасно drop); DIFFERENT значит тот же
# subject, но разное содержимое — типичный случай reset/rebase переигрывания той
# же логической фичи в разном контексте; такие пары требуют ручной сверки
# `git diff <sha1> <sha2>` перед тем как решать fixup/drop/оставить как есть.
#
# Запуск: ao-list-duplicate-commits.sh [--fix]

set -euo pipefail

FIX=0
for arg in "$@"; do
  case "$arg" in
    --fix) FIX=1 ;;
    --dry-run) : ;;  # поведение по умолчанию и так dry — принимаем флаг молча
  esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"
REPO_ROOT=""
for cand in "$SCRIPT_DIR/.." "$SCRIPT_DIR" "$PWD" "$PWD/.."; do
  abs="$(cd "$cand" 2>/dev/null && pwd)" || continue
  # -e, а не -d: в git worktree .git — файл со ссылкой на общий gitdir.
  if [ -e "$abs/.git" ] && [ -d "$abs/frontend" ] && [ -d "$abs/backend" ]; then
    REPO_ROOT="$abs"; break
  fi
done
if [ -z "$REPO_ROOT" ]; then
  echo "!! Не нашёл корень репо (нужен каталог с .git + frontend/ + backend/)." >&2
  exit 1
fi
cd "$REPO_ROOT"

if ! git rev-parse --verify -q origin/main >/dev/null; then
  echo "!! Нет ref origin/main — нечего сравнивать." >&2
  exit 1
fi

DUP_SUBJECTS="$(git log origin/main..HEAD --format='%s' | sort | uniq -d)"
if [ -z "$DUP_SUBJECTS" ]; then
  echo "==> дублирующихся subject-строк в дельте нет."
  exit 0
fi

echo "=========================================="
echo "ДУБЛИРУЮЩИЕСЯ КОММИТЫ В ДЕЛЬТЕ (origin/main..HEAD)"
echo "=========================================="

TODO_FILE="$(git rev-parse --git-dir)/ao-sync-squash-todo.txt"
[ $FIX -eq 1 ] && : > "$TODO_FILE"

# Полный список дельты в хронологическом порядке (старые сверху) — так должен
# выглядеть todo git rebase -i.
ALL_DELTA="$(git log origin/main..HEAD --format='%H %s' --reverse)"

printf '%s\n' "$DUP_SUBJECTS" | while IFS= read -r subj; do
  [ -z "$subj" ] && continue
  shas="$(printf '%s\n' "$ALL_DELTA" | grep -F " $subj" | awk '{print $1}')"
  ids="$(for sha in $shas; do git show "$sha" | git patch-id --stable | awk '{print $1}'; done)"
  uniq_ids="$(printf '%s\n' "$ids" | sort -u | wc -l | tr -d ' ')"
  if [ "$uniq_ids" = "1" ]; then
    eq="patch-id EQUAL — безопасно drop второй копии"
  else
    eq="patch-id DIFFERENT — НЕ дропать вслепую, сверь: git diff $(printf '%s\n' "$shas" | head -1) $(printf '%s\n' "$shas" | tail -1)"
  fi
  echo ""
  echo "\"$subj\""
  echo "  $shas" | tr '\n' ' '
  echo ""
  echo "  -> $eq"
done

echo ""
echo "=========================================="

if [ $FIX -eq 0 ]; then
  echo "Для генерации todo-файла git rebase -i: ao-list-duplicate-commits.sh --fix"
  exit 0
fi

# --fix: собираем todo-файл в хронологическом порядке. Первое вхождение каждого
# дублирующегося subject — pick; последующие с EQUAL patch-id — drop; последующие
# с DIFFERENT patch-id — тоже pick (решение оставляем человеку), с предупреждением.
#
# Без ассоциативных массивов: /bin/bash на macOS — 3.2, `declare -A` не работает
# (та же причина, по которой другие скрипты этого набора избегают `set -u`).
# "Уже видели этот subject" храним в scratch-файле — по одной строке "sha subj"
# на первое вхождение, ищем через grep -F.
SEEN_FILE="$(mktemp)"
trap 'rm -f "$SEEN_FILE"' EXIT

printf '%s\n' "$ALL_DELTA" | while IFS=' ' read -r sha rest; do
  subj="$rest"
  if ! printf '%s\n' "$DUP_SUBJECTS" | grep -Fxq "$subj"; then
    echo "pick $sha $subj" >> "$TODO_FILE"
    continue
  fi
  pid="$(git show "$sha" | git patch-id --stable | awk '{print $1}')"
  prev_line="$(grep -F $'\t'"$subj"$'\t' "$SEEN_FILE" 2>/dev/null | head -1 || true)"
  if [ -z "$prev_line" ]; then
    printf '%s\t%s\t%s\n' "$sha" "$subj" "$pid" >> "$SEEN_FILE"
    echo "pick $sha $subj" >> "$TODO_FILE"
  else
    first_sha="$(printf '%s' "$prev_line" | cut -f1)"
    first_pid="$(printf '%s' "$prev_line" | cut -f3)"
    if [ "$pid" = "$first_pid" ]; then
      echo "drop $sha $subj  # patch-id identical to $first_sha — safe to drop" >> "$TODO_FILE"
    else
      echo "pick $sha $subj  # patch-id DIFFERENT from $first_sha — не дропать вслепую: git diff $first_sha $sha" >> "$TODO_FILE"
    fi
  fi
done

echo "==> todo-файл сгенерирован: $TODO_FILE"
echo ""
echo "Просмотри и при необходимости отредактируй (особенно строки 'DIFFERENT'),"
echo "затем запусти сам (todo уже готов, редактор не откроется):"
echo ""
echo "  GIT_SEQUENCE_EDITOR=\"cp $TODO_FILE\" git rebase -i origin/main"
echo ""
echo "Ничего не было закоммичено или изменено этим запуском."
