import { useState } from 'react';
import { BookOpen } from 'lucide-react';
import type { SessionKnowledge } from '@/modules/session/knowledge';
import type { SearchCitation } from '@/modules/resource/types/search';
import { CitationReader } from '@/modules/resource/components/CitationReader';

export function KnowledgeSources({ knowledge }: { knowledge?: SessionKnowledge | null }) {
  const [selected, setSelected] = useState<SearchCitation | null>(null);
  const citations = knowledge?.citations ?? [];
  const enhanced = citations.length > 0;
  return (
    <div className="mt-4 min-w-0 border-t border-surface-100 pt-3 dark:border-surface-700">
      <p className="text-xs text-surface-500 dark:text-surface-400">{enhanced ? '知识增强' : '普通回答'}{knowledge?.degraded ? (enhanced ? ' · 部分知识暂不可用' : ' · 知识暂不可用') : ''}</p>
      {enhanced && <ol className="mt-2 space-y-2">
        {citations.map((citation, index) => <li key={`${citation.chunk_id}:${citation.document_version_id}:${index}`}>
          <button type="button" onClick={() => setSelected(citation)} className="flex max-w-full items-start gap-1.5 text-left text-xs leading-5 text-primary-700 hover:underline dark:text-primary-300">
            <BookOpen className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
            <span className="wrap-anywhere">[{index + 1}] {citation.title || '未命名资源'}{citation.page !== null && citation.page > 0 ? ` · 第 ${citation.page} 页` : ''}</span>
          </button>
        </li>)}
      </ol>}
      {selected && <CitationReader key={`${selected.chunk_id}:${selected.document_version_id}:${selected.generation}`} citation={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
