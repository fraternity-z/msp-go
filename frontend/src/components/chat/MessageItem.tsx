/**
 * 消息项组件
 *
 * 使用 React.memo 优化，避免消息列表中的不必要重渲染
 */

import React from 'react';
import {
  Bot,
  User,
  Sparkles,
  ThumbsUp,
  ThumbsDown,
} from 'lucide-react';
import { cn } from '../../libs/utils/cn';
import { normalizeSafeImageAttachmentUrl } from '../../libs/utils/safeUrl';
import { MarkdownContent } from './MarkdownContent';
import { StreamingMarkdownContent } from './StreamingMarkdownContent';
import { KnowledgeSources } from './KnowledgeSources';
import type { SessionKnowledge } from '@/modules/session/knowledge';

export interface MessageItemProps {
  id: string;
  role: 'student' | 'assistant' | 'system';
  content: string;
  modeName: string;
  /** 是否处于加载状态（流式响应等待中） */
  isLoading?: boolean;
  /** 是否正在流式接收内容 */
  isStreamingContent?: boolean;
  /** 图片附件 URL 列表 */
  attachments?: string[];
  knowledge?: SessionKnowledge | null;
}

/**
 * 消息项组件 - 使用 React.memo 优化渲染
 *
 * 自定义比较函数确保只有当消息内容实际变化时才重新渲染
 */
export const MessageItem = React.memo<MessageItemProps>(
  ({ role, content, modeName, isLoading, isStreamingContent, attachments, knowledge }) => {
    // 加载状态下显示的动画内容
    const loadingContent = (
      <div className="flex items-center space-x-2 py-1">
        <div className="flex space-x-1">
          <span className="w-2 h-2 rounded-full bg-linear-to-r from-secondary-400 to-purple-400 animate-pulse" />
          <span className="w-2 h-2 rounded-full bg-linear-to-r from-secondary-400 to-purple-400 animate-pulse [animation-delay:150ms]" />
          <span className="w-2 h-2 rounded-full bg-linear-to-r from-secondary-400 to-purple-400 animate-pulse [animation-delay:300ms]" />
        </div>
        <span className="text-sm text-surface-400 dark:text-surface-500">思考中...</span>
      </div>
    );

    // 判断是否显示加载状态
    const showLoading = isLoading && content === '';

    // 渲染图片附件
    const renderAttachments = () => {
      if (!attachments || attachments.length === 0) return null;
      const safeAttachments = attachments
        .map((url) => normalizeSafeImageAttachmentUrl(url))
        .filter((url): url is string => url !== null);
      if (safeAttachments.length === 0) return null;

      return (
        <div className="flex flex-wrap gap-2 mb-2">
          {safeAttachments.map((url, index) => (
            <a
              key={url}
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              className="block"
            >
              <img
                src={url}
                alt={`附件 ${index + 1}`}
                loading="lazy"
                className="max-w-48 max-h-48 rounded-lg border border-surface-200 dark:border-surface-600 hover:opacity-90 transition-opacity"
              />
            </a>
          ))}
        </div>
      );
    };

    return (
      <div
        className={cn(
          "flex w-full max-w-3xl mx-auto content-visibility-auto",
          role === 'student' ? "justify-end" : "justify-start"
        )}
      >
        <div className={cn(
            "flex min-w-0 max-w-[90%] sm:max-w-[80%]",
          role === 'student' ? "flex-row-reverse" : "flex-row"
        )}>
          {/* Avatar */}
          <div className={cn(
            "shrink-0 mt-1",
            role === 'student' ? "ml-3" : "mr-3"
          )}>
            {role === 'student' ? (
              <div className="h-9 w-9 rounded-xl bg-linear-to-br from-primary-500 to-primary-600 flex items-center justify-center text-white shadow-lg shadow-primary-500/25">
                <User className="w-5 h-5" />
              </div>
            ) : (
              <div className="h-9 w-9 rounded-xl bg-linear-to-br from-secondary-500 to-purple-500 flex items-center justify-center text-white shadow-lg shadow-secondary-500/25">
                <Bot className="w-5 h-5" />
              </div>
            )}
          </div>

          {/* Message Bubble */}
          <div className={cn(
            "min-w-0 wrap-anywhere rounded-2xl text-sm sm:text-base",
            role === 'student'
              ? "bg-linear-to-br from-primary-500 to-primary-600 text-white px-5 py-3.5 rounded-tr-md shadow-lg shadow-primary-500/20"
              : "bg-white dark:bg-surface-800 text-surface-800 dark:text-surface-200 border border-surface-200 dark:border-surface-700 px-5 py-4 rounded-tl-md shadow-sm"
          )}>
            {role === 'student' && renderAttachments()}
            {role === 'assistant' ? (
              showLoading ? loadingContent : (
                isStreamingContent
                  ? <StreamingMarkdownContent content={content} isStreaming={true} />
                  : <MarkdownContent content={content} />
              )
            ) : (
              <div className="whitespace-pre-wrap leading-relaxed">
                {content}
              </div>
            )}

            {role === 'assistant' && !showLoading && !isStreamingContent && <KnowledgeSources knowledge={knowledge} />}

            {/* Assistant Message Footer - 加载状态下不显示 */}
            {role === 'assistant' && !showLoading && (
              <div className="mt-4 pt-3 border-t border-surface-100 dark:border-surface-700 flex items-center justify-between">
                <div className="flex items-center space-x-1">
                  <button className="p-1.5 rounded-lg hover:bg-surface-100 dark:hover:bg-surface-700 text-surface-400 hover:text-green-600 dark:hover:text-green-400 transition-colors">
                    <ThumbsUp className="w-4 h-4" />
                  </button>
                  <button className="p-1.5 rounded-lg hover:bg-surface-100 dark:hover:bg-surface-700 text-surface-400 hover:text-red-600 dark:hover:text-red-400 transition-colors">
                    <ThumbsDown className="w-4 h-4" />
                  </button>
                </div>
                <div className="flex items-center text-xs text-surface-400 dark:text-surface-500">
                  <Sparkles className="w-3 h-3 mr-1 text-yellow-500" />
                  <span>MathAI · {modeName}</span>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    );
  },
  // 自定义比较函数：只比较关键属性
  (prevProps, nextProps) => {
    if (prevProps.id !== nextProps.id) return false;
    if (prevProps.content !== nextProps.content) return false;
    if (prevProps.role !== nextProps.role) return false;
    if (prevProps.modeName !== nextProps.modeName) return false;
    if (prevProps.isLoading !== nextProps.isLoading) return false;
    if (prevProps.isStreamingContent !== nextProps.isStreamingContent) return false;
    if (prevProps.knowledge !== nextProps.knowledge) return false;
    // attachments: 引用比较 + 逐项比较，避免 JSON.stringify 开销
    const prevAtt = prevProps.attachments;
    const nextAtt = nextProps.attachments;
    if (prevAtt === nextAtt) return true;
    if (!prevAtt || !nextAtt) return prevAtt === nextAtt;
    if (prevAtt.length !== nextAtt.length) return false;
    return prevAtt.every((url, i) => url === nextAtt[i]);
  }
);

MessageItem.displayName = 'MessageItem';
