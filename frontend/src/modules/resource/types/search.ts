export const DEFAULT_KNOWLEDGE_BASE_ID = '00000000-0000-4000-8000-000000000002';

export type SearchMode = 'hybrid' | 'fts_only' | 'vector_only' | 'none';

export interface SearchCitation {
  knowledge_base_id?: string;
  resource_id: string;
  document_version_id: string;
  chunk_id: string;
  generation: number;
  title: string;
  page: number | null;
  section_path: string | null;
  quote_hash: string;
}

export interface SearchHit {
  content: string;
  score: number;
  sources: string[];
  citation: SearchCitation;
}

export interface ResourceSearchRequest {
  query: string;
  knowledge_base_id?: string;
  top_k?: number;
  timeout_ms?: number;
  filters?: { type?: string; chapter?: string; topic?: string };
}

export interface ResourceSearchResponse {
  items: SearchHit[];
  mode: SearchMode;
  degraded: boolean;
  degraded_reasons: string[];
  trace_id?: string;
}
