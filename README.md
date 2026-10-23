# viktor

[![ci](https://github.com/mdarin/viktor-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mdarin/viktor-mcp/actions/workflows/ci.yml)
[![release](https://github.com/mdarin/viktor-mcp/actions/workflows/release.yml/badge.svg)](https://github.com/mdarin/viktor-mcp/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/mdarin/viktor-mcp.svg)](https://pkg.go.dev/github.com/mdarin/viktor-mcp)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)

viktor — персональный агентный слой поверх [Vikunja](https://vikunja.io):
MCP-сервер на Go, который превращает канбан-трекер в рабочую среду
AI-агента. Это уже не тонкий клиент и не дроп-ин замена
[vikunja-mcp](https://github.com/ivantohelpyou/vikunja-mcp) — проект вырос
из hard fork'а (Python, FastMCP) в самостоятельную систему с собственной
архитектурой; история и политика синхронизации с апстримом —
[ADR 0002](docs/adr/0002-hard-fork-policy.md).

Что делает viktor поверх «голого» трекера:

- **Полный CRUD трекера** — 110 tools: задачи, проекты, комментарии, метки,
  канбан, вью, команды, шаринг, handoff-очередь (assignment queue).
- **Детерминированный скоринг дня (`today_*`)** — ранжирование и кластеризация
  открытых задач по интентам с объяснимой why-трассой; настраиваемые веса;
  таксономия откладывания (dread/waiting/blocked) с эскалацией; weekly
  kill-list; непрерывный вектор целей карточки (goals-маркер, card #854).
  Никакого LLM — воспроизводимо и тестируемо.
- **FSM-жизненный цикл карточек** — claim → in_progress → submit_for_review →
  verify_review (семантическая сверка git-диффа с задачей) → done; stalled
  детект и возврат через watchdog.
- **Семантическая память проектов** — Qdrant-индекс задач, комментариев,
  коммитов и файлов репо; гибридный поиск (точный + векторный), repo-scoped
  фильтрация, orphan-чистка и full-rescan через `doctor`; CLI `viktor memory`.
- **Устойчивость** — offline shadow-mode (append-only журнал операций +
  локальное зеркало, авто-replay при возврате связи), ретраи/rate-limit в
  клиенте, честные деградации вместо падений.

Итог — один статический бинарник (stdio MCP + CLI) вместо uv/Python/uv cache
prune.

## Структура

```
cmd/viktor/          точка входа: stdio MCP-сервер + CLI (memory, watchdog, version)
internal/config/     конфигурация: env + JSON-файл, multi-instance, секреты
internal/client/     REST-клиент Vikunja (auth, ретраи, rate-limit, читаемые ошибки, пагинация, NullTime)
internal/scoring/    детерминированный scoring-движок (веса today_*, defer-политика, goals-вектор; без LLM)
internal/tools/      реестр MCP-инструментов: имя → схема → handler (110 namespaced)
internal/fsmstate/   FSM карточек (claim/submit/verify, idempotent request_id)
internal/memory/     семантическая память: Qdrant-индексатор, doctor, ghost-свит
internal/shadow/     offline shadow-режим: append-only журнал + зеркало + replay
internal/verifier/   семантическая верификация git-диффа против задачи
internal/watchdog/   детект stalled in_progress-задач
internal/server/     сборка MCP-сервера, slog-наблюдаемость
docs/adr/            архитектурные решения
```

## Сборка и установка

Из каталога модуля:

```bash
make build   # bin/viktor
make test
make lint    # golangci-lint
make ci      # lint + vet + test + build
make install # bin/viktor → ~/.local/bin/viktor
```

Из корня монорепозитория (`my-mcp`), как все модули воркспейса:

```bash
go -C viktor build -o bin/viktor ./cmd/viktor
go -C viktor test ./...
```

Установка одним бинарником:

```bash
go install viktor/cmd/viktor@latest   # если модуль опубликован
make install                          # локальная сборка в ~/.local/bin
```

Релизные артефакты: GitHub workflow `.github/workflows/release.yml` собирает
статические бинарники (darwin/linux, amd64/arm64) по тегу `v*`.

## Smoke-проверка протокола вручную

```bash
printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | VIKUNJA_URL=http://localhost:13456 VIKUNJA_TOKEN=tk_... ./bin/viktor
```

Вторая строка ответа — 94 инструмента.

## Конфигурация

Два источника, приоритет на ключ — env поверх файла:

1. **env** (как раньше): `VIKUNJA_URL`/`VIKUNJA_TOKEN` → инстанс `default`;
2. **JSON-файл**: путь из `VIKUNJA_CONFIG`, по умолчанию `~/.config/viktor/config.json`:

```json
{
  "default_instance": "main",
  "instances": {
    "main": {"url": "http://localhost:13456", "token": "tk_...", "timeout": "30s", "rate_limit": 10},
    "work": {"url": "https://work.example.com", "token": "tk_..."}
  }
}
```

Именованные инстансы также задаются env: `VIKUNJA_<ИМЯ>__URL`, `VIKUNJA_<ИМЯ>__TOKEN`
(и опционально `__TIMEOUT`, `__RATE_LIMIT`). Активный инстанс — `VIKUNJA_DEFAULT_INSTANCE`
или `default_instance` из файла; переключение в рантайме — tool `instance_switch`.

### Секция memory (опциональна)

Семантическая память (ADR 0003): `memory_search`/`memory_status` и CLI
`viktor memory sync|status|doctor`. Без секции инструменты отвечают
`{supported:false}`, CLI — ошибку с подсказкой:

```json
{
  "memory": {
    "qdrant_url": "http://localhost:16333",
    "ollama_url": "http://localhost:11434",
    "embed_model": "bge-m3",
    "collection": "viktor_tasks",
    "qdrant_api_key": "<если инстанс защищён ключом>",
    "hybrid_policy": "strict-slots",
    "git_enrich": {"main": "/path/to/repo"}
  }
}
```

`git_enrich` — карта инстанс → путь к git-репо: история коммитов
инстанса (`#<task_id>` в сообщениях) попадает в память задачи.
Пропущенные поля берут дефолты (`bge-m3`, `viktor_tasks`, localhost).
CLI-приоритет: флаг > `VIKTOR_MEMORY_*` env > секция `memory`.

Дополнительно: `full_sync_hours` (шаг полного пересканирования,
дефолт 24; card #836), `file_patterns` (какие файлы репо индексировать,
дефолт `["*.md"]`; card #842), `hybrid_policy` — порядок слияния
`search_hybrid`: `strict-slots` (дефолт, точные результаты сверху) или
`memory-first` (семантические строки в голове; card #841). Ответ
`search_hybrid` содержит `sources: {exact, memory, merged}` — сколько
строк дал каждый источник после дедупликации.

#### Секция repowise (опциональна)

Опциональный digest change-risk в `verify_review` (не блокирующий):

```json
{
  "repowise": {
    "cli": "repowise",
    "repo": "/path/to/repo",
    "timeout": "90s"
  }
}
```

Без секции `verify_review` ведёт себя как раньше. С секцией к вердикту
добавляется `change_risk` (`risk_percentile`, `review_priority`,
`findings`) из локального CLI `repowise risk --format json`; недоступный
CLI / нет индекса / таймаут деградируют в строку `"status": "unavailable"`
в дайджесте — вердикт и гейт не меняются.

| Переменная                | Обязательна | По умолчанию          | Описание                                   |
|---------------------------|-------------|-----------------------|--------------------------------------------|
| `VIKUNJA_URL`             | да*         | —                     | Базовый URL Vikunja (инстанс `default`)    |
| `VIKUNJA_TOKEN`           | да*         | —                     | API-токен (Bearer)                         |
| `VIKUNJA_CONFIG`          | нет         | `~/.config/viktor/config.json` | Путь к JSON-конфигу               |
| `VIKUNJA_DEFAULT_INSTANCE`| нет         | `default`             | Активный инстанс при старте                |
| `VIKUNJA_TIMEOUT`         | нет         | `30s`                 | Таймаут HTTP-запросов                      |
| `VIKUNJA_RATE_LIMIT`      | нет         | без лимита            | Запросов/сек на клиент (token bucket)      |
| `VIKUNJA_TOOL_TIMEOUT`    | нет         | `60` (сек)            | Таймаут одного вызова инструмента          |
| `VIKUNJA_LOG_LEVEL`       | нет         | `info`                | Уровень slog (JSON в stderr)               |
| `VIKUNJA_AGENT`           | нет         | —                     | Идентичность из `agents` инстанса: сервер действует под её токеном |
| `VIKTOR_BOT_NAME` / `VIKTOR_BOT_TOKEN` | нет | —        | Регистрация bot-токена на default-инстансе (алиасы `VIKUNJA_BOT_*`) |

\* либо полный инстанс в файле конфигурации.

### Bot-идентичность (card #616)

Инструменты `bot_*` управляют bot-пользователями Vikunja и минтят им
API-токены. Upstream исключает группы `user_*` и `tokens` из
API-токен-авторизации, поэтому `bot_create`/`bot_delete`/`bot_token_create`
работают только по JWT-сессии владельца бота (с API-токеном — 401).
Полученный токен регистрируется в конфиге:

```json
{
  "instances": {
    "main": {
      "url": "http://localhost:13456", "token": "tk_operator...",
      "agents": {"bot-viktor-mcp": "tk_bot_..."}
    }
  }
}
```

Env-сокращение `VIKTOR_BOT_NAME`/`VIKTOR_BOT_TOKEN` (алиасы
`VIKUNJA_BOT_*`) регистрирует ту же запись на default-инстансе.
Сервер никогда не выбирает bot-токен автоматически: без `VIKUNJA_AGENT`
он действует под операторским `token`, с `VIKUNJA_AGENT=<имя из agents>`
— под указанной идентичностью (bot-токен видит только расшаренные
проекты). Шаринг проектов боту — обычными `project_share*`.

Файл конфига с токенами должен иметь права `0600` — при более широких правах
сервер предупреждает в stderr при старте. Токены никогда не попадают в логи;
`instance_list` возвращает только факт наличия токена (`token_set`).

### Типовые ошибки конфигурации (card #839)

Невалидный конфиг валидируется при старте: сервер печатает точное имя
поля и ожидаемый тип в stderr и завершается с exit 1. Частые ловушки:

1. **Длительность как число.** Все timeout'ы (`instances.*.timeout`,
   `repowise.timeout`, env `VIKUNJA_TIMEOUT`) — строки-дюрации:
   `"timeout": "90s"`, а не `"timeout": 90`. Ошибка:
   `field "repowise.timeout": expected duration string (e.g. "90s"), got number`.
2. **Число в строковом поле** (например, `memory.qdrant_url: 123`):
   `field "memory.qdrant_url": expected string, got number`.
3. **Dim mismatch памяти**: модель эмбеддинга (`memory.embed_model`)
   даёт иную размерность, чем существующая Qdrant-коллекция — sync
   падает на старте с внятной ошибкой (см. `memory --status`), точек
   индекс не трогает.
4. **Скрытые soft-delete**: удалённые задачи Vikunja скрывает из API,
   но ghost-чанки остаются в индексе до `memory sync -full`. Диагностика
   — `viktor memory doctor` (card #837): свипает коллекцию по живым
   task_id, печатает сирот по chunk_type с примерами payload; удаление
   — только с `-prune` (по умолчанию dry run): `viktor memory doctor -prune`.

## Подключение как MCP-сервер

Пример для [opencode](https://opencode.ai) (`~/.config/opencode/opencode.json`):

```json
{
  "mcp": {
    "vikunja": {
      "type": "local",
      "command": [
        "/Users/mikhail/.local/bin/viktor"
      ],
      "enabled": true,
      "environment": {
        "VIKUNJA_URL": "http://localhost:13456",
        "VIKUNJA_TOKEN": "tk_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
      }
    }
  }
}
```

- `type: local` — stdio-транспорт, бинарник запускается локально;
- `environment` — переменные выше; токен Vikunja создаётся в UI: Settings → API Tokens;
- путь в `command` — куда установлена сборка `bin/viktor`.

Для Claude Desktop аналогично (`claude_desktop_config.json`, секция `mcpServers`):

```json
{
  "mcpServers": {
    "vikunja": {
      "command": "/usr/local/bin/viktor",
      "env": {
        "VIKUNJA_URL": "http://localhost:13456",
        "VIKUNJA_TOKEN": "tk_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
      }
    }
  }
}
```

## Tool surface

94 namespaced-инструмента — полный перенос upstream `main` (`task_*`, `project_*`, `today_*`, `batch_*`, `kanban_*`, `view_*`, `label_*`, `comment_*`, `teams_*`, `search_*`, `instance_*`, `ctx_*`, `config_*`, `cal_add_event`, `assign_*`, `triage_*`) + `task_batch_get` (конкурентные чтения) и `project_share_team` (шаринг на команды).

Golden-тест фиксирует снапшот сёрфейса (`internal/tools/testdata/golden.json`) и падает при любом дрейфе; обновление после намеренного изменения:

```bash
go test ./internal/tools -update-snapshot
```

### Полная таблица инструментов

Таблица ниже генерируется из `internal/tools/testdata/golden.json` и проверяется
тестом на дрейф (`go test ./internal/tools -run TestToolsDocTable -update-doc`):

<!-- tools:begin -->
| Инструмент | Описание |
|---|---|
| `assign_apply` | Claim and complete a handoff item from the assignment queue |
| `assign_queue` | Check the assignment queue for pending handoff items |
| `batch_assign_buckets` | Bulk assign tasks to kanban buckets |
| `batch_complete_by_label` | Complete all tasks with a matching label |
| `batch_create_labels` | Create multiple labels, skipping existing ones |
| `batch_create_tasks` | Create multiple tasks at once with labels and relations |
| `batch_label_to_buckets` | Move tasks to buckets based on their labels |
| `batch_move_by_label` | Move all tasks with a matching label to a bucket |
| `batch_relabel` | Bulk update labels on multiple tasks |
| `batch_reorder_tasks` | Set positions for multiple tasks in a view |
| `batch_update_tasks` | Update multiple tasks at once |
| `board_audit` | Check a project's kanban board against its profile (missing/extra columns, done bucket, counts); hygiene:true adds the tracker-git cross-check: done cards without a card #N commit and recent commits without a card reference (best-effort, dry-run) |
| `bot_create` | Create a bot user (username must start with 'bot-') |
| `bot_delete` | Delete a bot user permanently |
| `bot_disable` | Disable a bot user (status=2); revokes its tokens' access immediately |
| `bot_list` | List bot users owned by the authenticated operator |
| `bot_token_create` | Mint a long-lived API token for a bot user (returned once) |
| `cal_add_event` | Add an event to the calendar by creating a labeled task |
| `claim_task` | Atomically claim a task for in_progress work with request_id idempotency (FSM) |
| `comment_add` | Add a comment to a task |
| `comment_delete` | Delete a comment |
| `comment_list` | List comments on a task |
| `comment_recent` | Recent comments across all tasks |
| `comment_update` | Update a comment |
| `config_delete` | Delete a config key |
| `config_get` | Read a config value |
| `config_list` | List all config keys |
| `config_set` | Write a config value |
| `config_update` | Update an existing config value |
| `ctx_get` | Get the current default instance and project context |
| `ctx_set` | Set default instance and project for subsequent queries |
| `fsm_state` | Report or change the FSM state of a task; forbidden transitions are rejected |
| `get_task_context` | Task context budgeted by its FSM state (only status-relevant fields) |
| `instance_check_health` | Check if a Vikunja API token is valid |
| `instance_connect` | Add and connect a new Vikunja instance |
| `instance_disconnect` | Remove a configured Vikunja instance |
| `instance_list` | List all configured Vikunja instances |
| `instance_rename` | Rename a configured instance |
| `instance_switch` | Switch to a different Vikunja instance |
| `kanban_create_bucket` | Create a kanban bucket |
| `kanban_delete_bucket` | Delete a kanban bucket; tasks may move to another bucket |
| `kanban_get` | Get the kanban view for a project |
| `kanban_list_buckets` | List kanban buckets in a view |
| `kanban_setup` | Set up a kanban board with exactly the requested columns, one call, idempotent |
| `kanban_sort_bucket` | Re-sort all tasks in a bucket |
| `kanban_tasks_by_bucket` | Get tasks grouped by kanban bucket |
| `label_create` | Create a label |
| `label_delete` | Delete a label |
| `label_list` | List all labels |
| `memory_search` | Semantic search over project memory (Vikunja tasks, comments, repo commits). Use for 'why/зачем' questions and historical context; returns task_id/title/snippet/status/commits/chunk_type/repo; optional repo narrows to one indexed repo |
| `memory_status` | Semantic memory index status: points, cursor, embed model |
| `project_analyze` | Analyze project data to discover meaningful grouping dimensions |
| `project_create` | Create a project |
| `project_create_from_template` | Create a project from a template with exactly the requested kanban columns |
| `project_delete` | Delete a project and all its tasks permanently |
| `project_export` | Export all projects and tasks for backup |
| `project_get` | Get project details |
| `project_import` | Import projects from an export file |
| `project_list` | List projects on the current instance |
| `project_list_all` | List projects across all configured instances |
| `project_setup` | Set up a project with buckets, labels, and tasks in one call |
| `project_share` | Share a project with a user; resolves user_id/username pair automatically (Vikunja needs both) |
| `project_share_team` | Share a project with a team, list team shares, or revoke access (action: share\|list\|revoke; permission 0=read 1=write 2=admin) |
| `project_update` | Update a project |
| `search_all` | Search tasks and projects across instances |
| `search_all_tasks` | List tasks across instances with filters |
| `search_hybrid` | Hybrid search: exact task filtering plus top-K semantic hits from the memory index (source=memory); memory issues degrade to a note, never fail |
| `shadow_clear` | Remove already-synced entries from the shadow journal |
| `shadow_status` | Show shadow-mode state: online/offline, pending queue size, mirror root |
| `shadow_sync` | Probe the tracker and report shadow-queue synchronization state |
| `submit_for_review` | Move a claimed task to in_review after validating the git artifact |
| `task_add_label` | Add a label to a task |
| `task_assign_user` | Assign a user to a task |
| `task_batch_get` | Fetch multiple tasks concurrently, results in request order |
| `task_complete` | Atomically close a task: done, done bucket, verification GET |
| `task_create` | Create a task in a project |
| `task_create_relation` | Create a relation between two tasks |
| `task_delete` | Delete a task permanently |
| `task_get` | Get task details |
| `task_list` | List tasks in a project |
| `task_list_relations` | List relations of a task |
| `task_move` | Move a task to a different project |
| `task_query` | Query tasks by mode: focus, due-today, due-week, overdue, high-priority, urgent, unscheduled, summary |
| `task_set_position` | Move a task to a kanban bucket |
| `task_set_reminders` | Replace all reminders on a task |
| `task_unassign_user` | Remove a user from a task |
| `task_update` | Partially update a task; merges server-side so omitted fields keep their values |
| `teams_create` | Create a team (PUT semantics: creator becomes admin) |
| `teams_delete` | Delete a team permanently |
| `teams_list` | List all teams the authenticated user belongs to |
| `teams_member_add` | Add a user to a team by username |
| `teams_member_remove` | Remove a user from a team by username |
| `teams_search` | Search teams by name substring |
| `teams_update` | Update a team name or description |
| `today_actions` | Scored, clustered candidates for today under named intents, each with a numeric score and a why trace |
| `today_get_weights` | Read the current effective scoring weights |
| `today_reckoning` | Weekly kill list: tasks deferred N+ times, loudest first |
| `today_set_weights` | Retune deterministic scoring weights at runtime, per user |
| `today_snooze` | Defer a task with a mandatory reason; dread deferrals demand a date and escalate on return |
| `triage_park` | Park a whole speculative project tree aside in one gesture; reversible, never deletes |
| `triage_parked` | List currently parked project trees |
| `verify_review` | Verify the submitted git diff against the task's semantic anchors in Qdrant (hybrid: similarity + exact matches); drift queues a Shadow Task |
| `view_create` | Create a view for a project |
| `view_delete` | Delete a view from a project |
| `view_get_tasks` | Get tasks via a specific view; kanban includes bucket info |
| `view_list` | List all views for a project |
| `view_set_position` | Set a task position within a list, gantt, or table view |
| `view_update` | Update a view title or filter |
| `watchdog_resume` | Return a stalled task to in_progress with an audit trail (who/when) |
| `watchdog_scan` | Stall FSM-tracked in_progress tasks that have seen no tracker activity beyond the threshold |
<!-- tools:end -->

## Непрерывные цели карточки (goals-маркер, card #854)

Карточка может объявить вектор целей в описании — невидимым HTML-комментарием
(паттерн как у маркера defer из `today_snooze`):

```
<!-- viktor:goals speed=0.9 quality=0.3 safety=0.7 scope=0.5 -->
```

- Оси: `speed`, `quality`, `safety`, `scope`, все 0..1; `0.5` — нейтральное
  значение и вклад в score не даёт. Отсутствующие оси считаются 0.5,
  карточка без маркера ранжируется ровно как раньше.
- Вклад детерминированный: `(value − 0.5) × 2 × вес_оси` (веса: quality 4,
  speed 3, safety 3, scope −2 — широкий scope слегка понижает срочность
  старта). Виден в why-trace `today_actions` как term `goals`.
- Неизвестные оси возвращаются в ответе `today_actions` в поле
  `goals_unknown_axes`; битый маркер (нечисловое значение известной оси)
  молча игнорируется, ранжирование не ломается.
- Поле `Engine.SpeedDamp` — зарезервированный детерминированный хук
  контекстной модуляции (0 = нет): гасит ось speed, будущая интеграция
  repowise-hotspot'ов станет его единственным писателем.
- Веса осей пока фиксированные константы, не `today_set_weights`: при
  ~десятках конфликтов в неделю калибровочных данных для настраиваемых
  весов нет.

## Offline mode (shadow)

Когда Vikunja недоступен (connection refused / DNS / TLS, но не HTTP-ошибки),
viktor не падает, а переключается в теневой режим:

- **Write** (`task_create`, `task_update`, `task_complete`, `task_add_label`,
  `comment_add`, `batch_create_tasks`, `batch_update_tasks`, `batch_relabel`,
  `batch_complete_by_label`) — операция дописывается в append-only журнал и
  возвращается ответ `{"shadowed": true, "shadow_id": "s-N", ...}` без ошибки.
- **Read** (`task_get`, `task_list`) — отдаётся локальное зеркало с пометкой
  `shadowed_data: true`; если карточки нет в зеркале — внятная ошибка
  «tracker unreachable».
- Остальные инструменты при offline отвечают ошибкой «not supported offline».

Состояние хранится в `~/.local/state/viktor/shadow/<instance>/`:
`queue.jsonl` (append-only журнал операций: `{shadow_id, ts, op, project_id,
payload, status, remote_id}`) и зеркала `projects/<id>-<slug>/`
(`project.md` — читаемое рендер-зеркало, `tasks/*.json` — снапшоты).

Синхронизация (`shadow_sync` или автоматически при первом успешном вызове
после восстановления связи): журнал реплеится по порядку. `create` дедуплицируется
по fingerprint (project + title + due_date), `update` мержится по полям, при
конфликте (карточку меняли на трекере после записи в журнал) локальные правки
применяются, но конфликт фиксируется в отчёте. Ответ — структурированный отчёт
`{replayed, created, updated, completed, labels, comments, deduplicated,
deleted_shadowed, pending, conflicts, failed}`. Внесённые на доску теневые
карточки удаляются из зеркала, `project.md` перегенерируется, пустые зеркала
проектов удаляются. `shadow_status` показывает режим и очередь, `shadow_clear`
вычищает synced-записи журнала.

## Live-интеграция и ловушки Vikunja v2.7.1

Сервер проверен против реального Vikunja v2.7.1 (ранее v2.6.0). Нестандартности, учтённые в клиенте:

- **Создание задач и проектов — только `PUT`**: `POST /projects/{id}/tasks` и
  `POST /projects` возвращают `405 Method Not Allowed`. Клиент использует `PUT`.
- **Позиции задач**: только `POST /tasks/{id}/position` (единственное число).
  Варианты `PUT/PATCH` и `/positions` возвращают 404. Тело:
  `{"project_view_id", "task_id", "bucket_id"}`.
- **done-бакет**: при закрытии задачи bucket не переключается автоматически;
  `task_complete` ищет/создаёт колонку done (`done_bucket_id` у kanban view) и
  переносит задачу туда, затем сверяет результат контрольным GET.
- **Пользователи**: `GET /users/{id}` → 404; поиск только `GET /users?s=<точный
  username>`. Поэтому `project_share` резолвит `user_id` по точному username.
- **Шаринг**: повторный шаринг пользователю с доступом → HTTP 409 с кодом 7002
  (трактуется как уже-имеет-доступ, идемпотентно).
- **Права**: `DELETE`/`PUT` от того же токена работают штатно; обновления
  (`task_update`) делаются merge-ом: GET текущего тела → наложение патча → POST
  полным телом (частичный PATCH не поддержан).
- **Rename bucket** через `PUT /buckets/{id}` → 405 в этой версии; переименование
  не используется.

Observability: каждый вызов инструмента пишет JSON-лог в stderr
(`{"msg":"tool call","tool":...,"duration_ms":...,"status":"ok|error"}`), при
завершении — сводка `tool stats` по каждому инструменту. По логам видно, какой
инструмент тормозит или падает, без отладчика.

## Dev-стенд (docker compose)

Полный стек «Очереди 2» поднимается одной командой (`docker-compose.dev.yml`,
карточка #52): Vikunja на Postgres, ParadeDB (BM25 `search_history`),
Qdrant и Ollama (bge-m3). Порты намеренно в верхних диапазонах, чтобы не
конфликтовать с боевыми инстансами:

| Сервис   | Адрес                        | Примечание                              |
|----------|------------------------------|-----------------------------------------|
| Vikunja  | http://localhost:13457       | Postgres backend, registration выключена |
| Qdrant   | http://localhost:16333       | UI: `/dashboard`                        |
| ParadeDB | localhost:15433              | postgres `viktor/viktor`, БД `viktor`   |
| Ollama   | http://localhost:11435       | `bge-m3` тянется отдельно (см. ниже)    |

```sh
make dev-up          # поднять весь стек
make dev-down        # остановить (данные в named volumes)
make dev-logs        # хвост логов
make dev-reset       # снести вместе с данными
```

Первый запуск:

```sh
# админ Vikunja:
docker compose -f docker-compose.dev.yml exec vikunja \
  /app/vikunja user create --user admin --password <pass> --email admin@local
# модель эмбеддингов:
docker compose -f docker-compose.dev.yml exec ollama ollama pull bge-m3
```

`search_history` и BM25-индекс создаются автоматически при первом старте
ParadeDB (`deploy/paradedb-init/01_search_history.sql`). Для `memory sync`
против стенда см. dev-блок в `.env.example`.

## Выбор SDK

Официальный [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) (v1.8.0) — см. [ADR 0001](docs/adr/0001-sdk-choice.md).

Апстрим: hard fork от vikunja-mcp 0.9.3 (PyPI) с письменной политикой
расхождений и чеклистом сверки — см. [ADR 0002](docs/adr/0002-hard-fork-policy.md).

## Статус

Актуальный релиз: **v0.8.1** (Go, 110 tools, unit + golden + surface-check +
live-тесты против реального Vikunja v2.7.1).

Пройдено и в бою: полный перенос апстрима; multi-instance; shadow-offline;
FSM-жизненный цикл с верификацией диффов; семантическая память (hybrid,
repo-scoped, doctor/full-rescan/ghost-свит); детерминированный скоринг дня с
defer-таксономией, эскалацией и непрерывными целями (goals-маркер). Ближайшие
шаги — журнал рефлексии в PostgreSQL (card #853) и аудит гигиены трекера
(card #855).

## Лицензия

[MIT](LICENSE)
