#!/bin/bash
# TDD-тесты шагов 0–3 rebuild-ao.sh (инсталляция бандла).
# Инцидент 2026-10-11: после пересборки окно аппа показывало сырой бандл (JS
# как plain text). Причина: `ditto` писал байты нового asar ПОВЕРХ живого
# /Applications/…app — процесс со старым asar-индексом (GUI не успел умереть
# или стартовал во время копирования) читал спан двух соседних чанков и
# рисовал его как документ. Требования: инсталляция только через staging +
# mv (живой бандл никогда не переписывается по байтам), и замена не
# начинается, пока жив хоть один AO GUI-процесс.
set -u
REBUILD="${REBUILD:-/Users/axisrow/bin/rebuild-ao.sh}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fail=0

note_red() { echo "RED $1: $2"; fail=1; }
note_green() { echo "green $1: $2"; }

# T1: нет прямой записи в живой бандл; есть staging + mv-замена.
if grep -q 'ditto "$APP_SRC" "$APP_DST"' "$REBUILD"; then
  note_red T1 "ditto пишет прямо в живой \$APP_DST — bytes живого asar перезаписываются"
else
  note_green T1 "прямой ditto в живой бандл отсутствует"
fi
grep -q 'mv "$STAGE_APP" "$APP_DST"' "$REBUILD" \
  && note_green T1b "замена через mv из staging" \
  || note_red T1b "нет mv \$STAGE_APP → \$APP_DST (атомарной замены)"

# T2: перед заменой бандла скрипт ждёт завершения ВСЕХ AO GUI-процессов
# и падает, если процесс не умер (вместо того чтобы переписывать бандл под ним).
grep -q 'AO GUI не завершился' "$REBUILD" \
  && note_green T2 "есть отказ инсталляции при живом GUI" \
  || note_red T2 "инсталляция не защищена от живого GUI-процесса"

# T2b (поведенческий): гард-блок между маркерами guard: begin/end.
# Живой GUI (pgrep=0) → блок обязан выйти с ошибкой; мёртвый (pgrep=1) → пропустить.
GUARD="$TMP/guard.sh"
awk '/^# --- guard: begin/,/^# --- guard: end/' "$REBUILD" > "$GUARD"
if [ -s "$GUARD" ] && grep -q pgrep "$GUARD"; then
  mkdir -p "$TMP/bin2"
  printf '#!/bin/bash\nexit 0\n' > "$TMP/bin2/pgrep"   # «GUI жив»
  printf '#!/bin/bash\n' > "$TMP/bin2/sleep"            # без пауз
  printf '#!/bin/bash\nexit 0\n' > "$TMP/bin2/pkill"
  chmod +x "$TMP/bin2/"*
  if PATH="$TMP/bin2:$PATH" bash "$GUARD" >/dev/null 2>&1; then
    note_red T2b "гард пропустил инсталляцию при живом GUI (exit 0)"
  else
    note_green T2b "гард отказал при живом GUI"
  fi
  printf '#!/bin/bash\nexit 1\n' > "$TMP/bin2/pgrep"    # «GUI мёртв»
  if PATH="$TMP/bin2:$PATH" bash "$GUARD" >/dev/null 2>&1; then
    note_green T2c "гард пропускает при отсутствии GUI"
  else
    note_red T2c "гард ложноположительно отказал без GUI"
  fi
else
  note_red T2b "маркеры guard: begin/end не найдены"
fi

# T3 (поведенческий): блок инсталляции между маркерами гоняется в песочнице.
# success: dst = новая сборка; ditto-фейл: dst остаётся старой, live-бандл не тронут.
if ! awk '/^# --- install: begin/,/^# --- install: end/' "$REBUILD" > "$TMP/install.sh"; then
  note_red T3 "маркеры install: begin/end не найдены"
fi
if [ -s "$TMP/install.sh" ] && grep -q "ditto" "$TMP/install.sh"; then
  # Стабы: ditto→cp -R (или управляемый фейл), codesign→ok, security→ad-hoc ветка.
  mkdir -p "$TMP/bin"
  cat > "$TMP/bin/ditto" <<'STUB'
#!/bin/bash
[ -n "${FAKE_DITTO_FAIL:-}" ] && exit 1
mkdir -p "$2" && cp -R "$1/" "$2/"
STUB
  printf '#!/bin/bash\nexit 0\n' > "$TMP/bin/codesign"
  printf '#!/bin/bash\nexit 1\n' > "$TMP/bin/security"
  chmod +x "$TMP/bin/ditto" "$TMP/bin/codesign" "$TMP/bin/security"

  export PATH="$TMP/bin:$PATH"
  export CODESIGN_IDENTITY="-"

  # Фейковые сборки: старая (v1) в «dst», новая (v2) в «src».
  SRC="$TMP/out/Agent Orchestrator.app"
  DST="$TMP/Applications/Agent Orchestrator.app"
  STAGE_ROOT="$TMP/staging"
  STAGE_APP="$STAGE_ROOT/Agent Orchestrator.app"
  mkdir -p "$SRC/Contents/MacOS" "$SRC/Contents/Resources" "$DST/Contents/MacOS" "$DST/Contents/Resources"
  # Сетап-маркеры обязаны лечь: иначе тест гоняется на пустых деревьях.
  printf 'v1' > "$DST/Contents/MacOS/agent-orchestrator" \
    && printf 'v1' > "$DST/Contents/Resources/marker" \
    && printf 'v2' > "$SRC/Contents/MacOS/agent-orchestrator" \
    && printf 'v2' > "$SRC/Contents/Resources/marker" \
    || { echo "RED setup: не удалось создать фейковые сборки"; exit 1; }
  export APP_SRC="$SRC" APP_DST="$DST" STAGE_ROOT STAGE_APP

  # T3a: фейл ditto → dst нетронут (живой бандл не трогаем при любой ошибке).
  if FAKE_DITTO_FAIL=1 bash "$TMP/install.sh" >/dev/null 2>&1; then
    note_red T3a "блок инсталляции вернул 0 при фейле копирования"
  fi
  if [ "$(cat "$DST/Contents/Resources/marker")" = "v1" ]; then
    note_green T3a "при фейле копирования живой бандл остался v1"
  else
    note_red T3a "живой бандл испорчен при фейле копирования: $(cat "$DST/Contents/Resources/marker" 2>/dev/null || echo '<нет>')"
  fi

  # T3b: успех → dst стал v2, MacOS-бинарь на месте, staging подметён.
  if bash "$TMP/install.sh" >/dev/null 2>&1; then
    if [ "$(cat "$DST/Contents/Resources/marker" 2>/dev/null)" = "v2" ] \
       && [ "$(cat "$DST/Contents/MacOS/agent-orchestrator" 2>/dev/null)" = "v2" ]; then
      note_green T3b "после инсталляции dst = новая сборка (v2)"
    else
      note_red T3b "после инсталляции dst не v2"
    fi
    if [ ! -d "$STAGE_ROOT" ]; then
      note_green T3c "staging-каталог отсутствует после инсталляции"
    else
      note_red T3c "staging-каталог остался: $STAGE_ROOT"
    fi
  else
    note_red T3b "блок инсталляции упал на корректной сборке"
  fi
else
  note_red T3 "блок инсталляции не извлечён — маркеров нет (фикс не применён?)"
fi

[ "$fail" = 0 ] && echo "ALL GREEN" || exit 1
exit 0
