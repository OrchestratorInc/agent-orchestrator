#!/bin/bash
# ao-fix-migration-collision.sh — привести миграции дельты форка к политике
# нумерации и синхронно пересобрать shippedMigrations ledger.
#
# Зачем: у форка axisrow есть свои миграции (user-config, ...). Апстрим
# занимает следующие номера, и на каждом sync номер нашей миграции оказывается
# дублем. Три «починки руками» подряд (см. память ao-fork-upstream-plus-delta)
# показали, что точечные текстовые заплатки ledger не работают: rerere тащит
# протухшие прешады, вложенные маркеры уезжают в коммит.
#
# Политика нумерации (2026-09-02, зафиксирована):
#   fork/main:  fork-local миграции (перечислены в forkLocalMigrations) живут
#               на КАНОНИЧЕСКИХ номерах 9000+ (add_user_config = 9001 и т.д.).
#               Byte-identical к апстриму → дельта-копия УДАЛЯЕТСЯ, форк берёт
#               апстримовский файл (прецеденты: 0101_provider_ownership_epochs,
#               0120_normalize_activity_last_at из смерженного #3900).
#   PR-ветки:   сквозная нумерация «макс+1» (апстрим не должен видеть 9000+
#               в PR); fork-тулинг (migrate_fork_reserved_range_test.go) в PR
#               не попадает, поэтому fork-логика там сама отключается.
#
# Ключевой приём: ledger НЕ латается построчно — он ПЕРЕСОБИРАЕТСЯ целиком
# (тело из origin/main + детерминированный fork-блок перед закрывающей `}`).
# Это исключает hunk-merge, perl-вставки и rerere как класс.
#
# Режимы:
#   --settle    идемпотентный проход ПОСЛЕ успешного rebase: политика
#               принудительно (канонические номера, byte-identical-удаление,
#               пересборка ledger) независимо от того, были ли коллизии.
#   (без флага) то же самое; историческое имя сохранил ao-sync.sh и руки.
#   --dry-run   только показать план.
#   --no-test   пропустить go test (при незавершённом rebase пакет не
#               компилируется; проверку сделает ao-sync.sh после rebase).
#
# Ничего не коммитит. Дрейф остаётся в рабочем дереве — ao-sync.sh скажет
# «закоммить вместе с ребейзом».

# pipefail обязателен: свободный номер вычисляется пайплайном, и молчаливый
# сбой промежуточного звена дал бы max=0 → номер 0001 → новая коллизия.
set -euo pipefail

DRY_RUN=0
NO_TEST=0
SETTLE=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --no-test) NO_TEST=1 ;;
    --settle) SETTLE=1 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"
REPO_ROOT=""
for cand in "$SCRIPT_DIR/.." "$SCRIPT_DIR" "$PWD" "$PWD/.."; do
  abs="$(cd "$cand" 2>/dev/null && pwd)" || continue
  # В git worktree .git — это ФАЙЛ со ссылкой на общий gitdir, а не каталог,
  # поэтому проверяем -e, а не -d (иначе скрипт не работает в worktree).
  if [ -e "$abs/.git" ] && [ -d "$abs/frontend" ] && [ -d "$abs/backend" ]; then
    REPO_ROOT="$abs"; break
  fi
done
if [ -z "$REPO_ROOT" ]; then
  echo "!! Не нашёл корень репо (нужен каталог с .git + frontend/ + backend/)." >&2
  exit 1
fi
cd "$REPO_ROOT"

MIG_DIR="backend/internal/storage/sqlite/migrations"
LEDGER="backend/internal/storage/sqlite/migrate_burned_versions_test.go"
SQLITE_DIR="backend/internal/storage/sqlite"
RESERVED_TEST="backend/internal/storage/sqlite/migrate_fork_reserved_range_test.go"

if [ ! -d "$MIG_DIR" ]; then
  echo "!! Каталог миграций не найден: $MIG_DIR" >&2
  exit 1
fi
if ! git rev-parse --verify -q origin/main >/dev/null; then
  echo "!! Нет ref origin/main — нечего сравнивать." >&2
  exit 1
fi

GIT_DIR="$(git rev-parse --git-dir)"
IN_REBASE=0
if [ -d "$GIT_DIR/rebase-merge" ] || [ -d "$GIT_DIR/rebase-apply" ]; then
  IN_REBASE=1
fi

# --- 0. Fork-local суффиксы: один источник правды — forkLocalMigrations. -----
# На PR-ветках этого файла нет → список пуст → вся fork-логика отключается,
# остаётся историческая сквозная нумерация для PR.
read_fork_suffixes() {
  [ -f "$RESERVED_TEST" ] || return 0
  sed -n '/var forkLocalMigrations = \[\]string{/,/^}/p' "$RESERVED_TEST" \
    | grep -oE '"[a-zA-Z_]+"' | tr -d '"' || true
}

# Канонические номера. Сюда добавлять при появлении НОВОЙ fork-миграции;
# суффикс из forkLocalMigrations без записи здесь получит следующий свободный
# номер 9000+ автоматически (с warning'ом).
CANONICAL_LIST="add_user_config:9001
session_provider_stamp:9006
gateway_entries:9011"

is_fork_suffix() {
  printf '%s\n' "$FORK_SUFFIXES" | grep -qx "$1"
}

canonical_for() {
  printf '%s\n' "$CANONICAL_LIST" | grep -E "^$1:" | head -1 | cut -d: -f2
}

FORK_SUFFIXES="$(read_fork_suffixes)"
if [ -n "$FORK_SUFFIXES" ]; then
  echo "==> fork-local миграции: $(printf '%s ' $FORK_SUFFIXES)"
fi

# --- 0b. В конфликте rebase: разрешить unmerged-файлы миграций. ---------------
# rename/rename (наш файл уже перенумерован на предыдущем коммите, а применяемый
# коммит переименовывает его со старым номером), add/delete и delete/delete
# оставляют файл в unmerged-состоянии, на котором git mv падает с
# "fatal: conflicted". Правило разрешения:
#   - если в HEAD есть файл с тем же суффиксом — это наш файл, уже
#     перенумерованный; берём HEAD-версию (--ours), а устаревшую удаляем
#     (если это не тот же самый файл);
#   - если в HEAD файла с таким суффиксом нет — это новый файл дельты,
#     берём версию из применяемого коммита (--theirs).
if [ "$IN_REBASE" -eq 1 ]; then
  for f in $(git diff --name-only --diff-filter=U -- "$MIG_DIR"); do
    base="$(basename "$f")"
    suffix="${base#*_}"
    head_match="$(git ls-tree HEAD --name-only "$MIG_DIR/" | sed 's|.*/||' | grep -F "${suffix}" || true)"
    if [ -n "$head_match" ]; then
      if git ls-files --unmerged "$MIG_DIR/$head_match" | grep -q .; then
        git checkout --ours -- "$MIG_DIR/$head_match"
      fi
      if [ "$head_match" != "$base" ]; then
        git rm -f -- "$MIG_DIR/$base"
      fi
    else
      git checkout --theirs -- "$MIG_DIR/$base"
    fi
  done
fi

# --- 1. Дельта-миграции: есть в дереве, нет в origin/main. -------------------
# Именно они и только они могут переноситься: файлы апстрима трогать нельзя.
OURS="$(comm -23 <(ls "$MIG_DIR" | sed 's|.*/||' | grep '\.sql$' | sort) \
                <(git ls-tree origin/main --name-only "$MIG_DIR/" | sed 's|.*/||' | sort))"
if [ -z "$OURS" ]; then
  echo "==> миграций дельты нет."
  exit 0
fi

# --- 2. Классификация каждой дельта-миграции. --------------------------------
#   identical  — byte-identical blob'у origin/main: апстрим смержил нашу
#                миграцию под своим номером, дельта-копия подлежит удалению;
#   canonical  — fork-local суффикс: целевой номер канонический (9000+),
#                переносим если текущий номер не канонический, КОЛЛИЗИЯ ПРИ
#                ЭТОМ НЕ ОБЯЗАТЕЛЬНА (политика, а не реакция на конфликт);
#   sequential — суффикс вне forkLocalMigrations (PR-ветки): историческое
#                «макс+1», но только при реальной коллизии номера.
UPSTREAM_BLOBS="$(git ls-tree origin/main -- "$MIG_DIR/" | awk '{print $3}')"
MAX_NUM="$(ls "$MIG_DIR"/*.sql | sed 's|.*/||' | sed 's|_.*||' \
          | sed 's|^0*||' | grep -E '^[0-9]+$' | sort -n | tail -1)"
[ -z "$MAX_NUM" ] && MAX_NUM=0

MOVES=""        # "old_file new_file" по строке
REMOVES=""      # byte-identical дубли
LIST_STRIPS=""  # суффиксы, которые надо убрать из forkLocalMigrations

for f in $OURS; do
  # Имя без номера И без .sql — ровно в том виде, в каком суффиксы записаны
  # в forkLocalMigrations ("9001_add_user_config.sql" → "add_user_config").
  num="${f%%_*}"
  suffix="${f#*_}"; suffix="${suffix%.sql}"

  our_blob="$(git hash-object "$MIG_DIR/$f" 2>/dev/null || true)"
  if [ -n "$our_blob" ] && printf '%s\n' "$UPSTREAM_BLOBS" | grep -qx "$our_blob"; then
    # В дереве может остаться и другой файл с тем же суффиксом — тогда суффикс
    # в списке живёт дальше.
    others_with_suffix="$(ls "$MIG_DIR" | grep -F "${suffix}" | grep -v "^${f}$" || true)"
    REMOVES="$REMOVES $f"
    if [ -z "$others_with_suffix" ] && [ -n "$FORK_SUFFIXES" ] && printf '%s\n' "$FORK_SUFFIXES" | grep -qx "$suffix"; then
      LIST_STRIPS="$LIST_STRIPS $suffix"
    fi
    echo "~~ $f байт-в-байт совпадает с файлом в origin/main — дельта-копия будет удалена."
    continue
  fi

  others="$(ls "$MIG_DIR" | grep "^${num}_" | grep -v "^${f}$" || true)"

  if [ -n "$FORK_SUFFIXES" ] && printf '%s\n' "$FORK_SUFFIXES" | grep -qx "$suffix"; then
    target="$(canonical_for "$suffix" || true)"
    if [ -z "$target" ]; then
      # Авто-канонический номер: максимум по занятым 9000+ + 1.
      reserved_max="$(ls "$MIG_DIR"/*.sql | sed 's|.*/||' | sed 's|_.*||' \
        | sed 's|^0*||' | grep -E '^[0-9]+$' | awk '$1 >= 9000' | sort -n | tail -1)"
      [ -z "$reserved_max" ] && reserved_max=9000
      target=$((reserved_max + 1))
      echo "!! суффикс '$suffix' в forkLocalMigrations, но без канона в CANONICAL_LIST — присваиваю $target, добавь в CANONICAL_LIST." >&2
    fi
    cur=$((10#$num))
    if [ "$cur" -ne "$target" ]; then
      new_file="$(printf '%04d' "$target")_${suffix}.sql"
      MOVES="$MOVES
$f $new_file"
      echo "!! fork-local $f → $new_file (канонический резерв; коллизия при этом не обязательна — это политика)"
    fi
  elif [ -n "$others" ]; then
    MAX_NUM=$((MAX_NUM + 1))
    new_file="$(printf '%04d' "$MAX_NUM")_${suffix}.sql"
    MOVES="$MOVES
$f $new_file"
    echo "!! коллизия: наш $f и апстримовский $(echo $others | tr '\n' ' ') → $new_file"
  fi
done

if [ -z "$MOVES$(printf '%s' "$REMOVES")$(printf '%s' "$LIST_STRIPS")" ]; then
  if [ $SETTLE -eq 1 ]; then
    # Дрейфа в номерах/дублях нет, НО settle всё равно пересобирает ledger
    # (шаг 4): мусор из старых дельта-коммитов при канонических номерах
    # лечится именно здесь. Содержимо идемпотентно — чистое дерево не меняет.
    echo "==> settle: номера и дубли в порядке; ledger будет сверён с upstream+fork."
  else
    echo "==> коллизий номеров миграций нет."
    exit 0
  fi
fi

echo ""
echo "=========================================="
echo "ПРИВЕДЕНИЕ МИГРАЦИЙ ДЕЛЬТЫ К ПОЛИТИКЕ"
[ $DRY_RUN -eq 1 ] && echo "(--dry-run: только показываю, ничего не меняю)" || true
echo "=========================================="

# --- 3. Применяем удаления и переносы. ---------------------------------------
for f in $REMOVES; do
  if [ $DRY_RUN -eq 1 ]; then
    echo "    [dry-run] git rm $f"
    continue
  fi
  # Файл может быть и затреканным (после rebase), и свежесозданным
  # незатреканным (mid-rebase дерево) — покрываем оба случая.
  git rm -f --ignore-unmatch -- "$MIG_DIR/$f" >/dev/null 2>&1 || true
  rm -f -- "$MIG_DIR/$f"
  echo "--- удалена дельта-копия $f (апстрим уже шипит эту миграцию)"
done

OLDIFS="$IFS"
IFS="
"
for move in $MOVES; do
  [ -z "$move" ] && continue
  old_file="${move%% *}"
  new_file="${move#* }"
  if [ $DRY_RUN -eq 1 ]; then
    echo "--- [dry-run] $old_file  →  $new_file"
    continue
  fi
  if [ ! -f "$MIG_DIR/$old_file" ]; then
    echo "--- $old_file уже отсутствует (удалён/перенесён ранее) — пропускаю"
    continue
  fi
  git mv "$MIG_DIR/$old_file" "$MIG_DIR/$new_file"
  echo "--- $old_file  →  $new_file"
done
IFS="$OLDIFS"

# Суффиксы, чьи файлы больше не существуют (все байт-идентичные копии удалены),
# убираем из forkLocalMigrations.
for suffix in $LIST_STRIPS; do
  remaining="$(ls "$MIG_DIR" | grep -F "${suffix}" || true)"
  if [ -n "$remaining" ]; then
    continue
  fi
  if [ $DRY_RUN -eq 1 ]; then
    echo "--- [dry-run] убрать \"$suffix\" из forkLocalMigrations ($RESERVED_TEST)"
    continue
  fi
  if [ -f "$RESERVED_TEST" ] && grep -q "\"$suffix\"" "$RESERVED_TEST"; then
    perl -0777 -pi -e "s/\\n\\t\\\"\Q$suffix\E\\\",//" "$RESERVED_TEST"
    echo "--- убрал \"$suffix\" из forkLocalMigrations ($RESERVED_TEST)"
  fi
done

# --- 4. Пересборка ledger: тело из origin/main + детерминированный fork-блок.
# Никаких построчных заплаток: ledger всегда = upstream-версия + добавки форка.
# Это делает бессмысленными текстовые конфликты ledger (и rerere-мусор в нём).
rebuild_needed=0
if [ $SETTLE -eq 1 ]; then
  rebuild_needed=1
fi
if [ $DRY_RUN -eq 0 ] && grep -q '^<<<<<<<' "$LEDGER" 2>/dev/null; then
  rebuild_needed=1
fi
if [ $DRY_RUN -eq 0 ] && [ -n "$MOVES$REMOVES" ]; then
  rebuild_needed=1
fi

if [ $rebuild_needed -eq 1 ] && [ $DRY_RUN -eq 0 ]; then
  echo ""
  echo "==> пересборка ledger: тело мапы из origin/main + fork-блок, остальное из HEAD..."
  # База — HEAD-версия файла: она содержит fork-тесты НИЖЕ мапы
  # (TestSessionKillSucceedsOnBurnedVersion96 и т.п.), которые стирать нельзя.
  # Заменяется ТОЛЬКО тело литерала shippedMigrations.
  tmp_head="$(mktemp)"
  git show "HEAD:$LEDGER" > "$tmp_head" 2>/dev/null || git show "origin/main:$LEDGER" > "$tmp_head"

  # Тело мапы апстрима. Записи, чьих файлов нет в дереве, отбрасываем: между
  # синками origin/main может быть АHEAD дерева (ад-хок запуск settle вне
  # синка). После нормального rebase фильтр ничего не отрезает.
  tmp_body="$(mktemp)"
  git show "origin/main:$LEDGER" | awk -v migdir="$MIG_DIR" '
    /^var shippedMigrations = map\[int64\]string\{/ { inmap = 1; next }
    inmap && /^\}$/ { inmap = 0; next }
    inmap {
      if ($0 ~ /^[[:space:]]*[0-9]+:[[:space:]]*"[0-9]+_[a-zA-Z_]+\.sql",?$/) {
        fname = $0
        sub(/^[^"]*"/, "", fname); sub(/".*$/, "", fname)
        err = (getline chk < (migdir "/" fname))
        if (err < 0) next
        if (err == 0) close(migdir "/" fname)
      }
      print
    }' > "$tmp_body"

  # Fork-записи из ФИНАЛЬНОГО состояния дерева (файлом: awk -v не переваривает
  # переносы строк в -v).
  tmp_fork="$(mktemp)"
  for f in $(ls "$MIG_DIR" | grep '\.sql$' | sort); do
    suffix="${f#*_}"; suffix="${suffix%.sql}"
    if [ -n "$FORK_SUFFIXES" ] && printf '%s\n' "$FORK_SUFFIXES" | grep -qx "$suffix"; then
      n=$((10#${f%%_*}))
      printf '\t%s: "%s",\n' "$n" "$f" >> "$tmp_fork"
    fi
  done

  tmp_new="$(mktemp)"
  # Вставить новое тело вместо старого: печатаем строки до открытия мапы,
  # подменяем тело, дальше файл идёт как был (тесты ниже мапы сохраняются).
  awk -v bodyf="$tmp_body" -v forkf="$tmp_fork" '
    { print }
    /^var shippedMigrations = map\[int64\]string\{/ {
      while ((getline line < bodyf) > 0) print line
      close(bodyf)
      if ((getline line0 < forkf) > 0) {
        print "\t// Fork-local migrations live in the reserved 9000+ range so a sync rebase"
        print "\t// can never renumber them onto a number upstream will claim. See"
        print "\t// migrate_fork_reserved_range_test.go for why."
        print line0
        while ((getline line < forkf) > 0) print line
      }
      close(forkf)
      while ((getline line) > 0) {
        if (line ~ /^\}$/) { print line; break }
      }
    }
  ' "$tmp_head" > "$tmp_new"
  cp "$tmp_new" "$LEDGER"
  fork_count="$(grep -c . "$tmp_fork" || true)"
  rm -f "$tmp_head" "$tmp_body" "$tmp_fork" "$tmp_new"
  ( cd backend && gofmt -w "${LEDGER#backend/}" )
  echo "   OK (fork-записей: ${fork_count:-0})"
fi

[ $DRY_RUN -eq 1 ] && exit 0 || true

# При незавершённом rebase ledger надо застейджить (ao-sync.sh делает git add -A
# после фикса, но скрипт должен быть корректен и сам по себе).
if [ $rebuild_needed -eq 1 ] && [ "$IN_REBASE" -eq 1 ]; then
  git add -- "$LEDGER"
fi

# --- 5. Проверка: структурные тесты миграций должны позеленеть. --------------
if [ $NO_TEST -eq 0 ]; then
  echo ""
  echo "==> проверка (TestMigrationVersionsAreUnique + TestMigrationVersionLedger)..."
  if go -C backend test ./internal/storage/sqlite/ \
       -run 'TestMigrationVersionsAreUnique|TestMigrationVersionLedger' >/tmp/ao-fix-mig.log 2>&1; then
    echo "   OK"
    rm -f /tmp/ao-fix-mig.log
  else
    echo "!! структурные тесты всё ещё красные:" >&2
    cat /tmp/ao-fix-mig.log >&2
    exit 1
  fi
fi

echo ""
echo "=========================================="
echo "Изменённые файлы:"
git status --short -- "$MIG_DIR" "$SQLITE_DIR" | sed 's/^/  /'
echo ""
echo "ПРОВЕРЬ ГЛАЗАМИ и прогони поведенческие тесты миграций:"
echo "  go -C backend test ./internal/storage/sqlite/ -run TestMigration"
echo "=========================================="
