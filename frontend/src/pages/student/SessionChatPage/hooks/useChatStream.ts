import { useCallback, useEffect, useRef } from 'react';
import { useAppDispatch } from '@/store';
import {
  addMessage,
  appendToLastMessage,
  removeMessagesById,
  setCurrentTaskId,
  setSendingState,
  setStreamStatus,
  setStreamingMessageId,
  setMessageKnowledge,
  type StreamStatus,
} from '@/modules/session/store/sessionSlice';
import { sessionService } from '@/modules/session/services/sessionService';
import { uploadService, type UploadResponse } from '@/modules/upload/services/uploadService';
import type { ParsedDocument } from '@/libs/utils/documentParser';
import {
  formatChatMessageForDisplay,
  formatDocumentsForChat,
} from '@/modules/session/documentMessage';
import type { SSEController, SSEError, SSEHandlers } from '@/libs/http/sseClient';
import {
  isRequestCancelled,
  toAppError,
  type AppError,
} from '@/libs/http/appError';
import {
  MAX_CHAT_DOCUMENTS,
  MAX_CHAT_IMAGES,
  MAX_CHAT_MESSAGE_BYTES,
  MAX_CHAT_MESSAGE_KIB,
} from '@/modules/session/limits';
import type { DraftSessionIdentity, SessionMode } from '@/modules/session/types';
import { normalizeSessionKnowledge } from '@/modules/session/knowledge';

const CANCEL_EVENT_FALLBACK_MS = 1500;

export type ChatTarget =
  | { kind: 'existing'; sessionId: string }
  | {
      kind: 'draft';
      sessionId?: string;
      topic?: string;
      mode: SessionMode;
    };

interface UseChatStreamProps {
  resolveChatTarget: () => ChatTarget | null;
  isStreaming: boolean;
  attachmentsPending: boolean;
  selectedImages: File[];
  sseControllerRef: React.MutableRefObject<SSEController | null>;
  onRequestAccepted?: (sentInputText: string) => void;
  onSessionPrepared?: (identity: DraftSessionIdentity) => void;
  onSessionMaterialized?: (sessionId: string) => void;
  onFirstTurnCompleted?: (sessionId: string) => void;
  onChatSettled?: (settlement: ChatSettlement) => void;
  onClearImages: () => void;
  /** 获取已解析的文档列表 */
  getParsedDocuments: () => ParsedDocument[];
  /** 清空文件 */
  onClearFiles: () => void;
}

export type ChatSendOutcome = 'done' | 'error' | 'cancelled' | 'closed';

export interface ChatSettlement {
  sessionId: string | null;
  outcome: ChatSendOutcome;
  requestStarted: boolean;
  requestAccepted: boolean;
  error?: AppError;
  isFirstTurn: boolean;
}

type FinishSend = (
  outcome: ChatSendOutcome,
  nextStreamStatus: StreamStatus,
  appendedMessage?: string,
  error?: AppError
) => void;

export const useChatStream = ({
  resolveChatTarget,
  isStreaming,
  attachmentsPending,
  selectedImages,
  sseControllerRef,
  onRequestAccepted,
  onSessionPrepared,
  onSessionMaterialized,
  onFirstTurnCompleted,
  onChatSettled,
  onClearImages,
  getParsedDocuments,
  onClearFiles,
}: UseChatStreamProps) => {
  const dispatch = useAppDispatch();

  // 流式更新：rAF 节流相关 refs
  const contentBufferRef = useRef<string>('');
  const rafIdRef = useRef<number | null>(null);
  const sendPendingRef = useRef(false);
  const sendAbortControllerRef = useRef<AbortController | null>(null);
  const activeFinishRef = useRef<FinishSend | null>(null);
  const activeCancelRef = useRef<(() => Promise<boolean>) | null>(null);
  const cancelPendingRef = useRef(false);
  const cancelFallbackTimerRef = useRef<number | null>(null);
  const stopRequestedRef = useRef(false);
  const activeRef = useRef(true);
  const uploadedImageCacheRef = useRef<WeakMap<File, UploadResponse>>(new WeakMap());

  // 取消待执行的 rAF 刷新
  const cancelPendingFlush = useCallback(() => {
    if (rafIdRef.current !== null) {
      cancelAnimationFrame(rafIdRef.current);
      rafIdRef.current = null;
    }
  }, []);

  const clearCancelFallback = useCallback(() => {
    if (cancelFallbackTimerRef.current !== null) {
      window.clearTimeout(cancelFallbackTimerRef.current);
      cancelFallbackTimerRef.current = null;
    }
  }, []);

  // 刷新缓冲区中的剩余内容到 Redux
  const flushBuffer = useCallback(() => {
    cancelPendingFlush();
    if (contentBufferRef.current) {
      dispatch(appendToLastMessage(contentBufferRef.current));
      contentBufferRef.current = '';
    }
  }, [dispatch, cancelPendingFlush]);

  useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
      activeFinishRef.current = null;
      activeCancelRef.current = null;
      sendAbortControllerRef.current?.abort();
      sendAbortControllerRef.current = null;
      sendPendingRef.current = false;
      cancelPendingRef.current = false;
      stopRequestedRef.current = false;
      clearCancelFallback();
      sseControllerRef.current?.close();
      sseControllerRef.current = null;
      cancelPendingFlush();
      contentBufferRef.current = '';
      dispatch(setSendingState('idle'));
      dispatch(setStreamStatus('idle'));
      dispatch(setStreamingMessageId(null));
      dispatch(setCurrentTaskId(null));
    };
  }, [cancelPendingFlush, clearCancelFallback, dispatch, sseControllerRef]);

  // 发送消息
  const handleSendMessage = useCallback(
    async (messageContent: string): Promise<boolean> => {
      const parsedDocs = [...getParsedDocuments()];
      const imageSnapshot = [...selectedImages];
      if (
        isStreaming ||
        attachmentsPending ||
        sendPendingRef.current
      ) {
        return false;
      }

      const target = resolveChatTarget();
      if (!target) return false;
      const sentInputText = messageContent;
      if (
        !messageContent.trim() &&
        imageSnapshot.length === 0 &&
        parsedDocs.length === 0
      ) {
        return false;
      }

      sendPendingRef.current = true;
      const abortController = new AbortController();
      sendAbortControllerRef.current = abortController;
      dispatch(setSendingState('loading'));

      const releaseSend = () => {
        if (sendAbortControllerRef.current !== abortController) return;
        sendAbortControllerRef.current = null;
        sendPendingRef.current = false;
        dispatch(setSendingState('idle'));
      };
      const isAborted = () => abortController.signal.aborted || !activeRef.current;
      let recoverySessionId = target.kind === 'existing'
        ? target.sessionId
        : target.sessionId ?? crypto.randomUUID();
      let optimisticMessageIds: string[] = [];
      let requestStarted = false;
      let requestAccepted = false;
      let hasVisibleResponseContent = false;
      let stopConfirmed = false;
      let pendingStreamFailure: (() => void) | null = null;
      let settled = false;
      stopRequestedRef.current = false;
      clearCancelFallback();

      if (target.kind === 'draft') {
        onSessionPrepared?.({
          sessionId: recoverySessionId,
          topic: target.topic,
          mode: target.mode,
        });
      }

      const markRequestAccepted = () => {
        if (settled || requestAccepted || !activeRef.current) return;
        requestAccepted = true;
        if (target.kind === 'draft') onSessionMaterialized?.(recoverySessionId);
        onRequestAccepted?.(sentInputText);
        uploadedImageCacheRef.current = new WeakMap();
        onClearImages();
        onClearFiles();
      };
      const terminalNote = (message: string) =>
        `${hasVisibleResponseContent ? '\n\n' : ''}> ${message}`;

      const finishSend: FinishSend = (
        outcome: ChatSendOutcome,
        nextStreamStatus: StreamStatus,
        appendedMessage?: string,
        error?: AppError
      ) => {
        if (settled || activeFinishRef.current !== finishSend) return;
        settled = true;
        clearCancelFallback();
        pendingStreamFailure = null;
        activeFinishRef.current = null;
        activeCancelRef.current = null;
        stopRequestedRef.current = false;
        if (outcome !== 'done') abortController.abort();
        if (activeRef.current) {
          flushBuffer();
          if (requestAccepted && appendedMessage && optimisticMessageIds.length > 0) {
            dispatch(appendToLastMessage(appendedMessage));
          }
          if (!requestAccepted && outcome !== 'done' && optimisticMessageIds.length > 0) {
            dispatch(removeMessagesById(optimisticMessageIds));
          }
        } else {
          cancelPendingFlush();
          contentBufferRef.current = '';
        }
        releaseSend();
        dispatch(setStreamStatus(nextStreamStatus));
        dispatch(setStreamingMessageId(null));
        dispatch(setCurrentTaskId(null));
        if (activeRef.current && requestAccepted) {
          onFirstTurnCompleted?.(recoverySessionId);
        }
        const streamController = sseControllerRef.current;
        sseControllerRef.current = null;
        if (outcome !== 'closed') streamController?.close();
        if (activeRef.current) {
          onChatSettled?.({
            sessionId: recoverySessionId,
            outcome,
            requestStarted,
            requestAccepted,
            error,
            isFirstTurn: target.kind === 'draft',
          });
        }
      };
      activeFinishRef.current = finishSend;

      const finishCancelled = () => {
        finishSend(
          'cancelled',
          'cancelled',
          terminalNote('已停止生成'),
          toAppError({ code: 'CANCELLED', message: '已停止生成', source: 'ui' })
        );
      };

      const requestCancellation = async (): Promise<boolean> => {
        if (settled || cancelPendingRef.current) return true;

        const streamController = sseControllerRef.current;
        if (!streamController) {
          finishSend(
            'cancelled',
            'cancelled',
            undefined,
            toAppError({ code: 'CANCELLED', message: '已取消发送', source: 'ui' })
          );
          return true;
        }

        const taskId = streamController.getTaskId();
        if (!taskId) {
          // task_info arrives asynchronously; keep the SSE open so its task ID
          // can be cancelled on the server instead of only stopping locally.
          stopRequestedRef.current = true;
          return true;
        }

        stopRequestedRef.current = true;
        cancelPendingRef.current = true;
        try {
          const cancelled = await sessionService.cancelTask(taskId);
          if (!cancelled) {
            // The task may have completed between the SSE event and this request.
            // Treat that response as a successful local stop; the stream is no
            // longer useful to the user and should not produce a false error toast.
            stopConfirmed = true;
            if (pendingStreamFailure) {
              pendingStreamFailure = null;
              finishCancelled();
            } else if (!settled && activeFinishRef.current === finishSend) {
              clearCancelFallback();
              finishCancelled();
            }
            return true;
          }
          stopConfirmed = true;
          if (pendingStreamFailure) {
            pendingStreamFailure = null;
            finishCancelled();
            return true;
          }
          if (!settled && activeFinishRef.current === finishSend) {
            clearCancelFallback();
            cancelFallbackTimerRef.current = window.setTimeout(() => {
              cancelFallbackTimerRef.current = null;
              finishCancelled();
            }, CANCEL_EVENT_FALLBACK_MS);
          }
          return true;
        } catch (error) {
          const appError = toAppError(error, '取消任务失败');
          if (isRequestCancelled(appError)) {
            stopRequestedRef.current = false;
            return false;
          }
          stopRequestedRef.current = false;
          pendingStreamFailure = null;
          if (!settled && activeFinishRef.current === finishSend && activeRef.current) {
            // A failed cancel request must not leave the local stream in a
            // permanent "generating" state. Preserve the structured error in
            // the settlement so the page can show one standardized notice.
            finishSend(
              'error',
              'error',
              terminalNote('生成已中断'),
              appError
            );
          }
          return false;
        } finally {
          cancelPendingRef.current = false;
        }
      };
      activeCancelRef.current = requestCancellation;

      try {
        if (imageSnapshot.length > MAX_CHAT_IMAGES) {
          throw new Error(`每次最多上传 ${MAX_CHAT_IMAGES} 张图片`);
        }
        if (parsedDocs.length > MAX_CHAT_DOCUMENTS) {
          throw new Error(`每次最多上传 ${MAX_CHAT_DOCUMENTS} 个文档`);
        }

        const promptMessage = messageContent.trim()
          ? messageContent
          : parsedDocs.length > 0
            ? '请分析我上传的文档。'
            : '请分析我上传的图片。';
        const fullMessage = formatDocumentsForChat(parsedDocs, promptMessage);
        if (new TextEncoder().encode(fullMessage).byteLength > MAX_CHAT_MESSAGE_BYTES) {
          throw new Error(`消息和文档内容合计不能超过 ${MAX_CHAT_MESSAGE_KIB} KiB`);
        }

        const uploadPromises = imageSnapshot.map(async (file) => {
          const cached = uploadedImageCacheRef.current.get(file);
          if (cached) return cached;

          const uploaded = await uploadService.uploadImage(
            file,
            undefined,
            abortController.signal
          );
          uploadedImageCacheRef.current.set(file, uploaded);
          return uploaded;
        });
        let uploadedImageUrls: string[] = [];
        if (uploadPromises.length > 0) {
          const results = await Promise.all(uploadPromises);
          uploadedImageUrls = results.map((result) => result.url);
        }
        if (isAborted()) return false;

        const optimisticSessionId = recoverySessionId;
        const displayMessage = formatChatMessageForDisplay(fullMessage);

        const userMessageId = crypto.randomUUID();
        const aiMessageId = crypto.randomUUID();
        optimisticMessageIds = [userMessageId, aiMessageId];

        // 1. 添加用户消息到 UI
        dispatch(
          addMessage({
            id: userMessageId,
            sessionId: optimisticSessionId,
            role: 'user',
            content: displayMessage,
            timestamp: new Date().toISOString(),
            attachments: uploadedImageUrls,
          })
        );

        // 2. 创建 AI 消息占位
        dispatch(
          addMessage({
            id: aiMessageId,
            sessionId: optimisticSessionId,
            role: 'assistant',
            content: '',
            timestamp: new Date().toISOString(),
            metadata: { agent: null },
          })
        );

        // 3. 设置流式状态
        dispatch(setStreamStatus('streaming'));
        dispatch(setStreamingMessageId(aiMessageId));

        const streamHandlers: SSEHandlers = {
          onTaskInfo: (taskId: string) => {
            if (settled || !activeRef.current) return;
            dispatch(setCurrentTaskId(taskId));
            markRequestAccepted();
            if (stopRequestedRef.current) void requestCancellation();
          },
          onChunk: (content: string) => {
            if (settled || !activeRef.current) return;
            markRequestAccepted();
            if (content.trim()) hasVisibleResponseContent = true;
            // rAF 节流：缓冲内容，每帧最多 dispatch 一次
            contentBufferRef.current += content;
            if (rafIdRef.current === null) {
              rafIdRef.current = requestAnimationFrame(() => {
                if (contentBufferRef.current) {
                  dispatch(appendToLastMessage(contentBufferRef.current));
                  contentBufferRef.current = '';
                }
                rafIdRef.current = null;
              });
            }
          },
          onDone: (_messageId, _agent, knowledge) => {
            if (settled || !activeRef.current) return;
            dispatch(setMessageKnowledge({ id: aiMessageId, knowledge: normalizeSessionKnowledge(knowledge) }));
            markRequestAccepted();
            finishSend('done', 'idle');
          },
          onError: (error: SSEError) => {
            if (settled) return;
            console.error('SSE error:', error);
            if (stopConfirmed) {
              finishCancelled();
              return;
            }
            const finishError = () => finishSend(
                'error',
                'error',
                terminalNote('生成已中断'),
                error
              );
            if (cancelPendingRef.current && stopRequestedRef.current) {
              pendingStreamFailure = finishError;
              return;
            }
            finishError();
          },
          onCancelled: () => {
            markRequestAccepted();
            finishCancelled();
          },
          onClose: () => {
            if (stopConfirmed) {
              finishCancelled();
              return;
            }
            const finishClosed = () => finishSend(
                'closed',
                'error',
                terminalNote('生成已中断'),
                toAppError({
                  code: 'CONNECTION_ERROR',
                  message: '连接中断，请检查网络后重试',
                  source: 'sse',
                })
              );
            if (cancelPendingRef.current && stopRequestedRef.current) {
              pendingStreamFailure ??= finishClosed;
              return;
            }
            finishClosed();
          },
        };

        // 草稿通过一个原子接口创建；已有会话继续使用原聊天接口。
        if (target.kind === 'draft') {
          requestStarted = true;
          sseControllerRef.current = sessionService.startChatStream(
            {
              sessionId: recoverySessionId,
              topic: target.topic,
              mode: target.mode,
              message: fullMessage,
              attachments: uploadedImageUrls.length > 0 ? uploadedImageUrls : undefined,
            },
            {
              ...streamHandlers,
              onSessionInfo: (materializedSessionId: string) => {
                if (settled || !activeRef.current) return;
                recoverySessionId = materializedSessionId;
                if (requestAccepted) onSessionMaterialized?.(materializedSessionId);
                markRequestAccepted();
              },
            }
          );
        } else {
          requestStarted = true;
          sseControllerRef.current = sessionService.chatStream(
            target.sessionId,
            fullMessage,
            streamHandlers,
            uploadedImageUrls.length > 0 ? uploadedImageUrls : undefined
          );
        }
        return true;
      } catch (error) {
        if (settled || isAborted()) return false;
        console.error('消息发送准备失败:', error);
        finishSend(
          'error',
          'error',
          undefined,
          toAppError(error, '消息发送失败')
        );
        return false;
      }
    },
    [
      resolveChatTarget,
      isStreaming,
      attachmentsPending,
      selectedImages,
      sseControllerRef,
      onRequestAccepted,
      onSessionPrepared,
      onSessionMaterialized,
      onFirstTurnCompleted,
      onChatSettled,
      onClearImages,
      onClearFiles,
      getParsedDocuments,
      dispatch,
      flushBuffer,
      cancelPendingFlush,
      clearCancelFallback,
    ]
  );

  const cancelCurrentSend = useCallback(async (): Promise<boolean> => {
    return activeCancelRef.current?.() ?? false;
  }, []);

  return {
    handleSendMessage,
    cancelCurrentSend,
  };
};
