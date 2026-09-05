-- Citations are historical metadata, not an authorization grant or stored context.
ALTER TABLE public.session_messages
    ADD COLUMN knowledge jsonb,
    ADD CONSTRAINT ck_session_messages_knowledge_object
        CHECK (knowledge IS NULL OR jsonb_typeof(knowledge) = 'object');

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE INDEX ix_document_chunks_search_fts
    ON public.document_chunks USING gin (to_tsvector('simple'::regconfig, content))
    WHERE deleted_at IS NULL;

CREATE INDEX ix_document_chunks_search_substring
    ON public.document_chunks USING gin (content public.gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX ix_contents_resource_search_title
    ON public.contents USING gin (title public.gin_trgm_ops)
    WHERE deleted_at IS NULL AND status = 'PUBLISHED';
