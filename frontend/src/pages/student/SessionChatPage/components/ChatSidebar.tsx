import React from 'react';
import { Button } from '../../../../components/ui/Button';
import {
  Plus,
  CheckSquare,
  Trash2,
  Loader2,
  Clock,
  Square,
  PanelLeftClose,
  PanelLeftOpen,
  MessageCircle,
  GraduationCap,
  Target,
  Lightbulb,
} from 'lucide-react';
import { cn } from '../../../../libs/utils/cn';
import type { ChatSessionListItem } from '@/modules/session/types';
import { RequestErrorNotice } from '@/components/feedback';
import type { AppError } from '@/libs/http/appError';

interface ChatSidebarProps {
  isOpen: boolean;
  sessions: ChatSessionListItem[];
  currentSessionId?: string;
  isSelectMode: boolean;
  selectedSessionIds: string[];
  deletingSessionId: string | null;
  isBatchDeleting: boolean;
  loading: boolean;
  error?: AppError | null;
  onRetry?: () => void;
  interactionDisabled?: boolean;
  onToggleSidebar: () => void;
  onNewSession: () => void;
  onSelectSession: (sessionId: string) => void;
  onDeleteSession: (sessionId: string) => void;
  onToggleSelectMode: () => void;
  onToggleSessionSelection: (sessionId: string) => void;
  onSelectAll: () => void;
  onBatchDelete: () => void;
}

const getModeIcon = (mode: ChatSessionListItem['mode']) => {
  switch (mode) {
    case 'study':
      return <GraduationCap className="w-4 h-4" />;
    case 'practice':
      return <Target className="w-4 h-4" />;
    case 'explain':
      return <Lightbulb className="w-4 h-4" />;
    default:
      return <MessageCircle className="w-4 h-4" />;
  }
};

const formatTime = (timestamp: string) => {
  const date = new Date(timestamp);
  const now = new Date();
  const diff = now.getTime() - date.getTime();
  const minutes = Math.floor(diff / 60000);
  const hours = Math.floor(diff / 3600000);
  const days = Math.floor(diff / 86400000);

  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes}分钟前`;
  if (hours < 24) return `${hours}小时前`;
  if (days < 7) return `${days}天前`;
  return date.toLocaleDateString();
};

export const ChatSidebar = React.memo<ChatSidebarProps>(
  ({
    isOpen,
    sessions,
    currentSessionId,
    isSelectMode,
    selectedSessionIds,
    deletingSessionId,
    isBatchDeleting,
    loading,
    error,
    onRetry,
    interactionDisabled,
    onToggleSidebar,
    onNewSession,
    onSelectSession,
    onDeleteSession,
    onToggleSelectMode,
    onToggleSessionSelection,
    onSelectAll,
    onBatchDelete,
  }) => {
    return (
      <>
        {isOpen && <button type="button" aria-label="收起历史会话" onClick={onToggleSidebar} className="absolute inset-0 z-30 bg-surface-900/30 md:hidden" />}
        {/* 侧边栏 */}
        <div
          aria-hidden={!isOpen}
          inert={!isOpen}
          className={cn(
            'absolute inset-y-0 left-0 z-40 flex w-[calc(100vw-3rem)] max-w-72 shrink-0 flex-col border-r border-surface-200 bg-white transition-[margin] duration-300 dark:border-surface-700 dark:bg-surface-800 md:static md:z-auto md:w-72',
            !isOpen && 'hidden md:-ml-72 md:flex'
          )}
        >
          {/* 侧边栏头部 */}
          <div className="p-3 border-b border-surface-200 dark:border-surface-700">
            <div className="flex items-center justify-between mb-1 gap-2 whitespace-nowrap text-xs">
              <h2 className="text-lg font-semibold text-surface-900 dark:text-surface-100 shrink-0">
                历史会话
              </h2>
              <div className="flex items-center gap-1 shrink-0">
                {isSelectMode ? (
                  <>
                    <Button size="sm" variant="ghost" onClick={onSelectAll} disabled={interactionDisabled} className="flex items-center gap-1 px-2 py-1">
                      <CheckSquare className="w-3 h-3" />
                      <span className="text-xs">全选</span>
                    </Button>
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={onBatchDelete}
                      disabled={interactionDisabled || selectedSessionIds.length === 0 || isBatchDeleting}
                      className="px-2 py-1 text-white flex items-center justify-center"
                    >
                      <span className="flex items-center gap-1 text-[11px] font-semibold leading-none whitespace-nowrap">
                        {isBatchDeleting ? (
                          <Loader2 className="w-3 h-3 animate-spin" />
                        ) : (
                          <Trash2 className="w-3 h-3" />
                        )}
                        <span>删除 ({selectedSessionIds.length})</span>
                      </span>
                    </Button>
                    <Button size="sm" variant="ghost" onClick={onToggleSelectMode} disabled={interactionDisabled} className="px-2 py-1 text-xs">
                      取消
                    </Button>
                  </>
                ) : (
                  <>
                    {sessions.length > 0 && (
                      <Button size="sm" variant="ghost" onClick={onToggleSelectMode} disabled={interactionDisabled} title="批量管理">
                        <CheckSquare className="w-4 h-4" />
                      </Button>
                    )}
                    <Button size="sm" onClick={onNewSession} disabled={interactionDisabled} className="flex items-center space-x-1">
                      <Plus className="w-4 h-4" />
                      <span>新建</span>
                    </Button>
                  </>
                )}
              </div>
            </div>
          </div>

          {error && (
            <RequestErrorNotice
              error={error}
              onRetry={onRetry}
              onRefresh={onRetry}
              className="mx-3 mt-3 px-3 py-2 text-xs"
            />
          )}

          {/* 会话列表 */}
          <div className="flex-1 overflow-y-auto scroll-optimized p-2 space-y-1">
            {loading && sessions.length === 0 ? (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="w-5 h-5 animate-spin text-surface-400" />
              </div>
            ) : sessions.length === 0 ? (
              <div className="text-center py-8 text-surface-500 text-sm">暂无历史会话</div>
            ) : (
              sessions.map((session) => (
                <div
                  key={session.id}
                  onClick={() => {
                    if (interactionDisabled) return;
                    if (isSelectMode) onToggleSessionSelection(session.id);
                    else onSelectSession(session.id);
                  }}
                  className={cn(
                    'group relative p-3 rounded-lg cursor-pointer transition-colors',
                    interactionDisabled && 'opacity-60 cursor-not-allowed',
                    session.id === currentSessionId && !isSelectMode
                      ? 'bg-primary-50 dark:bg-primary-900/30 border border-primary-200 dark:border-primary-800'
                      : 'hover:bg-surface-100 dark:hover:bg-surface-800 border border-transparent',
                    isSelectMode &&
                      selectedSessionIds.includes(session.id) &&
                      'bg-primary-50 dark:bg-primary-900/30 border border-primary-200 dark:border-primary-800'
                  )}
                >
                  <div className="flex items-start space-x-2">
                    {/* 选择框 */}
                    {isSelectMode && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          if (interactionDisabled) return;
                          onToggleSessionSelection(session.id);
                        }}
                        disabled={interactionDisabled}
                        className="mt-0.5 p-1"
                      >
                        {selectedSessionIds.includes(session.id) ? (
                          <CheckSquare className="w-4 h-4 text-primary-500" />
                        ) : (
                          <Square className="w-4 h-4 text-surface-400" />
                        )}
                      </button>
                    )}
                    {!isSelectMode && (
                      <div
                        className={cn(
                          'mt-0.5 p-1.5 rounded-md',
                          session.id === currentSessionId
                            ? 'bg-primary-100 dark:bg-primary-800 text-primary-600 dark:text-primary-400'
                            : 'bg-surface-100 dark:bg-surface-700 text-surface-500 dark:text-surface-400'
                        )}
                      >
                        {getModeIcon(session.mode)}
                      </div>
                    )}
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center justify-between">
                        <span
                          className={cn(
                            'text-sm font-medium truncate',
                            session.id === currentSessionId
                              ? 'text-primary-700 dark:text-primary-300'
                              : 'text-surface-700 dark:text-surface-300'
                          )}
                        >
                          {session.title}
                        </span>
                      </div>
                      <div className="flex items-center space-x-2 mt-1">
                        <Clock className="w-3 h-3 text-surface-400" />
                        <span className="text-xs text-surface-500 dark:text-surface-400">
                          {formatTime(session.startedAt)}
                        </span>
                        <span className="text-xs text-surface-400">· {session.messageCount} 条消息</span>
                      </div>
                    </div>
                  </div>

                  {/* 删除按钮（非选择模式） */}
                  {!isSelectMode && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        if (interactionDisabled) return;
                        onDeleteSession(session.id);
                      }}
                      disabled={interactionDisabled || deletingSessionId === session.id}
                      className={cn(
                        'absolute right-2 top-1/2 -translate-y-1/2 p-1.5 rounded-md opacity-0 group-hover:opacity-100 transition-opacity',
                        'hover:bg-red-100 dark:hover:bg-red-900/30 text-surface-400 hover:text-red-500'
                      )}
                    >
                      {deletingSessionId === session.id ? (
                        <Loader2 className="w-4 h-4 animate-spin" />
                      ) : (
                        <Trash2 className="w-4 h-4" />
                      )}
                    </button>
                  )}
                </div>
              ))
            )}
          </div>
        </div>

        {/* 侧边栏切换按钮 */}
        <button
          onClick={onToggleSidebar}
          aria-label={isOpen ? '收起侧栏' : '展开侧栏'}
          title={isOpen ? '收起侧栏' : '展开侧栏'}
          className={cn('absolute top-1/2 -translate-y-1/2 z-50 p-1.5 bg-white dark:bg-surface-800 border border-surface-200 dark:border-surface-700 rounded-r-lg shadow-sm hover:bg-surface-50 dark:hover:bg-surface-700 transition-[left,background-color,border-color] duration-300', !isOpen && 'hidden md:block')}
          style={{ left: isOpen ? 'min(18rem, calc(100vw - 3rem))' : '0' }}
        >
          {isOpen ? (
            <PanelLeftClose className="w-4 h-4 text-surface-500" />
          ) : (
            <PanelLeftOpen className="w-4 h-4 text-surface-500" />
          )}
        </button>
      </>
    );
  }
);

ChatSidebar.displayName = 'ChatSidebar';
