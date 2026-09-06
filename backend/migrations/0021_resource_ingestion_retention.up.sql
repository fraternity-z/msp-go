-- Keep one owner-visible outcome after detailed execution records expire.
ALTER TABLE public.resource_documents
    ADD COLUMN last_ingestion_job jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT ck_resource_documents_last_ingestion_job
        CHECK (jsonb_typeof(last_ingestion_job) = 'object');

CREATE INDEX ix_resource_processing_jobs_retention
    ON public.resource_processing_jobs (finished_at, id)
    WHERE status IN ('succeeded', 'failed', 'dead', 'cancelled');

CREATE TABLE public.resource_ingestion_uploads (
    id character varying(36) PRIMARY KEY,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    owner_id character varying(36) NOT NULL REFERENCES public.users(id),
    source_uri character varying(1000) NOT NULL,
    storage_key character varying(500) NOT NULL,
    state character varying(16) NOT NULL DEFAULT 'staging' CHECK (state IN ('staging','deleting')),
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    lease_token character varying(36),
    lease_expires_at timestamp without time zone,
    UNIQUE (owner_id, source_uri, storage_key)
);
CREATE INDEX ix_resource_ingestion_uploads_cleanup
    ON public.resource_ingestion_uploads (state, updated_at, lease_expires_at, id);
