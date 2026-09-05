import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { Modal } from '@/components/ui/Modal';
import { RequestErrorNotice } from '@/components/feedback';
import { toAppError, type AppError } from '@/libs/http/appError';
import { resourceSearchService } from '@/modules/resource/services/searchService';
import type { SearchCitation, SearchHit } from '@/modules/resource/types/search';

interface CitationReaderProps {
  citation: SearchCitation;
  onClose: () => void;
}

export function CitationReader({ citation, onClose }: CitationReaderProps) {
  const [hit, setHit] = useState<SearchHit | null>(null);
  const [error, setError] = useState<AppError | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    void resourceSearchService.readCitation(citation, controller.signal).then((result) => {
      if (!controller.signal.aborted) setHit(result);
    }).catch((failure: unknown) => {
      if (controller.signal.aborted) return;
      const appError = toAppError(failure, '引用暂时无法读取');
      setError({ ...appError, message: appError.kind === 'not_found' || appError.kind === 'forbidden'
        ? '引用已更新或无法访问' : '引用暂时无法读取，请稍后重试' });
    });
    return () => controller.abort();
  }, [citation, attempt]);

  const retry = () => {
    setHit(null);
    setError(null);
    setAttempt((value) => value + 1);
  };

  return (
    <Modal isOpen onClose={onClose} title={hit?.citation.title || '引用阅读'} stickyHeader className="max-w-3xl max-h-[85dvh] rounded-lg p-5 sm:p-6 [&_h3]:whitespace-normal [&_h3]:wrap-anywhere [&_h3]:text-base">
      {error ? <RequestErrorNotice error={error} onRetry={retry} onRefresh={retry} className="my-4" /> : hit ? (
        <article className="min-w-0 py-4">
          <div className="mb-4 flex flex-wrap gap-x-3 gap-y-1 text-xs text-surface-500 dark:text-surface-400">
            {hit.citation.page !== null && hit.citation.page > 0 && <span>第 {hit.citation.page} 页</span>}
            {hit.citation.section_path && <span className="break-all">{hit.citation.section_path}</span>}
          </div>
          <div className="whitespace-pre-wrap wrap-anywhere text-sm leading-7 text-surface-800 dark:text-surface-200">{hit.content}</div>
        </article>
      ) : (
        <div role="status" className="flex items-center justify-center gap-2 py-16 text-sm text-surface-500">
          <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" />读取中...
        </div>
      )}
    </Modal>
  );
}
