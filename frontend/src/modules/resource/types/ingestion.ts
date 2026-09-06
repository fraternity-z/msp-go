export interface IngestionStatus {
  resource_id: string;
  document_version_id: string;
  job_id: string;
  knowledge_base_id: string;
  title: string;
  filename: string;
  mime_type: string;
  byte_size: number;
  state: 'queued' | 'processing' | 'published' | 'failed' | 'dead' | 'unpublished' | 'deleted';
  publication_status: 'draft' | 'published' | 'unpublished' | 'deleted';
  stage: string;
  retryable: boolean;
  process_status: string;
  index_status: string;
  job_status: string;
  attempt: number;
  max_attempts: number;
  indexed_chunks: number;
  error_code?: string;
  can_retry: boolean;
  can_unpublish: boolean;
  can_delete: boolean;
  created_at: string;
  updated_at: string;
  chunk_count: number;
}

export interface IngestionListResponse {
  items: IngestionStatus[];
  total: number;
  page: number;
  page_size: number;
  has_more: boolean;
}

export interface IngestionUpload {
  file: File;
  title: string;
  chapter?: string;
  topic?: string;
  client_request_id: string;
}

export function isIngestionPending(item: IngestionStatus): boolean {
  return item.state === 'queued' || item.state === 'processing';
}

export function ingestionStatusLabel(item: IngestionStatus): string {
  const labels: Record<string, string> = {
    queued: item.attempt > 0 ? '等待重试' : '等待处理', processing: '处理中',
    published: '已发布', failed: '处理失败', dead: '处理失败', unpublished: '已下线', deleted: '已删除',
  };
  return labels[item.state] || '等待状态更新';
}

export function ingestionErrorMessage(code: string): string {
  const messages: Record<string, string> = {
    EMPTY_DOCUMENT: '文档没有可读取的内容，请更换文件。',
    SCANNED_PDF: '文档为扫描件，请上传包含文字的文档。',
    ENCRYPTED_DOCUMENT: '文档已加密，请先解除密码保护。',
    UNSUPPORTED_TYPE: '文档格式不受支持。',
    FILE_TOO_LARGE: '文档超过大小限制。',
    DOCUMENT_TOO_LARGE: '文档内容超过处理限制。',
    PARSE_FAILED: '文档无法读取，请检查文件是否完整。',
    EMBEDDING_UNAVAILABLE: '内容处理暂不可用，请稍后重试。',
    MODEL_UNAVAILABLE: '内容处理暂不可用，请联系管理员。',
    DOCUMENT_EMPTY: '文档没有可读取的内容，请更换文件。',
    PDF_NO_TEXT: '文档为扫描件，请上传包含文字的文档。',
    DOCUMENT_ENCRYPTED: '文档已加密，请先解除密码保护。',
    OBJECT_UNSUPPORTED: '文档格式不受支持。',
    OBJECT_TOO_LARGE: '文档超过大小限制。',
    DOCUMENT_PAGE_LIMIT: '文档页数超过处理限制，请拆分后上传。',
    DOCUMENT_CHARACTER_LIMIT: '文档内容超过处理限制，请拆分后上传。',
    DOCUMENT_BLOCK_LIMIT: '文档内容超过处理限制，请拆分后上传。',
    DOCUMENT_INVALID: '文档无法读取，请检查文件是否完整。',
    DOCUMENT_MATH_UNSUPPORTED: '文档包含暂不支持的公式，请调整后重新上传。',
    OBJECT_READ_FAILED: '文档暂时无法读取，请稍后重试。',
    PARSER_UNAVAILABLE: '文档读取暂不可用，请联系管理员。',
    PROVIDER_UNAVAILABLE: '内容处理暂不可用，请稍后重试。',
  };
  return messages[code.toUpperCase()] || '文档暂时无法完成处理。';
}

export function ingestionStageLabel(stage: string): string {
  const labels: Record<string, string> = { queued: '等待处理', parsing: '读取文档', indexing: '整理知识', completed: '处理完成', failed: '处理失败', dead: '处理失败', purging: '清理中', cancelled: '已取消' };
  return labels[stage] || '正在处理';
}
