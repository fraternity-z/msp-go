import { apiClient } from '@/libs/http/apiClient';
import { DEFAULT_KNOWLEDGE_BASE_ID } from '@/modules/resource/types/search';
import type { ResourceSearchRequest, ResourceSearchResponse, SearchCitation, SearchHit } from '@/modules/resource/types/search';

export const resourceSearchService = {
  async search(request: ResourceSearchRequest, signal?: AbortSignal): Promise<ResourceSearchResponse> {
    const response = await apiClient.post<ResourceSearchResponse>('/resources/search', request, { signal });
    return response.data;
  },

  async readCitation(citation: SearchCitation, signal?: AbortSignal): Promise<SearchHit> {
    const response = await apiClient.get<SearchHit>(`/resources/citations/${encodeURIComponent(citation.chunk_id)}`, {
      params: {
        knowledge_base_id: citation.knowledge_base_id || DEFAULT_KNOWLEDGE_BASE_ID,
        document_version_id: citation.document_version_id,
        generation: citation.generation,
      },
      signal,
    });
    return response.data;
  },
};
