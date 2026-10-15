# ADR 0003: Семантический слой памяти (viktor-memory)

Статус: принято (2026-10-15)

## Контекст

Stateless-агент при старте сессии не помнит ничего о проекте. Сегодня
у него три источника контекста, и все — детерминированные:

- viktor MCP — карточка/комментарии по точному `task_id`, поиск
  `search_all_tasks` — литеральный substring;
- git — история по точному ключу (`log --grep`), join-ключ — `task_id`
  в сообщении коммита (конвенция repo-task-flow);
- repowise — семантический поиск ПО КОДУ, риск, decision records.

Gap один: вопрос «зачем мы это делали» («найди всё про кэш-инвалидацию»)
не отвечает ни один источник. Синтез на лету (прочитать все задачи
проекта) — дорого и медленно. Нужна семантическая память: Qdrant +
Ollama как локальный слой эмбеддингов над задачами трекера.

## Решение

viktor-memory — подсистема внутри viktor (пакет `internal/memory` +
MCP-инструменты). Не отдельный сервис, не отдельный MCP-сервер:
переиспользуются `internal/client`, `internal/config` и паттерн
graceful degradation.

### Принцип слоя

**Source of truth — Vikunja, git и repowise. Qdrant — только их
семантическая проекция.** В индекс попадает лишь то, что не
восстанавливается за 2 вызова tool'ов: исторические решения, закрытые
задачи, rationale. Память write-only от indexer'а — агент не мутирует
её, что упрощает модель консистентности до «sync идемпотентен».

### Коллекция

Одна коллекция `viktor_tasks`. Чанк = задача целиком
(title + description + status + labels) или отдельный комментарий.
Payload каждого чанка:

```json
{
  "task_id": 123,
  "project_id": 8,
  "title": "...",
  "status": "done",
  "labels": ["memory"],
  "chunk_type": "task",
  "updated_at": "2025-01-15",
  "commits": ["abc123"]
}
```

`task_id` в payload — join-ключ ко всем детерминированным источникам:
`viktor task_get`, `git log --grep`, коммит-диффы. Закрытые задачи
индексируются наравне с открытыми — исторический контекст и есть
главная ценность слоя.

### Эмбеддинги

Ollama, модель **bge-m3** (1024-dim, мультиязычная RU/EN — корпус
задач двуязычный). `nomic-embed-text` отвергнут: хуже на русском.

### Компоненты

1. **Indexer** (`internal/memory`): инкрементальный sync по курсору
   `updated_at`; chunking; Ollama/Qdrant HTTP-клиенты по образцу
   `internal/client`; коммит-энричмент (`git log --grep="#?<task_id>"`)
   как **опция инстанса** (multi-instance: у разных инстансов Vikunja
   разные репозитории), с кэшем в payload. При offline Vikunja —
   fallback-источник: shadow mirror (`internal/shadow`).
2. **Tool'ы**: `memory_search(query, project_id?, k?)` — vector search
   с фильтрами по project/labels; `memory_status` — размер индекса,
   свежесть курсора, модель.
3. **CLI**: `viktor memory sync` — первичная загрузка и инкремент
   (cron/hook по желанию пользователя).

### Деградация

Отсутствие секции `memory` в конфиге → tool'ы отвечают `not configured`.
Недоступен Qdrant/Ollama → `{supported:false, message}` без ошибки —
по образцу `comment_recent`. Никогда не ронять сервер.

### Маршрутизация поиска (контракт для агентов)

- «зачем/почему», начало сессии, незнакомый код → `memory_search`;
- точный поиск (id, title, label) → `search_all` / `task_get`;
- код → repowise; история коммитов → `git log --grep`;
- multi-hop: `memory_search` → `payload.commits` → `git show` →
  repowise по затронутым файлам.

Зафиксировать в skill'е task-tracker-viktor (доработка) и новом
skill'е project-memory.

## Non-goals

- Индексация кода и диффов — покрывает repowise, второй индекс кода
  в Qdrant запрещён.
- Эмбеддинг на лету при записи задач — только batch sync.
- Отдельный индекс коммитов как самостоятельных точек — коммиты живут
  в payload задачи.

## Последствия

- Три внешних зависимости у памяти (Qdrant, Ollama, git) — все
  опциональны и деградируют предсказуемо; без секции `memory` в
  конфиге viktor ведёт себя как сегодня.
- Идемпотентность sync — обязательный DoD: повторный sync без
  изменений = 0 upsert'ов.
- Golden-снапшот tool surface и README-таблица обновляются при
  добавлении `memory_search`/`memory_status` (см. ADR 0002).

## Открытые вопросы

- Коммит-энричмент исторических закрытых задач: индексировать все
  проекты или только активные? (решить при реализации indexer'а)
