-- Инициализация search_history для ParadeDB (карточка #52).
-- Монтируется в /docker-entrypoint-initdb.d — выполняется один раз
-- при первом старте контейнера (когда дата-том пуст).
CREATE EXTENSION IF NOT EXISTS pg_search;

CREATE TABLE search_history (
    id         BIGSERIAL PRIMARY KEY,
    task_id    BIGINT NOT NULL,
    project_id BIGINT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'note', -- note | error | command
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- BM25-индекс (карточка AC: индекс создаётся при подъеме стенда).
-- key_field не задаём: ParadeDB ≥0.26 объявил параметр deprecated no-op.
CREATE INDEX search_history_bm25 ON search_history
USING bm25 (id, content, kind);
