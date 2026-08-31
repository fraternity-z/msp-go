-- Resource-center vector foundation.  PostgreSQL remains the business source
-- of truth; Qdrant receives only versioned vectors and minimal payloads.

CREATE TABLE public.tenants (
    id character varying(36) PRIMARY KEY,
    code character varying(64) NOT NULL UNIQUE,
    name character varying(200) NOT NULL,
    status character varying(20) NOT NULL DEFAULT 'active',
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    CONSTRAINT ck_tenants_code_not_blank CHECK (btrim(code) <> ''),
    CONSTRAINT ck_tenants_status CHECK (status IN ('active', 'suspended', 'deleted'))
);

CREATE TABLE public.knowledge_bases (
    id character varying(36) PRIMARY KEY,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    code character varying(100) NOT NULL,
    name character varying(200) NOT NULL,
    scenario character varying(50) NOT NULL DEFAULT 'resource',
    status character varying(20) NOT NULL DEFAULT 'active',
    active_generation bigint NOT NULL DEFAULT 0,
    acl_version bigint NOT NULL DEFAULT 1,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    CONSTRAINT uq_knowledge_bases_tenant_code UNIQUE (tenant_id, code),
    CONSTRAINT ck_knowledge_bases_code_not_blank CHECK (btrim(code) <> ''),
    CONSTRAINT ck_knowledge_bases_generation_nonnegative CHECK (active_generation >= 0),
    CONSTRAINT ck_knowledge_bases_acl_version_positive CHECK (acl_version > 0),
    CONSTRAINT ck_knowledge_bases_status CHECK (status IN ('active', 'archived', 'deleted'))
);

-- Existing contents keep their identity and gain an explicit tenant boundary.
ALTER TABLE public.contents
    ADD COLUMN tenant_id character varying(36);

INSERT INTO public.tenants (id, code, name, status)
VALUES ('00000000-0000-4000-8000-000000000001', 'default', '默认租户', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO public.knowledge_bases (id, tenant_id, code, name, scenario, status)
VALUES (
    '00000000-0000-4000-8000-000000000002',
    '00000000-0000-4000-8000-000000000001',
    'default',
    '默认资源知识库',
    'resource',
    'active'
)
ON CONFLICT (id) DO NOTHING;

UPDATE public.contents
SET tenant_id = '00000000-0000-4000-8000-000000000001'
WHERE tenant_id IS NULL;

ALTER TABLE public.contents
    ALTER COLUMN tenant_id SET NOT NULL,
    ALTER COLUMN tenant_id SET DEFAULT '00000000-0000-4000-8000-000000000001';

ALTER TABLE public.contents
    ADD CONSTRAINT contents_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES public.tenants(id);

CREATE INDEX ix_contents_tenant_status
    ON public.contents (tenant_id, status, deleted_at);

CREATE TABLE public.resource_memberships (
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    knowledge_base_id character varying(36) NOT NULL REFERENCES public.knowledge_bases(id) ON DELETE CASCADE,
    resource_id character varying(36) NOT NULL REFERENCES public.contents(id) ON DELETE CASCADE,
    status character varying(20) NOT NULL DEFAULT 'active',
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    PRIMARY KEY (knowledge_base_id, resource_id),
    CONSTRAINT ck_resource_memberships_status CHECK (status IN ('active', 'removed'))
);

INSERT INTO public.resource_memberships (tenant_id, knowledge_base_id, resource_id)
SELECT '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000002', id
FROM public.contents
ON CONFLICT (knowledge_base_id, resource_id) DO NOTHING;

CREATE INDEX ix_resource_memberships_resource
    ON public.resource_memberships (resource_id, status);

CREATE TABLE public.knowledge_base_acl (
    id character varying(36) PRIMARY KEY,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    knowledge_base_id character varying(36) NOT NULL REFERENCES public.knowledge_bases(id) ON DELETE CASCADE,
    subject_type character varying(32) NOT NULL,
    subject_id character varying(128) NOT NULL,
    permission character varying(32) NOT NULL,
    effect character varying(8) NOT NULL DEFAULT 'allow',
    created_by character varying(36) REFERENCES public.users(id) ON DELETE SET NULL,
    valid_from timestamp without time zone,
    valid_to timestamp without time zone,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    CONSTRAINT uq_knowledge_base_acl_entry UNIQUE (knowledge_base_id, subject_type, subject_id, permission, effect),
    CONSTRAINT ck_knowledge_base_acl_subject_type CHECK (subject_type IN ('tenant', 'user', 'role', 'department', 'owner')),
    CONSTRAINT ck_knowledge_base_acl_subject_id_not_blank CHECK (btrim(subject_id) <> ''),
    CONSTRAINT ck_knowledge_base_acl_permission CHECK (permission IN ('read', 'manage', 'publish')),
    CONSTRAINT ck_knowledge_base_acl_effect CHECK (effect IN ('allow', 'deny')),
    CONSTRAINT ck_knowledge_base_acl_valid_window CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to > valid_from)
);

CREATE INDEX ix_knowledge_base_acl_lookup
    ON public.knowledge_base_acl (knowledge_base_id, subject_type, subject_id, effect);

CREATE TABLE public.resource_documents (
    id character varying(36) PRIMARY KEY,
    resource_id character varying(36) NOT NULL REFERENCES public.contents(id) ON DELETE CASCADE,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    knowledge_base_id character varying(36) NOT NULL REFERENCES public.knowledge_bases(id),
    source_type character varying(32) NOT NULL DEFAULT 'upload',
    source_uri character varying(1000),
    object_uri character varying(1000),
    filename character varying(255),
    mime_type character varying(127) NOT NULL,
    byte_size bigint NOT NULL DEFAULT 0,
    checksum_sha256 character varying(64) NOT NULL,
    status character varying(20) NOT NULL DEFAULT 'active',
    created_by character varying(36) REFERENCES public.users(id) ON DELETE SET NULL,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    deleted_at timestamp without time zone,
    CONSTRAINT uq_resource_documents_checksum UNIQUE (resource_id, checksum_sha256),
    CONSTRAINT ck_resource_documents_source_type CHECK (source_type IN ('upload', 'url', 'external')),
    CONSTRAINT ck_resource_documents_source_present CHECK (source_uri IS NOT NULL OR object_uri IS NOT NULL),
    CONSTRAINT ck_resource_documents_byte_size_nonnegative CHECK (byte_size >= 0),
    CONSTRAINT ck_resource_documents_checksum CHECK (checksum_sha256 ~ '^[0-9a-fA-F]{64}$'),
    CONSTRAINT ck_resource_documents_status CHECK (status IN ('active', 'deleted'))
);

CREATE INDEX ix_resource_documents_knowledge_base
    ON public.resource_documents (knowledge_base_id, status, deleted_at);
CREATE INDEX ix_resource_documents_resource
    ON public.resource_documents (resource_id, updated_at DESC);

CREATE TABLE public.document_versions (
    id character varying(36) PRIMARY KEY,
    document_id character varying(36) NOT NULL REFERENCES public.resource_documents(id) ON DELETE CASCADE,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    version_no integer NOT NULL,
    content_sha256 character varying(64) NOT NULL,
    parser_name character varying(100) NOT NULL,
    parser_version character varying(100) NOT NULL,
    process_status character varying(20) NOT NULL DEFAULT 'queued',
    index_status character varying(20) NOT NULL DEFAULT 'pending',
    source_metadata json NOT NULL DEFAULT '{}'::json,
    chunk_count integer NOT NULL DEFAULT 0,
    index_generation bigint,
    model_version_id character varying(36),
    error_code character varying(100),
    error_message character varying(500),
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    published_at timestamp without time zone,
    deleted_at timestamp without time zone,
    CONSTRAINT uq_document_versions_number UNIQUE (document_id, version_no),
    CONSTRAINT ck_document_versions_number_positive CHECK (version_no > 0),
    CONSTRAINT ck_document_versions_checksum CHECK (content_sha256 ~ '^[0-9a-fA-F]{64}$'),
    CONSTRAINT ck_document_versions_process_status CHECK (process_status IN ('queued', 'processing', 'succeeded', 'failed')),
    CONSTRAINT ck_document_versions_index_status CHECK (index_status IN ('pending', 'building', 'ready', 'failed', 'retired')),
    CONSTRAINT ck_document_versions_chunk_count_nonnegative CHECK (chunk_count >= 0),
    CONSTRAINT ck_document_versions_generation_nonnegative CHECK (index_generation IS NULL OR index_generation >= 0)
);

ALTER TABLE public.resource_documents
    ADD COLUMN current_version_id character varying(36);

ALTER TABLE public.resource_documents
    ADD CONSTRAINT resource_documents_current_version_fkey
    FOREIGN KEY (current_version_id) REFERENCES public.document_versions(id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX ix_document_versions_document
    ON public.document_versions (document_id, version_no DESC);
CREATE INDEX ix_document_versions_processing
    ON public.document_versions (process_status, index_status, created_at);

CREATE TABLE public.document_assets (
    id character varying(36) PRIMARY KEY,
    document_version_id character varying(36) NOT NULL REFERENCES public.document_versions(id) ON DELETE CASCADE,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    asset_type character varying(32) NOT NULL,
    storage_key character varying(500) NOT NULL,
    mime_type character varying(127),
    checksum_sha256 character varying(64),
    byte_size bigint NOT NULL DEFAULT 0,
    page_no integer,
    metadata json NOT NULL DEFAULT '{}'::json,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    CONSTRAINT uq_document_assets_storage UNIQUE (document_version_id, storage_key),
    CONSTRAINT ck_document_assets_byte_size_nonnegative CHECK (byte_size >= 0),
    CONSTRAINT ck_document_assets_page_nonnegative CHECK (page_no IS NULL OR page_no >= 0)
);

CREATE INDEX ix_document_assets_version
    ON public.document_assets (document_version_id, page_no);

CREATE TABLE public.document_chunks (
    id character varying(36) PRIMARY KEY,
    document_version_id character varying(36) NOT NULL REFERENCES public.document_versions(id) ON DELETE CASCADE,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    ordinal integer NOT NULL,
    parent_chunk_id character varying(36),
    content text NOT NULL,
    content_sha256 character varying(64) NOT NULL,
    token_count integer NOT NULL DEFAULT 0,
    language character varying(32),
    page_no integer,
    section_path character varying(1000),
    start_offset integer,
    end_offset integer,
    metadata json NOT NULL DEFAULT '{}'::json,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    deleted_at timestamp without time zone,
    CONSTRAINT uq_document_chunks_ordinal UNIQUE (document_version_id, ordinal),
    CONSTRAINT ck_document_chunks_ordinal_nonnegative CHECK (ordinal >= 0),
    CONSTRAINT ck_document_chunks_checksum CHECK (content_sha256 ~ '^[0-9a-fA-F]{64}$'),
    CONSTRAINT ck_document_chunks_token_count_nonnegative CHECK (token_count >= 0),
    CONSTRAINT ck_document_chunks_page_nonnegative CHECK (page_no IS NULL OR page_no >= 0),
    CONSTRAINT ck_document_chunks_offsets CHECK (start_offset IS NULL OR (start_offset >= 0 AND (end_offset IS NULL OR end_offset >= start_offset))),
    CONSTRAINT ck_document_chunks_not_empty CHECK (btrim(content) <> '')
);

ALTER TABLE public.document_chunks
    ADD CONSTRAINT document_chunks_parent_fkey
    FOREIGN KEY (parent_chunk_id) REFERENCES public.document_chunks(id) ON DELETE SET NULL;

CREATE INDEX ix_document_chunks_version
    ON public.document_chunks (document_version_id, ordinal);

-- Keep the original table for compatibility while recording immutable provider
-- and revision identity for new indexing generations.
ALTER TABLE public.embedding_models
    ADD COLUMN provider character varying(100) NOT NULL DEFAULT 'unknown',
    ADD COLUMN provider_model character varying(200) NOT NULL DEFAULT '',
    ADD COLUMN revision character varying(100) NOT NULL DEFAULT 'unversioned',
    ADD COLUMN tokenizer character varying(100),
    ADD COLUMN normalization character varying(50),
    ADD COLUMN max_tokens integer NOT NULL DEFAULT 0,
    ADD COLUMN updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC');

ALTER TABLE public.embedding_models
    ADD CONSTRAINT ck_embedding_models_dim_positive CHECK (dim > 0),
    ADD CONSTRAINT ck_embedding_models_max_tokens_nonnegative CHECK (max_tokens >= 0),
    ADD CONSTRAINT ck_embedding_models_provider_not_blank CHECK (btrim(provider) <> ''),
    ADD CONSTRAINT ck_embedding_models_revision_not_blank CHECK (btrim(revision) <> '');

CREATE TABLE public.embedding_model_versions (
    id character varying(36) PRIMARY KEY,
    logical_name character varying(100) NOT NULL,
    provider character varying(100) NOT NULL,
    provider_model character varying(200) NOT NULL,
    revision character varying(100) NOT NULL,
    dimension integer NOT NULL,
    metric public.distancemetric NOT NULL,
    tokenizer character varying(100),
    normalization character varying(50),
    max_tokens integer NOT NULL DEFAULT 0,
    status character varying(20) NOT NULL DEFAULT 'draft',
    metadata json NOT NULL DEFAULT '{}'::json,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    CONSTRAINT uq_embedding_model_versions_identity UNIQUE (provider, provider_model, revision),
    CONSTRAINT ck_embedding_model_versions_dimension_positive CHECK (dimension > 0),
    CONSTRAINT ck_embedding_model_versions_max_tokens_nonnegative CHECK (max_tokens >= 0),
    CONSTRAINT ck_embedding_model_versions_status CHECK (status IN ('draft', 'active', 'retired'))
);

CREATE INDEX ix_embedding_model_versions_active
    ON public.embedding_model_versions (logical_name, status);

ALTER TABLE public.document_versions
    ADD CONSTRAINT document_versions_model_version_fkey
    FOREIGN KEY (model_version_id) REFERENCES public.embedding_model_versions(id);

CREATE TABLE public.vector_index_generations (
    id character varying(36) PRIMARY KEY,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    knowledge_base_id character varying(36) NOT NULL REFERENCES public.knowledge_bases(id) ON DELETE CASCADE,
    model_version_id character varying(36) NOT NULL REFERENCES public.embedding_model_versions(id),
    generation bigint NOT NULL,
    collection_name character varying(255) NOT NULL,
    dimension integer NOT NULL,
    distance public.distancemetric NOT NULL,
    state character varying(20) NOT NULL DEFAULT 'pending',
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    activated_at timestamp without time zone,
    retired_at timestamp without time zone,
    CONSTRAINT uq_vector_index_generations_kb_generation UNIQUE (knowledge_base_id, generation),
    CONSTRAINT uq_vector_index_generations_collection_generation UNIQUE (collection_name, generation),
    CONSTRAINT ck_vector_index_generations_generation_positive CHECK (generation > 0),
    CONSTRAINT ck_vector_index_generations_dimension_positive CHECK (dimension > 0),
    CONSTRAINT ck_vector_index_generations_state CHECK (state IN ('pending', 'building', 'ready', 'active', 'retired', 'failed'))
);

CREATE INDEX ix_vector_index_generations_active
    ON public.vector_index_generations (knowledge_base_id, state, generation DESC);

CREATE TABLE public.chunk_vector_manifests (
    id character varying(36) PRIMARY KEY,
    chunk_id character varying(36) NOT NULL REFERENCES public.document_chunks(id) ON DELETE CASCADE,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    generation_id character varying(36) NOT NULL REFERENCES public.vector_index_generations(id) ON DELETE CASCADE,
    model_version_id character varying(36) NOT NULL REFERENCES public.embedding_model_versions(id),
    collection_name character varying(255) NOT NULL,
    index_generation bigint NOT NULL,
    embedding_sha256 character varying(64) NOT NULL,
    dimension integer NOT NULL,
    state character varying(20) NOT NULL DEFAULT 'pending',
    indexed_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    deleted_at timestamp without time zone,
    CONSTRAINT uq_chunk_vector_manifests_chunk_generation UNIQUE (chunk_id, generation_id),
    CONSTRAINT ck_chunk_vector_manifests_checksum CHECK (embedding_sha256 ~ '^[0-9a-fA-F]{64}$'),
    CONSTRAINT ck_chunk_vector_manifests_dimension_positive CHECK (dimension > 0),
    CONSTRAINT ck_chunk_vector_manifests_generation_positive CHECK (index_generation > 0),
    CONSTRAINT ck_chunk_vector_manifests_state CHECK (state IN ('pending', 'indexed', 'failed', 'deleted'))
);

CREATE INDEX ix_chunk_vector_manifests_generation_state
    ON public.chunk_vector_manifests (generation_id, state, created_at);
CREATE INDEX ix_chunk_vector_manifests_chunk
    ON public.chunk_vector_manifests (chunk_id, deleted_at);

CREATE TABLE public.resource_processing_jobs (
    id character varying(36) PRIMARY KEY,
    tenant_id character varying(36) NOT NULL REFERENCES public.tenants(id),
    job_type character varying(50) NOT NULL,
    resource_id character varying(36) REFERENCES public.contents(id) ON DELETE CASCADE,
    document_version_id character varying(36) REFERENCES public.document_versions(id) ON DELETE CASCADE,
    idempotency_key character varying(200) NOT NULL UNIQUE,
    status character varying(20) NOT NULL DEFAULT 'pending',
    priority integer NOT NULL DEFAULT 0,
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 3,
    available_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    claimed_by character varying(100),
    lease_expires_at timestamp without time zone,
    heartbeat_at timestamp without time zone,
    last_error_code character varying(100),
    last_error_message character varying(500),
    created_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    updated_at timestamp without time zone NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
    finished_at timestamp without time zone,
    CONSTRAINT ck_resource_processing_jobs_status CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'dead', 'cancelled')),
    CONSTRAINT ck_resource_processing_jobs_priority CHECK (priority >= 0),
    CONSTRAINT ck_resource_processing_jobs_attempts CHECK (attempt_count >= 0 AND max_attempts > 0)
);

CREATE INDEX ix_resource_processing_jobs_claim
    ON public.resource_processing_jobs (status, available_at, priority DESC, created_at);
CREATE INDEX ix_resource_processing_jobs_lease
    ON public.resource_processing_jobs (lease_expires_at) WHERE status = 'running';
CREATE INDEX ix_resource_processing_jobs_resource
    ON public.resource_processing_jobs (resource_id, created_at DESC);

-- Extend the existing transactional outbox so P2 can claim resource events
-- without introducing a second delivery ledger.
ALTER TABLE public.outbox_events
    ADD COLUMN tenant_id character varying(36) REFERENCES public.tenants(id),
    ADD COLUMN aggregate_type character varying(50),
    ADD COLUMN aggregate_id character varying(36),
    ADD COLUMN idempotency_key character varying(200),
    ADD COLUMN available_at timestamp without time zone,
    ADD COLUMN lease_owner character varying(100),
    ADD COLUMN lease_expires_at timestamp without time zone,
    ADD COLUMN heartbeat_at timestamp without time zone,
    ADD COLUMN dead_at timestamp without time zone,
    ADD COLUMN error_code character varying(100),
    ADD COLUMN max_attempts integer NOT NULL DEFAULT 3;

UPDATE public.outbox_events
SET available_at = created_at
WHERE available_at IS NULL;

ALTER TABLE public.outbox_events
    ALTER COLUMN available_at SET NOT NULL,
    ADD CONSTRAINT uq_outbox_events_idempotency UNIQUE (idempotency_key),
    ADD CONSTRAINT ck_outbox_events_max_attempts_positive CHECK (max_attempts > 0),
    ADD CONSTRAINT ck_outbox_events_retry_nonnegative CHECK (retry_count >= 0);

CREATE INDEX ix_outbox_events_claim
    ON public.outbox_events (processed_at, dead_at, available_at, created_at);
CREATE INDEX ix_outbox_events_lease
    ON public.outbox_events (lease_expires_at) WHERE processed_at IS NULL AND dead_at IS NULL;
CREATE INDEX ix_outbox_events_aggregate
    ON public.outbox_events (aggregate_type, aggregate_id, created_at);
