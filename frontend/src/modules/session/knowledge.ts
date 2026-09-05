import type { SearchCitation, SearchMode } from '@/modules/resource/types/search';

export interface SessionKnowledge {
  mode: SearchMode;
  degraded: boolean;
  degraded_reasons: string[];
  citations: SearchCitation[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCitation(value: unknown): value is SearchCitation {
  if (!isRecord(value)) return false;
  const uuid = /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i;
  return ['resource_id', 'document_version_id', 'chunk_id'].every((key) => typeof value[key] === 'string' && uuid.test(value[key]))
    && (value.knowledge_base_id === undefined || (typeof value.knowledge_base_id === 'string' && uuid.test(value.knowledge_base_id)))
    && typeof value.generation === 'number' && Number.isSafeInteger(value.generation) && value.generation > 0
    && typeof value.title === 'string' && value.title.length <= 2000
    && typeof value.quote_hash === 'string' && value.quote_hash.length <= 256
    && (value.page === null || (typeof value.page === 'number' && Number.isSafeInteger(value.page) && value.page >= 0))
    && (value.section_path === null || (typeof value.section_path === 'string' && value.section_path.length <= 2000));
}

export function normalizeSessionKnowledge(value: unknown): SessionKnowledge | null {
  if (!isRecord(value) || typeof value.mode !== 'string' || !['hybrid', 'fts_only', 'vector_only', 'none'].includes(value.mode)
    || typeof value.degraded !== 'boolean' || !Array.isArray(value.degraded_reasons)
    || value.degraded_reasons.length > 20 || !value.degraded_reasons.every((reason) => typeof reason === 'string' && reason.length <= 128)
    || !Array.isArray(value.citations) || value.citations.length > 20 || !value.citations.every(isCitation)) return null;
  return { mode: value.mode as SearchMode, degraded: value.degraded, degraded_reasons: value.degraded_reasons, citations: value.citations };
}
