import React from 'react';
import { MessageItem } from '../../../../components/chat/MessageItem';
import { Loader2 } from 'lucide-react';
import type { SessionMessage } from '../../../../types';
import { RequestErrorNotice } from '@/components/feedback';
import type { AppError } from '@/libs/http/appError';

interface ChatMessagesProps {
  messages: SessionMessage[];
  draftWelcome?: string;
  draftModeName?: string;
  streamingMessageId: string | null;
  isLoading: boolean;
  error: AppError | null;
  onRetry?: () => void;
  messagesContainerRef: React.RefObject<HTMLDivElement | null>;
}

export const ChatMessages = React.memo<ChatMessagesProps>(
  ({ messages, draftWelcome, draftModeName, streamingMessageId, isLoading, error, onRetry, messagesContainerRef }) => {
    return (
      <div
        ref={messagesContainerRef}
        className="min-w-0 flex-1 overflow-y-auto scroll-optimized px-3 py-4 space-y-4 sm:px-6"
      >
        {isLoading && messages.length === 0 ? (
          <div className="flex items-center justify-center h-full">
            <div className="flex items-center gap-2 text-surface-500">
              <Loader2 className="w-5 h-5 animate-spin" />
              <span>加载中...</span>
            </div>
          </div>
        ) : error ? (
          <div className="flex items-center justify-center h-full px-4">
            <RequestErrorNotice error={error} onRetry={onRetry} onRefresh={onRetry} className="w-full max-w-xl" />
          </div>
        ) : draftWelcome && messages.length === 0 ? (
          <MessageItem
            id="draft-welcome"
            role="assistant"
            content={draftWelcome}
            modeName={draftModeName ?? 'AI 助手'}
          />
        ) : (
          messages.map((message) => (
            <MessageItem
              key={message.id}
              id={message.id}
              role={message.role === 'user' ? 'student' : message.role}
              content={message.content}
              modeName="AI 助手"
              isLoading={message.id === streamingMessageId && message.content === ''}
              isStreamingContent={message.id === streamingMessageId && message.content !== ''}
              attachments={message.attachments}
              knowledge={message.knowledge}
            />
          ))
        )}
      </div>
    );
  }
);

ChatMessages.displayName = 'ChatMessages';
