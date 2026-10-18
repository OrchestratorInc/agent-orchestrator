#!/bin/bash
# ao-sync.sh — синхронизировать fork/main с upstream origin/main: fetch → backup-тег →
# merge origin/main в дельту форка → быстрые проверки-ловушки → быстрые гейты.
#
# Модель синка — MERGE, не rebase (переход 2026-09-04): дельта форка живёт как
# один squash-коммит (+ новые форковые коммиты) поверх origin/main, каждый синк
# добавляет merge-коммит. История fork/main не переписывается: конфликты
# разрешаются один раз в merge-коммите и не воспроизводятся в следующих синках,
# force-push не нужен. (Сжатие дельты обратно в 1 коммит — отдельный ручной
# squash-пасс «время от времени», не часть синка.)
#
# Цель: ловить типичные отказы (коллизия номера миграции, забытый `npm install`
# после новой зависимости, рассинхрон regen-артефактов) ЗА СЕКУНДЫ, до того как
# они всплывут через 5 минут `npm run package` или после запуска демона.
#
# НЕ делает: push в fork (обычный fast-forward — решение пользователя),
# пересборку .app (это ~/bin/rebuild-ao.sh). Конфликты: миграционные (дубль
# номера) — авто-фиксер; остальные — стоп и ручное решение по каждому
# (для «апстрим удалил фичу» — подсказка git rm по политике «апстрим всегда
# прав»; rerere на время merge выключен, авто-повтор старых резолюций
# исключён).
#
# Запускать из корня репо agent-orchestrator (или откуда угодно — REPO_ROOT
# ищется по .git+frontend+backend, см. rebuild-ao.sh).

set -e
# NB: без `set -u` — та же причина, что в rebuild-ao.sh (macOS /bin/bash 3.2).

SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"
REPO_ROOT=""
for cand in "$SCRIPT_DIR/.." "$SCRIPT_DIR" "$PWD" "$PWD/.."; do
  abs="$(cd "$cand" 2>/dev/null && pwd)" || continue
  # -e, а не -d: в git worktree `.git` — файл со ссылкой на общий gitdir.
  if [ -e "$abs/.git" ] && [ -d "$abs/frontend" ] && [ -d "$abs/backend" ]; then
    REPO_ROOT="$abs"; break
  fi
done
if [ -z "$REPO_ROOT" ]; then
  echo "!! Не нашёл корень репо (нужен каталог с .git + frontend/ + backend/)." >&2
  exit 1
fi
cd "$REPO_ROOT"

echo "==> репо: $REPO_ROOT (ветка: $(git rev-parse --abbrev-ref HEAD))"

# 1. Guard: рабочее дерево обязано быть чистым — иначе merge может всё смешать.
if [ -n "$(git status --porcelain)" ]; then
  echo "!! Рабочее дерево не чистое. Закоммить/застэшь изменения и запусти снова." >&2
  git status --short >&2
  exit 1
fi

# 2. fetch + backup-тег на текущий main (страховка перед merge).
#
# ВАЖНО: тег ставится ОДИН РАЗ на синк. Скрипт задуман перезапускаемым (после
# ручного разрешения конфликта merge), а иначе он на каждом запуске ставил бы
# тег на УЖЕ СМЁРЖЕННЫЙ HEAD, затирая единственную точку отката. Реальное
# состояние держим в маркер-файле, который живёт ровно на время незавершённого
# синка.
# git rev-parse --git-dir, а не "$REPO_ROOT/.git": в worktree последний —
# файл, и запись в него затёрла бы ссылку на общий gitdir.
# Фетч ВСЕГДА, включая резюме: резюме может начаться спустя сутки после
# прерывания, и merge всё равно пойдёт против origin/main (инцидент
# 2026-10-07: пропущенный на резюме fetch спрятал 18 апстрим-коммитов).
# Тег-защита ниже — только про тег: точка отката не двигается на резюме.
echo "==> git fetch origin..."
git fetch origin
BACKUP_MARKER="$(git rev-parse --git-dir)/ao-sync-backup-ref"
BACKUP_TAG="$(cat "$BACKUP_MARKER" 2>/dev/null || true)"
if [ -n "$BACKUP_TAG" ] && git rev-parse -q --verify "refs/tags/$BACKUP_TAG" >/dev/null; then
  echo "==> продолжаю начатый синк; backup-тег остаётся прежним: $BACKUP_TAG"
  echo "    ($(git rev-parse --short "$BACKUP_TAG") — состояние main ДО этого синка)"
  echo "==> резюме: тег не двигаю, merge идёт против свежего origin/main."
else
  [ -n "$BACKUP_TAG" ] && echo "!! маркер указывал на несуществующий тег $BACKUP_TAG — создаю новый." >&2 || true
  BACKUP_TAG="backup/pre-sync-$(date +%Y%m%d-%H%M)"
  # Без -f: если тег уже есть (два запуска в одну минуту), это тот же синк,
  # и перезаписывать его нельзя — иначе точка отката уедет на смёрженный HEAD.
  git tag "$BACKUP_TAG" main 2>/dev/null || true
  printf '%s\n' "$BACKUP_TAG" > "$BACKUP_MARKER"
  echo "==> backup-тег: $BACKUP_TAG (на случай отката: git reset --hard $BACKUP_TAG)"
fi

if git rev-parse --verify -q origin/main >/dev/null && [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ]; then
  echo "==> уже на уровне origin/main, синкать нечего."
  rm -f "$BACKUP_MARKER"
  exit 0
fi

# 3. merge origin/main в дельту форка.
#    Миграционные конфликты (дубль номера дельты: наш коммит и upstream меняли
#    одну строку shippedMigrations в migrate_burned_versions_test.go)
#    авто-разрешаем фиксером. Остальные — стоп: список файлов с типом каждого,
#    решение по каждому вручную.
FIXER="$HOME/bin/ao-fix-migration-collision.sh"
export GIT_EDITOR=true   # чтобы merge-коммит не открывал редактор

# Признак «миграционный конфликт»: среди конфликтующих файлов есть migrations/
# или migrate_.
migration_conflict() {
  git diff --name-only --diff-filter=U | grep -Eq 'backend/internal/storage/sqlite/(migrations/|migrate_)'
}

# Список НЕ-миграционных конфликтующих файлов (пустой = остались только
# миграционные). grep -Ev даёт 1 при пустом выводе — под set -e нужен || true.
nonmigration_conflicts() {
  git diff --name-only --diff-filter=U | grep -Ev 'backend/internal/storage/sqlite/(migrations/|migrate_)' || true
}

# Класс не-миграционного конфликта — подсказка исполнителю (человек/агент),
# ЧТО делать с файлом после решения владельца. Само решение скрипт не
# принимает и merge не двигает: отчёт уходит владельцу, дальше — его «ок»
# (сводить по классам) или «откат» (git merge --abort).
# union-i18n — обе стороны дописали ключи локалей одним блоком (1 hunk):
# сводится объединением обеих сторон; >1 блока в i18n — уже не механика.
# regen — артефакт регенерации: брать любую сторону, шаг 5 синка перегенерит.
# ручное — только по содержимому, стороной по умолчанию не считается.
conflict_class() {
  local path="$1"
  case "$path" in
    frontend/src/renderer/i18n/*.json)
      if [ "$(grep -cE '^<{7} ' "$path" || true)" = "1" ]; then
        echo "union-i18n — механический union: дописать обе стороны (ключи локалей)"
      else
        echo "ручное — i18n, но блоков конфликта больше одного: сверка по ключам"
      fi
      ;;
    *openapi.yaml|*schema.ts|backend/*/gen/*)
      echo "regen — артефакт регенерации: брать любую сторону, шаг 5 синка перегенерит"
      ;;
    *)
      echo "ручное — решение владельца по содержимому"
      ;;
  esac
}

# Авто-сведение механических классов (политика 2026-10-16: механика сводится
# сразу автоматически, владелец разгребает только сложное).
# union-i18n — ровно 1 блок маркеров: union обеих сторон с запятой на шве
# (последняя строка «нашей» стороны получает запятую перед «их» первой),
# валидация JSON через python3 -m json.tool (plutil на этом macOS -lint
# отвергает валидный JSON); сбой валидации — конфликт восстанавливается
# (git checkout -m) и файл уезжает в ручной отчёт.
# regen — сторона апстрима (origin/main): шаг 5 синка перегенерит корректное.
# Печатает пути сведённых файлов.
auto_resolve_mechanical_conflicts() {
  local f
  while read -r f; do
    [ -n "$f" ] || continue
    case "$(conflict_class "$f")" in
      union-i18n*)
        awk '
          /^[[:space:]]*<{7}/ || /^[[:space:]]*>{7}/ { next }
          /^[[:space:]]*={7}[[:space:]]*$/ { seam = 1; next }
          {
            if (seam && started && prev ~ /"$/ && prev !~ /,/) sub(/$/, ",", prev)
            seam = 0
            if (started) print prev
            prev = $0
            started = 1
          }
          END { if (started) print prev }
        ' "$f" > "$f.auto" || { rm -f "$f.auto"; continue; }
        if command -v python3 >/dev/null 2>&1 && ! python3 -m json.tool "$f.auto" >/dev/null 2>&1; then
          rm -f "$f.auto"
          echo "!! union $f не прошёл JSON-валидацию — оставляю конфликт на владельца." >&2
          git checkout -m -q -- "$f" 2>/dev/null || true
          continue
        fi
        mv "$f.auto" "$f"
        git add "$f"
        echo "$f"
        ;;
      regen*)
        if git checkout --theirs -q -- "$f" 2>/dev/null; then
          git add "$f"
          echo "$f"
        fi
        ;;
    esac
  done < <(git diff --name-only --diff-filter=U)
}

# Печать конфликтующих файлов с индекс-кодами: UU меняли оба, UD удалил
# апстрим, DU удалён в форке, AA добавили оба (+DD/AU/UA — rename-крайности).
# Для UU/AA — «что произошло»: счёт строк с обеих сторон (три точки = один
# merge-base) и fork-коммиты файла (живая in-flight PR работа или шум).
# ponytail: пути с пробелами git кавычит в --porcelain, read их не
# разворачивает — при первом реальном случае перейти на --porcelain -z.
print_conflicts() {
  echo "   конфликтующие файлы:" >&2
  git status --porcelain | grep -E '^(DD|AU|UD|UA|DU|AA|UU) ' | while read -r code path; do
    echo "     $code $path" >&2
    case "$code" in
      UU|AA)
        fork="$(git diff --numstat origin/main...HEAD -- "$path" | awk '{printf "+%s -%s", $1, $2}')"
        up="$(git diff --numstat HEAD...origin/main -- "$path" | awk '{printf "+%s -%s", $1, $2}')"
        echo "        дельта форка: ${fork:-(нет)} | апстрим: ${up:-(нет)}" >&2
        echo "        класс: $(conflict_class "$path")" >&2
        log="$(git log --oneline --no-merges origin/main..HEAD -- "$path" | head -3 | paste -sd ';' -)"
        [ -n "$log" ] && echo "        fork-коммиты: $log" >&2
        ;;
    esac
  done
}

echo "==> git merge origin/main..."
# NB: rerere отключён ТОЛЬКО на время merge (-c): его авто-повтор старых
# резолюций подменял содержимое молча (инцидент «rerere ревертит
# апстрим-тексты»). Каждая не-миграционная резолюция проходит через руки.
if git merge-base --is-ancestor HEAD origin/main; then
  # Чистый fast-forward: дельты нет, HEAD строго позади origin/main —
  # без merge-коммита, гейты ниже (3b+) идемпотентны.
  echo "==> дельты нет — чистый fast-forward на origin/main."
  git merge --ff-only origin/main
elif git -c rerere.enabled=false merge --no-edit -m "chore(sync): merge origin/main $(date +%Y-%m-%d)" origin/main; then
  :
else
  if migration_conflict; then
    echo "==> миграционный конфликт — автоперенумерация..."
    if ! "$FIXER" --no-test; then
      echo "!! автопочинка не справилась — дальше руками (см. сообщение фиксера)." >&2
      print_conflicts
      exit 1
    fi
    # add -A нельзя, пока есть не-миграционные конфликты: он застаеджит их
    # маркеры как «решённые» и merge закоммитит мусор (инциденты 2026-09-02).
    REST="$(nonmigration_conflicts)"
    if [ -n "$REST" ]; then
      cat >&2 <<'EOF'

!! Вместе с миграционным остались НЕ-миграционные конфликты — реши их по
   каждому (для UD «апстрим удалил фичу» см. политику ниже), затем:
     git add <файлы> && git merge --continue
   и запусти этот скрипт снова — он продолжит с шага проверки миграций.
EOF
      print_conflicts
      exit 1
    fi
    git add -A
    if ! git merge --continue; then
      cat >&2 <<'EOF'

!! git merge --continue не прошёл после автоперенумерации — разреши руками,
   затем:
     git add <файлы> && git merge --continue
   и запусти этот скрипт снова — он продолжит с шага проверки миграций.
   Откат: git merge --abort
EOF
      print_conflicts
      exit 1
    fi
    echo "==> merge завершён после автоперенумерации миграций."
  else
    echo "==> авто-сведение механических конфликтов (union-i18n, regen)..."
    RESOLVED="$(auto_resolve_mechanical_conflicts || true)"
    if [ -n "$RESOLVED" ]; then
      echo "$RESOLVED" | sed 's/^/     сведено автоматически: /' >&2
    fi
    if [ -n "$(nonmigration_conflicts)" ]; then
      cat >&2 <<'EOF'

!! Merge остановился на конфликте, требующем решения владельца. По каждому
   файлу ниже — ручное решение, затем:
     git add <файлы> && git merge --continue
   и запусти этот скрипт снова — он продолжит с шага проверки миграций.
   Откат всего синка: git merge --abort

   РЕШЕНИЕ ЗА ВЛАДЕЛЬЦЕМ: классификация в списке ниже — подсказка исполнителю,
   не разрешение. Без явного «ок» владельца ничего не сводить — откат.
   Исполнитель (агент) показывает владельцу список с классами и ждёт:
   «ок» (сводить по классам: union-i18n объединением, regen любой стороной,
   ручное по каждому отдельно) или «откат».

   Типы конфликтов (коды git status):
     UU — меняли оба: сводить содержимое вручную. Стороны по каждому файлу:
            git diff HEAD...origin/main -- <файл>   # что принёс апстрим
            git diff origin/main...HEAD -- <файл>   # что несёт дельта форка
          Реген-артефакты (openapi.yaml, schema.ts, backend/**/gen/) можно
          брать с любой стороны — шаг 5 синка перегенерит корректное.
     UD — апстрим удалил файл, дельта его меняла («апстрим удалил фичу»):
          по политике «апстрим всегда прав» — git rm <файл>, чтобы не тащить
          фичу в форке. ЕСЛИ только это не живая работа in-flight PR
          (сверь с ao-pr-parked.txt и открытыми PR, прежде чем сносить).
     DU — дельта удаляла файл, апстрим менял: обычно git add <файл>
          (вернуть апстрим-версию).
     AA — добавили оба: сверить содержимое (стороны — те же команды, что
          в UU).
EOF
    print_conflicts
    exit 1
    fi
    echo "==> все не-миграционные конфликты сведены автоматически — завершаю merge."
    if ! git merge --continue; then
      echo "!! git merge --continue не прошёл после авто-сведения — разреши руками." >&2
      print_conflicts
      exit 1
    fi
  fi
fi

# 3b. Settle: привести миграции дельты к политике нумерации (канонические 9000+
#     для fork-local, byte-identical дубли апстрима — удалить, ledger пересобрать
#     детерминированно из origin/main + fork-блока). Идемпотентно: на чистом
#     дереве ничего не меняет. Дрейф остаётся незакоммиченным — закоммить
#     отдельным chore-коммитом, как regen-артефакты ниже.
echo "==> settle миграций (канонические номера, пересборка ledger)..."
if ! "$FIXER" --settle --no-test; then
  echo "!! settle не справился — миграции дальше руками (см. сообщение фиксера)." >&2
  exit 1
fi
if git diff --name-only -- backend/internal/storage/sqlite/ | grep -q .; then
  echo "   !! settle изменил миграции/ledger — закоммить эти изменения отдельным chore-коммитом."
  git status --short -- backend/internal/storage/sqlite/
else
  echo "   OK, дрейфа нет"
fi

# 3c. Гейт на маркеры конфликта. Ловит класс «merge завершился УСПЕШНО, но
#     закоммитил мусор» (оба инцидента 2026-09-02: вложенные <<<<<<< в
#     migrate_activity_zone_test.go и в ledger уехали в main при зелёном синке).
#     Ограничение по расширениям исключает false-positive на setext-подчёркиваниях
#     (======= под заголовками) в markdown-доках. Дёшево: один git grep.
#     NB: git grep_exit 1 = «совпадений нет» — это ЗЕЛЁНЫЙ случай, поэтому
#     код возврата не гейтится, гейтится только непустой вывод.
check_conflict_markers() {
  git grep -nI -E '^(<{7} |={7}$|>{7} )' -- '*.go' '*.ts' '*.tsx' '*.json' '*.yaml' '*.yml' '*.sql' 2>/dev/null || true
}
echo "==> проверка маркеров конфликта в дереве..."
MARKERS="$(check_conflict_markers)"
if [ -n "$MARKERS" ]; then
  echo "!! В дереве остались маркеры конфликта (merge закоммитил мусор):" >&2
  printf '%s\n' "$MARKERS" >&2
  echo "   Почини вручную (выбрать сторону, убрать маркеры) и закоммить." >&2
  exit 1
fi
echo "   OK, маркеров нет"

# 3d. Проактивная проверка: не смержил ли апстрим уже одну из наших fork-local
#     миграций под своим номером (byte-identical содержимое под другим именем).
#     В норме МОЛЧИТ — этот кейс теперь автоматом разбирает settle (шаг 3b).
#     Если ругается — settle не справился (напр., suffix не в forkLocalMigrations),
#     удаление требует осмысленного коммита руками.
LEDGER_TEST="backend/internal/storage/sqlite/migrate_fork_reserved_range_test.go"
MIG_DIR_SYNC="backend/internal/storage/sqlite/migrations"
check_fork_migrations_not_merged_upstream() {
  [ -f "$LEDGER_TEST" ] || return 0
  local names
  names="$(sed -n '/var forkLocalMigrations = \[\]string{/,/^}/p' "$LEDGER_TEST" \
    | grep -oE '"[a-zA-Z_]+"' | tr -d '"')"
  [ -z "$names" ] && return 0
  local upstream_blobs
  upstream_blobs="$(git ls-tree origin/main -- "$MIG_DIR_SYNC/" | awk '{print $3}')"
  local name f blob
  for name in $names; do
    for f in "$MIG_DIR_SYNC"/*"$name"*.sql; do
      [ -f "$f" ] || continue
      blob="$(git hash-object "$f" 2>/dev/null || true)"
      if [ -n "$blob" ] && printf '%s\n' "$upstream_blobs" | grep -qx "$blob"; then
        cat >&2 <<EOF

!! forkLocalMigrations: "$name" ($f) байт-в-байт совпадает с файлом в origin/main.
   Похоже, апстрим смержил эту миграцию под своим номером. Вручную:
     1. git rm $f
     2. убрать "$name" из forkLocalMigrations в $LEDGER_TEST
     3. проверить/убрать соответствующую запись в shippedMigrations, если есть
     4. go -C backend test ./internal/storage/sqlite/ -run TestForkMigrationsUseReservedRange
EOF
      fi
    done
  done
}
echo "==> проверка forkLocalMigrations на совпадение с апстримом..."
check_fork_migrations_not_merged_upstream
echo "   OK"

# 3e. Детекция дублей коммитов ВНУТРИ дельты форка (subject встречается >1 раза).
#     Инцидент: та же логическая фича (per-role env profile, user-scope config)
#     оказалась переиграна в истории дважды — 11 subject-строк по 2 копии каждая
#     (обнаружено 2026-08-28). Порог: НЕ блокирует уже известный бардак, но
#     блокирует появление НОВОГО дубля: типичный симптом случайного повторного
#     cherry-pick/merge уже присутствующей feature-ветки.
#     NB: --no-merges обязателен — merge-коммиты синка имеют одинаковый
#     subject-паттерн и без фильтра каждый синк «находил бы» N-1 дублей.
DUPES_CACHE="$(git rev-parse --git-dir)/ao-sync-known-dupes-count"
KNOWN_DUPES="$(cat "$DUPES_CACHE" 2>/dev/null || echo 0)"

echo "==> проверка дублей коммитов в дельте..."
DUP_SUBJECTS="$(git log --no-merges origin/main..HEAD --format='%s' | sort | uniq -d)"
CURRENT_DUPES="$(printf '%s\n' "$DUP_SUBJECTS" | grep -c . || true)"

if [ "$CURRENT_DUPES" -gt 0 ]; then
  echo "   найдено $CURRENT_DUPES дублирующихся subject-строк (было известно: $KNOWN_DUPES):"
  printf '%s\n' "$DUP_SUBJECTS" | while IFS= read -r subj; do
    [ -z "$subj" ] && continue
    shas="$(git log --no-merges origin/main..HEAD --format='%H %s' | grep -F " $subj" | awk '{print $1}')"
    ids="$(for sha in $shas; do git show "$sha" | git patch-id --stable | awk '{print $1}'; done | sort -u | wc -l | tr -d ' ')"
    if [ "$ids" = "1" ]; then eq="patch-id EQUAL (безопасно squash/drop)"; else eq="patch-id DIFFERENT (нужна ручная сверка diff)"; fi
    echo "     - \"$subj\": $shas — $eq"
  done
  echo "   Разовая чистка: ~/bin/ao-list-duplicate-commits.sh --fix"
else
  echo "   OK, дублей subject-строк нет"
fi

if [ "$CURRENT_DUPES" -gt "$KNOWN_DUPES" ]; then
  cat >&2 <<EOF

!! Появился НОВЫЙ дубль коммита (было известно $KNOWN_DUPES, сейчас $CURRENT_DUPES).
   Похоже, feature-ветка задвоилась (повторный cherry-pick/merge уже
   присутствующей работы). Проверь список выше перед тем как продолжать.
EOF
  exit 1
fi
# DUPES_CACHE обновляется только в самом конце (шаг 8), вместе со снятием
# BACKUP_MARKER — т.е. только если весь sync успешно дошёл до конца.

# 4. Проверка коллизии миграций — САМЫЙ ДЕШЁВЫЙ и самый частый отказ этого форка
#    (дважды подряд: дубль номера, отсутствие записи в shippedMigrations ledger).
#    Гонять ТОЧЕЧНО, не весь пакет — доли секунды.
# NB: команда пишет в лог и проверяется по СОБСТВЕННОМУ коду возврата. Раньше
# было `if ! go test ... | tee file`, что проверяло код возврата tee — то есть
# коллизия миграции проходила через гейт молча.
MIG_LOG=/tmp/ao-sync-migrate-test.log
migration_tests_pass() {
  go -C backend test ./internal/storage/sqlite/ \
    -run 'TestMigrationVersionsAreUnique|TestMigrationVersionLedger' >"$MIG_LOG" 2>&1
}
# FIXER определён выше (шаг 3) — используется и здесь.

echo "==> проверка миграций (TestMigrationVersionsAreUnique + TestMigrationVersionLedger)..."
if ! migration_tests_pass; then
  cat "$MIG_LOG" >&2
  echo "" >&2
  echo "!! Миграции красные — пробую автопочинку ($(basename "$FIXER"))..." >&2

  [ -x "$FIXER" ] || { echo "!! $FIXER не найден — чини вручную." >&2; exit 1; }

  # Фиксер сам решает, коллизия ли это (наш номер занял upstream): переименует
  # наш файл на свободный номер и перепишет запись в shippedMigrations.
  # Поведенческие тесты он не трогает — они находят миграцию по имени через
  # migrationVersion(t, "<суффикс>").
  "$FIXER" || { cat >&2 <<'EOF'

!! Автопочинка не справилась — значит это не типовая коллизия номера.
   Ручной путь:
     1. ls backend/internal/storage/sqlite/migrations/*.sql | sort | tail -5
     2. git mv <наш_файл>_XXXX_*.sql <наш_файл>_<свободный>_*.sql
     3. переписать запись в shippedMigrations в
        backend/internal/storage/sqlite/migrate_burned_versions_test.go
   См. память ao-0041-migration-duplicate-fix.
EOF
    exit 1; }

  echo "==> автопочинка прошла; повторяю проверку миграций..."
  migration_tests_pass || { cat "$MIG_LOG" >&2; echo "!! после автопочинки всё ещё красные — дальше руками." >&2; exit 1; }
  echo "   OK (после автоперенумерации — ПРОВЕРЬ git diff перед коммитом)"
fi
rm -f "$MIG_LOG"
echo "   OK"

# 5. Регенерация артефактов — убирает ложные diff-конфликты между regen-файлами
#    (openapi.yaml, schema.ts, sqlc gen/*.go) и то, что реально изменилось в API.
echo "==> регенерация API-спеки (openapi.yaml + schema.ts)..."
npm run api >/tmp/ao-sync-regen.log 2>&1 || { echo "!! npm run api упал:" >&2; cat /tmp/ao-sync-regen.log >&2; exit 1; }
# Эти два файла помечены merge=ours в .git/info/attributes: при конфликте git
# молча оставляет НАШУ версию, а корректное содержимое восстанавливает regen
# выше. Поэтому непустой дифф здесь — нормальный и ОЖИДАЕМЫЙ исход после
# merge, а не тревога; важно его увидеть, иначе не отличить «regen ничего не
# поменял» от «regen починил то, что merge=ours оставил устаревшим».
if git diff --name-only -- backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts | grep -q .; then
  echo "   !! regen изменил openapi.yaml/schema.ts — закоммить эти изменения отдельным chore-коммитом."
  git diff --stat -- backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
else
  echo "   OK, без изменений"
fi

# Три точки (merge-base), а не $BACKUP_TAG..HEAD: последнее сравнивает старый
# main с новым и включает ЛЮБУЮ апстримную правку queries/ за период — то есть
# почти всегда истинно, даже когда дельта форка queries/ не касалась. Три
# точки спрашивают только «поменяла ли ЭТА дельта queries/ относительно её же
# базы», что и должно гейтить дорогой regen.
if git diff --name-only origin/main...HEAD -- backend/internal/storage/sqlite/queries/ | grep -q .; then
  echo "==> дельта форка меняет queries/ — регенерация sqlc..."
  if ! npm run sqlc >/tmp/ao-sync-sqlc.log 2>&1; then
    # proxy.golang.org бывает недоступен (TLS handshake timeout) — ретраим через
    # локальный кэш модулей как file://-прокси: точная версия sqlc уже скачана
    # прошлыми синками и лежит в ~/go/pkg/mod.
    echo "   go run не смог скачать sqlc из сети — ретраю через локальный кэш модулей..."
    if ! GOPROXY="file://$HOME/go/pkg/mod/cache/download" npm run sqlc >/tmp/ao-sync-sqlc.log 2>&1; then
      echo "!! npm run sqlc упал (в том числе через локальный кэш):" >&2
      cat /tmp/ao-sync-sqlc.log >&2
      exit 1
    fi
  fi
  if git diff --name-only -- backend/internal/storage/sqlite/gen/ | grep -q .; then
    # gen/ не всегда воспроизводим regen'ом: апстрим вписывал артефакты руками
    # (issue #4621), поэтому regen может дать несобираемый дифф. Пробуем билд;
    # красный — откатываем regen и оставляем gen/ как был, синк продолжается.
    echo "   !! sqlc regen изменил gen/ — проверяю, что билд жив..."
    if ( cd backend && go build ./... ) >/tmp/ao-sync-regen-build.log 2>&1; then
      echo "   !! regen зелёный по билду — закоммить эти изменения отдельным chore-коммитом."
      git diff --stat -- backend/internal/storage/sqlite/gen/
    else
      echo "   !! после regen билд красный — откатываю regen gen/ (артефакты апстрима не воспроизводимы, см. #4621):"
      head -5 /tmp/ao-sync-regen-build.log | sed 's/^/        /'
      git restore -- backend/internal/storage/sqlite/gen/
      echo "   gen/ оставлен как был, синк продолжается."
    fi
    rm -f /tmp/ao-sync-regen-build.log
  else
    echo "   OK, без изменений"
  fi
fi

# 6. npm install — единственная защита от «upstream добавил зависимость,
#    её нет в node_modules» (стреляло: @fontsource-variable/geist и
#    @tanstack/react-virtual, каждая съела 5 минут package до отказа).
#    Гоняем ВСЕГДА, не по условию: зависимость может прийти и из апстрима
#    (origin/main), и из дельты, а любое diff-условие ловит лишь то, что
#    рядом с этой конкретной синк-сессией, а не «что вообще изменилось в
#    package.json с последнего install». npm install при актуальном lockfile
#    дёшев (~5с), а пропущенная зависимость стоит 5 минут сборки — всегда
#    гонять.
echo "==> npm install (frontend)..."
( cd frontend && npm install ) || { echo "!! npm install упал." >&2; exit 1; }

# 6b. Гейт frontend typecheck — единственная защита от класса «merge взял
#     апстрим-версию файла и потерял часть fork-дельты». Инцидент 2026-10-10:
#     #6439 перестроил ProjectSettingsEditor.tsx, merge-резолюция съела
#     ProjectAgentRoleHeader из импорта, использование осталось — «X is not
#     defined» всплыло только в рантайме собранного .app, потому что vite не
#     типчекает. tsc --noEmit ловит это за секунды, до пуша и сборки.
TS_LOG=/tmp/ao-sync-typecheck.log
gate_frontend_typecheck() {
  ( cd "$REPO_ROOT/frontend" && npm run typecheck ) >"$TS_LOG" 2>&1
}
echo "==> tsc --noEmit (frontend)..."
if ! gate_frontend_typecheck; then
  cat "$TS_LOG" >&2
  rm -f "$TS_LOG"
  cat >&2 <<'EOF'

!! TypeScript красный после merge. Типичная причина: merge взял апстрим-версию
   файла и потерял часть fork-дельты (импорт, использование, проп). Сверь
   файл с состоянием до синка и верни потерянное:
     git diff <файл>          # что сломано сейчас
     git diff $BACKUP_TAG HEAD -- <файл>   # что дельта теряет/меняет
   После починки: git add <файлы> && git commit и запусти синк снова
   (merge уже закоммичен — продолжение начнёт с проверок).
EOF
  exit 1
fi
echo "   OK"


# 7. Быстрые гейты: whole-module gofmt (см. память ao-gofmt-whole-module-gate —
#    точечный gofmt на файлах не ловит рассинхрон после merge) + go build.
echo "==> gofmt -l . (весь backend-модуль)..."
FMT_OUT="$(cd backend && gofmt -l .)"
if [ -n "$FMT_OUT" ]; then
  echo "!! gofmt нашёл неотформатированные файлы:" >&2
  echo "$FMT_OUT" >&2
  echo "   Почини: (cd backend && gofmt -w \$FMT_OUT)" >&2
  exit 1
fi
echo "   OK"

echo "==> go build ./... (backend)..."
if ! ( cd backend && go build ./... ) 2>/tmp/ao-sync-build.log; then
  echo "!! go build упал:" >&2
  cat /tmp/ao-sync-build.log >&2
  exit 1
fi
rm -f /tmp/ao-sync-build.log
echo "   OK"

# 7b. Автосквош разросшейся дельты (решение 2026-09-23: пользователь попросил
#     автоматизировать «дельта 18 → 1»). Порог — AO_SQUASH_AFTER (по умолчанию 8).
#     Сквош-скрипт сам стоит на гейтах (чистое дерево, origin/main предок,
#     tree-id self-check с откатом), а force-with-lease здесь — потому что
#     переписанная история без него не запушится; пользователь дал стоячее
#     разрешение на этот конкретный пуш при запуске автоматизации. Отключить:
#     AO_SQUASH_AFTER=999999 ~/bin/ao-sync.sh
DELTA_N="$(git rev-list --count --no-merges origin/main..HEAD)"
SQUASH_AFTER="${AO_SQUASH_AFTER:-8}"
if [ "$DELTA_N" -ge "$SQUASH_AFTER" ]; then
  echo "==> дельта $DELTA_N коммитов >= $SQUASH_AFTER — авто-сквош..."
  "$REPO_ROOT/local/toolbox/ao-squash-delta.sh"
  git push --force-with-lease fork main
  echo "   OK, дельта сжата и запушена (force-with-lease)."
fi

# 8. Итог + подсказки на следующие (осознанно ручные) шаги.
#    Синк дошёл до конца — снимаем маркер, чтобы СЛЕДУЮЩИЙ запуск завёл свежий
#    backup-тег, а не продолжал считать этот синк незавершённым. Сам тег
#    остаётся в репо как точка отката.
rm -f "$BACKUP_MARKER"
# Кэш дублей коммитов (шаг 3e) обновляем только здесь — синк дошёл до конца
# успешно, значит текущее число дублей становится новым «известным» порогом.
printf '%s\n' "$CURRENT_DUPES" > "$DUPES_CACHE"
echo "=========================================="
echo "==> синк готов. main = $(git rev-parse --short HEAD): $(git rev-list --count --no-merges origin/main..HEAD) коммитов дельты + $(git rev-list --count --merges origin/main..HEAD) merge-коммитов поверх origin/main."
echo "    Точка отката (состояние ДО синка): git reset --hard $BACKUP_TAG"
echo "    Дальше вручную (merge историю не переписывает — push обычный fast-forward):"
echo "      git push fork main"
echo "      ~/bin/rebuild-ao.sh"
echo ""
echo "    Перед push ветки в upstream-PR — полные гейты (golangci-lint + typecheck:e2e,"
echo "    те самые, которых нет здесь и на которых уже краснел CI):"
echo "      ~/bin/ao-pregate.sh"

# 9. Отчёт по mergeable-статусу моих открытых PR в апстриме.
#    Только отчёт — никаких rebase/push здесь: конфликт может быть безобидным
#    (regen-артефакты) или содержательным (см. случай #3910 — PR оказался
#    дублем уже смерженного апстримного фикса), решение всегда ручное.
#    Не блокирует синк (может упасть без gh/сети) — просто пропускается.
if command -v gh >/dev/null 2>&1; then
  echo ""
  echo "==> проверка mergeable-статуса открытых PR (OrchestratorInc/agent-orchestrator, author=axisrow)..."
  PR_JSON="$(gh pr list --repo OrchestratorInc/agent-orchestrator --author axisrow --state open \
    --json number,title,url,mergeable 2>/dev/null || true)"
  if [ -z "$PR_JSON" ] || [ "$PR_JSON" = "[]" ]; then
    echo "   нет открытых PR или gh недоступен — пропускаю."
  else
    # Сводка: сколько открыто и что с каждым (mergeable по списку GitHub;
    # детальный разбор конфликтов ниже, с пересчётом через REST).
    PR_COUNT="$(printf '%s' "$PR_JSON" | jq 'length' 2>/dev/null || echo '?')"
    echo "   открытых PR: $PR_COUNT"
    # Парковка (решение 2026-09-29, апстрим-ишью #6037): номера из ao-pr-parked.txt
    # сняты с ребейз-дорожки — их конфликты и CI печатаются как info, не как «!!».
    PARKED_FILE="$REPO_ROOT/local/toolbox/ao-pr-parked.txt"
    PARKED=" $(awk 'NF && $1 !~ /^#/ {printf "%s ", $1}' "$PARKED_FILE" 2>/dev/null)"
    PARKED_N="$(printf '%s' "$PARKED" | wc -w | tr -d ' ')"
    echo "   парковка (не ребейзим): $PARKED_N шт. — ao-pr-parked.txt"
    printf '%s' "$PR_JSON" | jq -r --arg parked "$PARKED" 'sort_by(.number) | reverse | .[] | (.number|tostring) as $nid | "      #\(.number) [\(.mergeable)] \(.title)" + (if $parked | contains(" \($nid) ") then " ← парковка" else "" end)' 2>/dev/null
    # UNKNOWN означает «GitHub ещё не пересчитал» (обычно на старых/неактивных
    # PR) — не факт конфликта, форсируем пересчёт через REST для каждого номера.
    NUMBERS="$(printf '%s' "$PR_JSON" | jq -r '.[].number' 2>/dev/null || true)"
    if [ -z "$NUMBERS" ]; then
      echo "   не удалось разобрать список PR (нужен jq) — пропускаю."
    else
      CONFLICTING=""
      FAILED=""
      CI_NOTE=""
      for n in $NUMBERS; do
        case "$PARKED" in *" $n "*) continue;; esac
        title="$(printf '%s' "$PR_JSON" | jq -r ".[] | select(.number==$n) | .title")"
        state="$(gh api "repos/OrchestratorInc/agent-orchestrator/pulls/$n" --jq '.mergeable_state' 2>/dev/null || echo "")"
        if [ "$state" = "dirty" ]; then
          url="$(printf '%s' "$PR_JSON" | jq -r ".[] | select(.number==$n) | .url")"
          CONFLICTING="$CONFLICTING#$n\t$title\t$url\n"
        elif [ -z "$state" ] || [ "$state" = "unknown" ]; then
          # Пустой/unknown state — gh упал или GitHub не посчитал. Молча считать
          # это «конфликтов нет» нельзя (инцидент: 11 реальных конфликтов при
          # пустом списке) — отдельный список, чтобы ложный OK был невозможен.
          FAILED="$FAILED#$n\t$title\n"
        fi
        # CI-статус ПРа: report-only, ничего не ждём — непрогнавшие чеки
        # покажет следующий синк (решение 2026-09-24: не висеть на ожидании).
        CHK="$(gh pr checks "$n" --repo OrchestratorInc/agent-orchestrator 2>/dev/null || true)"
        if [ -n "$CHK" ]; then
          CP="$(printf '%s\n' "$CHK" | awk -F'\t' '$2=="pass"' | wc -l | tr -d ' ')"
          CF="$(printf '%s\n' "$CHK" | awk -F'\t' '$2=="fail"' | wc -l | tr -d ' ')"
          CQ="$(printf '%s\n' "$CHK" | awk -F'\t' '$2=="pending"' | wc -l | tr -d ' ')"
          if [ "$CF" -gt 0 ] || [ "$CQ" -gt 0 ]; then
            CI_NOTE="$CI_NOTE#$n pass=$CP fail=$CF pending=$CQ\n"
          fi
        fi
      done
      if [ -n "$CONFLICTING" ]; then
        echo "   !! конфликтуют с origin/main:"
        # %b, не %s: CONFLICTING хранит литеральные \t/\n, read сплитит по
        # настоящим табам — %s их не разворачивал и список печатался пустым.
        printf '%b' "$CONFLICTING" | while IFS=$'\t' read -r num title url; do
          [ -z "$num" ] && continue
          echo "      $num  $title"
          echo "          $url"
        done
        echo "   Разрешать вручную (rebase каждой ветки в отдельном worktree, force-with-lease push)."
      elif [ -z "$FAILED" ]; then
        echo "   OK, конфликтующих нет."
      fi
      if [ -n "$FAILED" ]; then
        echo "   !! не удалось проверить mergeable (ошибка gh или статус не посчитан):"
        printf '%b' "$FAILED" | while IFS=$'\t' read -r num title; do
          [ -z "$num" ] && continue
          echo "      $num  $title"
        done
        echo "   Сверь статусы напрямую: gh pr list --author axisrow --state open --json number,mergeable"
      fi
      if [ -n "$CI_NOTE" ]; then
        echo "   CI не зелёный (fail/pending):"
        printf '%b' "$CI_NOTE" | while IFS= read -r line; do
          [ -z "$line" ] && continue
          echo "      $line"
        done
        echo "   Агенту: pending не ждать — покажет следующий синк; fail — разобраться и починить."
      fi
    fi
  fi
fi

# 10. Напоминание агенту: ситуация по PR должна попасть в ответ пользователю.
#     Открытые печатает шаг 9; здесь — свежесмерженные, чтобы агент не ходил
#     за ними отдельным вызовом. Только отчёт, ничего не блокирует.
if command -v gh >/dev/null 2>&1; then
  echo ""
  echo "==> напоминание агенту: покажи пользователю ситуацию по PR —"
  echo "    открытые — список выше; смерженные за последнее время:"
  gh pr list --repo OrchestratorInc/agent-orchestrator --author axisrow --state merged --limit 8 \
    --json number,title,mergedAt 2>/dev/null \
    | jq -r 'sort_by(.mergedAt) | reverse | .[] | "      #\(.number)  \(.mergedAt[0:10])  \(.title)"' 2>/dev/null
fi
