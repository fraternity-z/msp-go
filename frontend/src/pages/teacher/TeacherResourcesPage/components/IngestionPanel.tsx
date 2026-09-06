import { useCallback, useEffect, useRef, useState } from 'react';
import { Archive, ChevronLeft, ChevronRight, FileText, Loader2, RefreshCw, RotateCcw, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { RequestErrorNotice } from '@/components/feedback';
import { useSerialPolling } from '@/hooks/useSerialPolling';
import { toAppError, type AppError } from '@/libs/http/apiClient';
import { ingestionService } from '@/modules/resource/services/ingestionService';
import { ingestionErrorMessage, ingestionStageLabel, ingestionStatusLabel, isIngestionPending, type IngestionListResponse, type IngestionStatus } from '@/modules/resource/types/ingestion';

interface IngestionPageProps {
  page: number;
  onPageChange: (page: number) => void;
  onRefresh: () => void;
  onChanged: () => void;
}

function IngestionPage({ page, onPageChange, onRefresh, onChanged }: IngestionPageProps) {
  const [result, setResult] = useState<IngestionListResponse | null>(null);
  const [error, setError] = useState<AppError | null>(null);
  const [polling, setPolling] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmation, setConfirmation] = useState<{ item: IngestionStatus; action: 'unpublish' | 'remove' } | null>(null);
  const actionRef = useRef<AbortController | null>(null);
  const pendingIDsRef = useRef(new Set<string>());
  const onChangedRef = useRef(onChanged);

  useEffect(() => { onChangedRef.current = onChanged; }, [onChanged]);
  useEffect(() => () => actionRef.current?.abort(), []);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await ingestionService.list(page, signal);
      if (signal.aborted) return;
      const published = response.items.some((item) => item.state === 'published' && pendingIDsRef.current.has(item.resource_id));
      pendingIDsRef.current = new Set(response.items.filter(isIngestionPending).map((item) => item.resource_id));
      setResult(response);
      setError(null);
      setPolling(response.items.some(isIngestionPending));
      if (page > 1 && response.items.length === 0) onPageChange(page - 1);
      if (published) onChangedRef.current();
    } catch (failure) {
      if (signal.aborted) return;
      setError(toAppError(failure, '文档状态加载失败'));
      setPolling(false);
    }
  }, [page, onPageChange]);
  useSerialPolling(load, polling ? 3000 : 0);

  const runAction = async (item: IngestionStatus, action: 'retry' | 'unpublish' | 'remove') => {
    if (actionRef.current) return;
    const controller = new AbortController();
    actionRef.current = controller;
    setBusy(item.resource_id);
    setError(null);
    try {
      await ingestionService[action](item.resource_id, controller.signal);
      if (controller.signal.aborted) return;
      setConfirmation(null);
      onChanged();
      onRefresh();
    } catch (failure) {
      if (!controller.signal.aborted) setError({ ...toAppError(failure), message: '文档操作未完成，请刷新状态后重试' });
    } finally {
      if (!controller.signal.aborted) setBusy(null);
      actionRef.current = null;
    }
  };

  return (
    <section aria-label="文档处理列表" className="min-w-0 py-4">
      <div className="mb-4 flex items-center justify-between gap-3">
        <p className="text-sm text-surface-500">{result ? `${result.total} 个文档` : '文档处理'}</p>
        <Button variant="ghost" size="icon" title="刷新文档状态" aria-label="刷新文档状态" onClick={onRefresh} disabled={Boolean(busy)}><RefreshCw className="h-4 w-4" /></Button>
      </div>
      {error && <RequestErrorNotice error={error} onRetry={onRefresh} onRefresh={onRefresh} className="mb-4" />}
      {!result && !error ? <div role="status" className="flex items-center justify-center gap-2 py-12 text-sm text-surface-500"><Loader2 className="h-5 w-5 animate-spin" />加载中...</div> : result?.items.length === 0 ? <div className="py-12 text-center text-sm text-surface-500"><FileText className="mx-auto mb-3 h-8 w-8" />暂无文档</div> : (
        <div className="divide-y divide-surface-200 dark:divide-surface-700">
          {result?.items.map((item) => (
            <article key={item.resource_id} className="flex min-w-0 flex-col gap-3 py-4 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0 flex-1">
                <h3 className="wrap-anywhere text-sm font-medium text-surface-900 dark:text-surface-100">{item.title || item.filename}</h3>
                <p className="mt-1 wrap-anywhere text-xs text-surface-500">{item.filename}</p>
                <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
                  <span className={item.error_code ? 'text-red-600 dark:text-red-400' : 'text-surface-600 dark:text-surface-300'}>{ingestionStatusLabel(item)}</span>
                  {isIngestionPending(item) && <span className="inline-flex items-center gap-1 text-surface-500"><Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />{ingestionStageLabel(item.stage) !== ingestionStatusLabel(item) && ingestionStageLabel(item.stage)}</span>}
                </div>
                {item.error_code && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{ingestionErrorMessage(item.error_code)}</p>}
              </div>
              <div className="flex shrink-0 gap-2">
                {item.can_retry && <Button variant="outline" size="sm" disabled={Boolean(busy)} onClick={() => void runAction(item, 'retry')}><RotateCcw className="mr-1 h-4 w-4" />重试</Button>}
                {item.can_unpublish && <Button variant="outline" size="sm" disabled={Boolean(busy)} onClick={() => setConfirmation({ item, action: 'unpublish' })}><Archive className="mr-1 h-4 w-4" />下线</Button>}
                {item.can_delete && <Button variant="ghost" size="icon" title={`删除 ${item.title}`} aria-label={`删除 ${item.title}`} disabled={Boolean(busy)} onClick={() => setConfirmation({ item, action: 'remove' })}><Trash2 className="h-4 w-4 text-red-500" /></Button>}
              </div>
            </article>
          ))}
        </div>
      )}
      {result && result.total > 0 && <div className="mt-5 flex items-center justify-end gap-3 border-t border-surface-200 pt-4 dark:border-surface-700">
        <Button variant="ghost" size="icon" title="上一页" aria-label="上一页" disabled={page <= 1 || Boolean(busy)} onClick={() => onPageChange(page - 1)}><ChevronLeft className="h-4 w-4" /></Button>
        <span className="text-sm text-surface-500">第 {page} 页</span>
        <Button variant="ghost" size="icon" title="下一页" aria-label="下一页" disabled={!result.has_more || Boolean(busy)} onClick={() => onPageChange(page + 1)}><ChevronRight className="h-4 w-4" /></Button>
      </div>}
      <ConfirmDialog isOpen={Boolean(confirmation)} onClose={() => { if (!busy) setConfirmation(null); }} onConfirm={() => { if (confirmation) void runAction(confirmation.item, confirmation.action); }} loading={Boolean(busy)} title={confirmation?.action === 'unpublish' ? '下线文档' : '删除文档'} message={confirmation?.action === 'unpublish' ? '下线后，学生将无法查看或检索此文档。' : '确定删除此文档吗？此操作无法撤销。'} confirmText={confirmation?.action === 'unpublish' ? '下线' : '删除'} showIcon={confirmation?.action !== 'unpublish'} />
    </section>
  );
}

export function IngestionPanel({ onChanged }: { onChanged: () => void }) {
  const [page, setPage] = useState(1);
  const [revision, setRevision] = useState(0);
  return <IngestionPage key={`${page}:${revision}`} page={page} onPageChange={setPage} onRefresh={() => setRevision((value) => value + 1)} onChanged={onChanged} />;
}
