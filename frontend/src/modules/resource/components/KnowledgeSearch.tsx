import { useEffect, useRef, useState } from 'react';
import { BookOpen, FileSearch, Loader2, Square } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { RequestErrorNotice } from '@/components/feedback';
import { toAppError, type AppError } from '@/libs/http/appError';
import { resourceSearchService } from '@/modules/resource/services/searchService';
import type { ResourceSearchResponse, SearchCitation } from '@/modules/resource/types/search';
import { CitationReader } from './CitationReader';

interface KnowledgeSearchProps {
  query: string;
  type: string;
  chapter: string;
}

// The parent keys this view by its inputs so a new query immediately drops old results.
export function KnowledgeSearch({ query, type, chapter }: KnowledgeSearchProps) {
  const [result, setResult] = useState<ResourceSearchResponse | null>(null);
  const [error, setError] = useState<AppError | null>(null);
  const [loading, setLoading] = useState(Boolean(query.trim()));
  const [cancelled, setCancelled] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [citation, setCitation] = useState<SearchCitation | null>(null);
  const requestRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!query.trim()) return;
    const controller = new AbortController();
    requestRef.current = controller;
    const timer = window.setTimeout(() => {
      if (controller.signal.aborted) return;
      void resourceSearchService.search({ query, filters: { type, chapter }, top_k: 10 }, controller.signal).then((response) => {
        if (!controller.signal.aborted) setResult(response);
      }).catch((failure: unknown) => {
        if (!controller.signal.aborted) setError(toAppError(failure, '知识搜索失败'));
      }).finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    }, 300);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query, type, chapter, attempt]);

  const retry = () => {
    setError(null);
    setResult(null);
    setCancelled(false);
    setLoading(true);
    setAttempt((value) => value + 1);
  };

  const cancel = () => {
    requestRef.current?.abort();
    setLoading(false);
    setCancelled(true);
  };

  return (
    <section aria-label="知识搜索结果" className="min-w-0">
      {loading ? (
        <div role="status" className="flex items-center justify-center gap-3 py-12 text-sm text-surface-500">
          <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" />检索中...
          <Button variant="ghost" size="sm" onClick={cancel} title="取消检索" aria-label="取消检索"><Square className="h-4 w-4" /></Button>
        </div>
      ) : error ? <RequestErrorNotice error={error} onRetry={retry} onRefresh={retry} /> : cancelled ? (
        <div className="flex items-center justify-center gap-3 py-12 text-sm text-surface-500">已取消检索<Button variant="outline" size="sm" onClick={retry}>重新检索</Button></div>
      ) : (
        <>
          {result?.degraded && <p role="status" className="mb-4 border-l-2 border-amber-400 pl-3 text-sm text-amber-700 dark:text-amber-300">{result.mode === 'none' ? '知识搜索暂时不可用' : '当前结果可能不完整'}</p>}
          {result && result.items.length > 0 ? (
            <div className="divide-y divide-surface-200 dark:divide-surface-700">
              {result.items.map((hit) => (
                <article key={`${hit.citation.chunk_id}:${hit.citation.document_version_id}:${hit.citation.generation}`} className="min-w-0 py-5 first:pt-0">
                  <button type="button" onClick={() => setCitation(hit.citation)} className="flex max-w-full items-start gap-2 text-left font-medium text-primary-700 hover:underline dark:text-primary-300">
                    <BookOpen className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" /><span className="wrap-anywhere">{hit.citation.title || '未命名资源'}</span>
                  </button>
                  <p className="my-2 line-clamp-4 whitespace-pre-wrap wrap-anywhere text-sm leading-6 text-surface-700 dark:text-surface-300">{hit.content}</p>
                  <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-surface-500 dark:text-surface-400">
                    {hit.citation.page !== null && hit.citation.page > 0 && <span>第 {hit.citation.page} 页</span>}
                    {hit.citation.section_path && <span className="break-all">{hit.citation.section_path}</span>}
                  </div>
                </article>
              ))}
            </div>
          ) : <div className="py-12 text-center text-sm text-surface-500"><FileSearch className="mx-auto mb-3 h-8 w-8 text-surface-400" aria-hidden="true" />{query.trim() ? '未找到相关内容' : '暂无检索结果'}</div>}
        </>
      )}
      {citation && <CitationReader key={`${citation.chunk_id}:${citation.document_version_id}:${citation.generation}`} citation={citation} onClose={() => setCitation(null)} />}
    </section>
  );
}
