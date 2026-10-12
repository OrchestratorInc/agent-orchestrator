# Live Grid — первоначальная проверка локальной лабораторной сборки

Это исторический отчёт первого этапа. Актуальная проверка обычной сборки и удалённых проектов: [live-grid-remote-validation.md](live-grid-remote-validation.md).

Ветка: `feature/live-grid`. База: `c087cb02ee016620d5e9dcfe7dd9487c0b1b12c1` из официального OrchestratorInc/agent-orchestrator. Исполняемый код приложения собран из `dad07934`; последующие изменения относятся к проверкам и документации.

Черновой PR: [OrchestratorInc/agent-orchestrator#6549](https://github.com/OrchestratorInc/agent-orchestrator/pull/6549).

## Что реализовано

- Open Grid рядом с Open Kanban; отдельный маршрут проекта внутри существующего Electron-приложения.
- Главный оркестратор закреплён в первой ячейке. Работники приходят из общего workspace query и CDC. Отсутствующий оркестратор получает панель восстановления без автоматического создания сессии.
- Настоящие существующие TUI через TerminalPane/xterm/общий mux и Chat через SessionChatSurface/ChatWorkspace. Тип сессии сохраняется.
- Живые потоки, ввод, история, ошибки и штатное переподключение. Стабильные порталы сохраняют DOM, буферы и черновики при смене представлений.
- Разделители с мышью и клавиатурой, разворачивание панели, перестановка работников, статусы и подсветка ожидания пользователя. Завершённые сессии можно показать.
- Порядок, пропорции и видимость завершённых сохраняются по проекту в Electron localStorage. Проверено восстановление после полного перезапуска Electron.
- Один общий локальный mux; максимум восемь WebGL-контекстов. Остальные терминалы продолжают работать через интерактивный DOM renderer xterm.
- Отдельная lab-сборка с собственными идентификатором приложения, профилем, БД, run file, Browser runtime и tmux socket. Обновления и перенос в Applications отключены.

Изменений backend, миграций БД, зависимостей, настроек моделей и разрешений новая функция не добавляет. Kanban сохраняет прежнюю логику. Исправлен также таймер копирования диагностики в настройках: он вызывал необработанные ошибки после размонтирования в полном наборе тестов.

## Алгоритм компоновки

Перебираются допустимые количества столбцов с учётом ширины. Оценка учитывает соотношение сторон ячейки (ориентир 1,35), пустые места и необходимую прокрутку. Минимум TUI-панели — 360 × 280 CSS px; при наличии Chat минимальная высота сетки — 360 px. При нехватке места включается прокрутка.

Сохранённые пропорции перераспределяются при изменении окна с ограничением минимального размера каждой дорожки. Разделитель изменяет две соседние дорожки, сохраняя их общую величину. Пропорции сохраняются отдельно для каждой формы сетки. Порядок работников определяется сохранённой перестановкой, затем временем создания и стабильным ID; потоковые обновления не пересортировывают панели.

## Проверки

| Проверка | Результат |
| --- | --- |
| Полный frontend Vitest | 430 файлов; 7081 прошёл, 6 штатно пропущены; без необработанных ошибок |
| TypeScript frontend и E2E | прошли |
| Self-hosted lockfile compatibility | npm ci dry run прошёл на закреплённом Node 22.23.2 |
| Gitleaks 7.4.0 | новые коммиты проверены с конфигурацией CI; утечек не найдено |
| Grid E2E в Electron 33 | 9 сценариев прошли |
| product-ui | 157 тестов; типизация, сборка и pack dry run прошли |
| cloud-client | генерация без дрейфа схемы, типизация, 29 тестов и pack dry run прошли |
| Docs production build | прошёл |
| Guarded-navigation browser integration | прошёл в Electron |
| Проверки сборки macOS helper/repair installer | 13 тестов прошли |
| Native helper arm64 | компиляция, lipo verify_arch и Swift state tests прошли |
| Native packaged desktop | смешанная команда, только TUI, только Chat: 1, 2, 4, 6, 9, 12 сессий — прошли |
| Нативный ввод и reconnect | ввод доставлен нужному ACP/PTY; восстановление общего mux прошло |
| Grid ↔ Kanban, размеры, черновик, завершённые | прошли; PID существующих тестовых провайдеров сохранились |
| Перезапуск Electron | раскладка сохранена; остановленные тестовые сессии не запустились повторно |
| Светлая/тёмная тема и визуальный просмотр | нативные снимки проверены, включая развёрнутую панель и прокрутку |
| Локальная ad hoc подпись | codesign --verify --deep --strict прошёл; ACP Node allow-jit проверен |

Нативные проверки используют настоящий packaged Electron, preload, daemon, SQLite, PTY и ACP с детерминированным тестовым провайдером в отдельном временном Git-проекте. Платные запросы к Claude/Codex не выполнялись. Тестовые сессии после проверки штатно завершены; их история остаётся в изолированных тестовых данных.

На Apple M1 Pro с 16 GB RAM в этих прогонах пик working set renderer составлял 353 MiB для смешанной команды, 305 MiB для 12 TUI и 416 MiB для 12 Chat. Это короткие проверки с тестовым потоком, а не длительный benchmark реальных провайдеров.

Логи, нативные снимки и JSON с PID/метриками находятся в `out/live-grid-validation/`. Они не включены в Git.

## Ограничения и недоступные проверки

- Grid в этой реализации предназначен для локальных проектов. Cloud/remote navigation остаётся в прежних представлениях.
- Автодополнение файлов в видимом Chat внутри Grid обновляет список раз в 30 секунд, чтобы отдельные file SSE не исчерпывали HTTP/1 соединения Chromium. Вывод Chat продолжает передаваться через общий CDC.
- Grid не получает wake lease для спящего Chat ради одного просмотра. Штатный пользовательский ввод сохраняет обычный путь пробуждения AO.
- Сборка локально подписана ad hoc; Developer ID и Apple notarization отсутствуют. Канонический verify-mac-artifact.sh подтверждает seal и ACP entitlements, но завершается с ожидаемой ошибкой отсутствующего stapled ticket. Gatekeeper release approval не подтверждён: на тестовой машине spctl сообщает уже существовавший override=security disabled; настройки безопасности не изменялись.
- Intel cross-link helper недоступен в установленном Apple SDK: libswiftCompatibilityConcurrency/56/Packs содержит только arm64/arm64e. Проверка Intel должна пройти на macos-26-intel CI. Windows/Linux packaging и полный Linux renderer-smoke не проверены на этом Mac; Docker отсутствует.
- Дополнительный полный Go-прогон был остановлен после ошибок в неизменённых OpenCode discovery/version тестах из-за установленного системного OpenCode. Backend не изменён, Go workflow этой веткой не затрагивается. Полный Go/race проход не заявляется.
- Удалённые Frontend и gitleaks workflows получили `action_required` без запуска jobs: требуется одобрение сопровождающего репозитория для PR из fork. Проход удалённого CI не заявляется. Production publish, update/replacement установленного AO и миграция реальных данных не выполнялись.

## Готовое приложение и безопасная проверка

Приложение: `/Users/macbookpro/Documents/ChatGPT/ao/out/Agent Orchestrator Live Grid.app`.

```sh
open '/Users/macbookpro/Documents/ChatGPT/ao/out/Agent Orchestrator Live Grid.app'
```

При обычном запуске lab использует `~/.ao/live-grid-lab`, порт 32191 и собственный Electron-профиль. Создайте одноразовый тестовый Git-проект и тестовые сессии через обычный интерфейс AO, затем откройте Open Grid. Установленный AO 0.13.7 продолжает работать со своими данными. Не направляйте lab на рабочие коммерческие репозитории и не копируйте в неё открытую production SQLite БД.

Для перехода коммерческих проектов нужен рассмотренный и подписанный обычный релиз AO, включающий эту ветку, с версией новее установленной 0.13.7. Перед плановым обновлением сохраните текущую работу и согласованную резервную копию AO; выполните обычную процедуру обновления и восстановления сессий. Не подменяйте установленное приложение этой lab-сборкой. Отдельное развёртывание production не входит в выполненные действия.

Повторяемые команды сборки и описание архитектуры: [live-grid.md](live-grid.md).

## Изменённые файлы

- `frontend/docs/live-grid-validation.md`
- `frontend/docs/live-grid.md`
- `frontend/e2e/live-grid-desktop.mjs`
- `frontend/e2e/live-grid.spec.ts`
- `frontend/e2e/support/fake-bridge.ts`
- `frontend/e2e/support/live-grid-agent.cjs`
- `frontend/forge.config.ts`
- `frontend/src/main.ts`
- `frontend/src/main/live-grid-lab.test.ts`
- `frontend/src/main/live-grid-lab.ts`
- `frontend/src/renderer/components/LiveGrid.tsx`
- `frontend/src/renderer/components/OpenGridButton.tsx`
- `frontend/src/renderer/components/SessionTopbarPortal.tsx`
- `frontend/src/renderer/components/SessionView.tsx`
- `frontend/src/renderer/components/SessionsBoard.tsx`
- `frontend/src/renderer/components/ShellTopbar.tsx`
- `frontend/src/renderer/components/TerminalPane.test.tsx`
- `frontend/src/renderer/components/TerminalPane.tsx`
- `frontend/src/renderer/components/chat/ChatComposer.test.tsx`
- `frontend/src/renderer/components/chat/ChatComposer.tsx`
- `frontend/src/renderer/components/chat/ChatWorkspace.tsx`
- `frontend/src/renderer/components/chat/SessionChatCache.test.tsx`
- `frontend/src/renderer/components/chat/SessionChatCache.tsx`
- `frontend/src/renderer/components/chat/SessionChatSurface.tsx`
- `frontend/src/renderer/components/settings/HarnessSettingsSection.test.tsx`
- `frontend/src/renderer/components/settings/HarnessSettingsSection.tsx`
- `frontend/src/renderer/hooks/useConversation.test.tsx`
- `frontend/src/renderer/hooks/useConversation.ts`
- `frontend/src/renderer/i18n/de.json`
- `frontend/src/renderer/i18n/en.json`
- `frontend/src/renderer/i18n/es.json`
- `frontend/src/renderer/i18n/fr.json`
- `frontend/src/renderer/i18n/ja.json`
- `frontend/src/renderer/i18n/ko.json`
- `frontend/src/renderer/i18n/pt-BR.json`
- `frontend/src/renderer/i18n/zh-CN.json`
- `frontend/src/renderer/lib/live-grid-layout.test.ts`
- `frontend/src/renderer/lib/live-grid-layout.ts`
- `frontend/src/renderer/lib/session-viewport.tsx`
- `frontend/src/renderer/lib/terminal-webgl-pool.test.ts`
- `frontend/src/renderer/lib/terminal-webgl-pool.ts`
- `frontend/src/renderer/routeTree.gen.ts`
- `frontend/src/renderer/routes/_shell.projects.$projectId_.grid.tsx`
- `frontend/src/renderer/routes/_shell.tsx`
- `frontend/vite.main.config.ts`

