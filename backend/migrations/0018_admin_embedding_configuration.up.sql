-- Administrator-managed embedding runtime configuration.  Provider secrets
-- remain in llm_providers; each activation snapshots the immutable vector
-- contract used by a resource index generation.

ALTER TABLE public.embedding_model_versions
    ADD COLUMN llm_model_id character varying(36)
        REFERENCES public.llm_models(id) ON DELETE SET NULL,
    ADD COLUMN send_dimensions boolean NOT NULL DEFAULT false,
    ADD COLUMN batch_size integer NOT NULL DEFAULT 32,
    ADD COLUMN timeout_seconds integer NOT NULL DEFAULT 30,
    ADD COLUMN max_retries integer NOT NULL DEFAULT 3,
    ADD COLUMN verified_at timestamp without time zone,
    ADD COLUMN activated_at timestamp without time zone,
    ADD COLUMN retired_at timestamp without time zone,
    ADD CONSTRAINT ck_embedding_model_versions_batch_size
        CHECK (batch_size BETWEEN 1 AND 256),
    ADD CONSTRAINT ck_embedding_model_versions_timeout_seconds
        CHECK (timeout_seconds BETWEEN 1 AND 300),
    ADD CONSTRAINT ck_embedding_model_versions_max_retries
        CHECK (max_retries BETWEEN 0 AND 10);

CREATE UNIQUE INDEX uq_embedding_model_versions_one_active
    ON public.embedding_model_versions (logical_name)
    WHERE status = 'active';

CREATE INDEX ix_embedding_model_versions_source_model
    ON public.embedding_model_versions (llm_model_id, status);
