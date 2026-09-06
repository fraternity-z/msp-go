import { apiClient } from '@/libs/http/apiClient';
import type { IngestionListResponse, IngestionStatus, IngestionUpload } from '../types/ingestion';

const basePath = '/resources/ingestions';

export function validateIngestionFile(file: File): { valid: boolean; error?: string } {
  if (!/\.(pdf|docx|txt|md)$/i.test(file.name)) return { valid: false, error: '文档仅支持 PDF、DOCX、TXT 和 MD' };
  if (file.size === 0) return { valid: false, error: '文件不能为空' };
  if (file.size > 50 * 1024 * 1024) return { valid: false, error: '文档不能超过 50 MiB' };
  return { valid: true };
}

export const ingestionService = {
  async upload(input: IngestionUpload, onProgress?: (percent: number) => void, signal?: AbortSignal): Promise<IngestionStatus> {
    const validation = validateIngestionFile(input.file);
    if (!validation.valid) throw new Error(validation.error);
    const form = new FormData();
    form.append('file', input.file);
    form.append('title', input.title);
    form.append('chapter', input.chapter || '');
    form.append('topic', input.topic || '');
    form.append('client_request_id', input.client_request_id);
    const response = await apiClient.post<IngestionStatus>(basePath, form, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 120000,
      signal,
      onUploadProgress: onProgress ? (event) => {
        const total = event.total ?? input.file.size;
        onProgress(Math.min(100, Math.round(event.loaded * 100 / total)));
      } : undefined,
    });
    return response.data;
  },
  async list(page: number, signal?: AbortSignal): Promise<IngestionListResponse> {
    const response = await apiClient.get<IngestionListResponse>(basePath, { params: { page, page_size: 10 }, signal });
    return response.data;
  },
  async get(resourceID: string, signal?: AbortSignal): Promise<IngestionStatus> {
    const response = await apiClient.get<IngestionStatus>(`${basePath}/${encodeURIComponent(resourceID)}`, { signal });
    return response.data;
  },
  async retry(resourceID: string, signal?: AbortSignal): Promise<IngestionStatus> {
    const response = await apiClient.post<IngestionStatus>(`${basePath}/${encodeURIComponent(resourceID)}/retry`, undefined, { signal });
    return response.data;
  },
  async unpublish(resourceID: string, signal?: AbortSignal): Promise<IngestionStatus> {
    const response = await apiClient.post<IngestionStatus>(`${basePath}/${encodeURIComponent(resourceID)}/unpublish`, undefined, { signal });
    return response.data;
  },
  async remove(resourceID: string, signal?: AbortSignal): Promise<void> {
    await apiClient.delete(`${basePath}/${encodeURIComponent(resourceID)}`, { signal });
  },
};
