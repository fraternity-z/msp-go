-- Preserve callers that predate the transactional outbox availability column.
ALTER TABLE public.outbox_events
    ALTER COLUMN available_at SET DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC');

ALTER TABLE public.resource_processing_jobs
    ADD COLUMN generation_id character varying(36) REFERENCES public.vector_index_generations(id),
    ADD COLUMN outbox_event_id character varying(36) REFERENCES public.outbox_events(id),
    ADD COLUMN stage character varying(32) NOT NULL DEFAULT 'queued';

CREATE INDEX ix_resource_processing_jobs_generation
    ON public.resource_processing_jobs (generation_id, status, document_version_id);
CREATE UNIQUE INDEX uq_resource_processing_jobs_outbox
    ON public.resource_processing_jobs (outbox_event_id) WHERE outbox_event_id IS NOT NULL;

ALTER TABLE public.resource_documents
    ADD COLUMN registration_key character varying(200),
    ADD COLUMN registration_sha256 character varying(64);
CREATE UNIQUE INDEX uq_resource_documents_registration
    ON public.resource_documents (created_by, registration_key) WHERE registration_key IS NOT NULL;

ALTER TABLE public.vector_index_generations
    ADD COLUMN reconcile_cursor character varying(128) NOT NULL DEFAULT '';
