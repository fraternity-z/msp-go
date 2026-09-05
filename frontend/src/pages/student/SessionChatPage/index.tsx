import React, { useState, useRef, useEffect, useCallback } from 'react';
import { useParams, useNavigate, useLocation } from 'react-router-dom';
import { MainLayout } from '../../../components/layout/MainLayout';
import { useAppDispatch, useAppSelector } from '../../../store';
import {
  fetchHistoryAsync,
  reconcileHistoryAsync,
  fetchSessionsAsync,
  deleteSessionAsync,
  batchDeleteSessionsAsync,
  updateSessionModeAsync,
  setCurrentSession,
  setMode,
  clearCurrentSession,
  invalidateSession,
  prepareDraftSession,
  materializeDraftSession,
  completeDraftFirstTurn,
  selectCurrentSession,
  selectMessages,
  selectMode,
  selectDraftSessionId,
  selectDraftSessionTopic,
  selectDraftSessionMode,
  selectDraftSessionMaterialized,
  selectDraftFirstTurnCompleted,
  selectStreamStatus,
  selectStreamingMessageId,
  selectSessionLoadingState,
  selectSessionSendingState,
  selectSessionError,
  selectHistorySessionId,
  selectHistorySessionStatus,
  selectReconcileState,
  selectSessions,
  selectSessionsLoadingState,
  selectSessionsError,
  selectModeUpdateState,
} from '@/modules/session/store/sessionSlice';
import type {
  ChatMode,
  DraftSessionIdentity,
} from '@/modules/session/types';
import type { SSEController } from '../../../libs/http/sseClient';
import { ChatHeader } from './components/ChatHeader';
import { ChatSidebar } from './components/ChatSidebar';
import { ChatMessages } from './components/ChatMessages';
import { ChatInput } from './components/ChatInput';
import { ModeSelector } from './components/ModeSelector';
import { QuickActions } from './components/QuickActions';
import {
  useChatStream,
  type ChatSettlement,
  type ChatTarget,
} from './hooks/useChatStream';
import { useImageUpload } from './hooks/useImageUpload';
import { useFileUpload } from './hooks/useFileUpload';
import { CHAT_MODES, QUICK_ACTIONS } from './constants.tsx';
import type { ExerciseTutorLaunchState } from '../exerciseTutorLaunch';
import { useToast, type ToastOptions } from '@/components/ui/Toast';
import {
  isDefinitiveDraftIdentityError,
  isSessionNotFoundError,
} from '@/modules/session/errors';
import {
  formatAppErrorDescription,
  toAppError,
  toAppErrorFeedback,
} from '@/libs/http/appError';

function formatChatErrorDescription(
  message: string,
  error: ChatSettlement['error']
): string {
  if (error) {
    return formatAppErrorDescription({ ...error, message: message || error.message });
  }
  return message;
}

function showSessionRequestError(
  toast: (options: ToastOptions) => string,
  error: unknown,
  fallback: string,
  onRefresh?: () => void,
): void {
  const appError = toAppError(error, fallback);
  const feedback = toAppErrorFeedback(appError, fallback);
  if (!feedback) return;
  toast({
    ...feedback,
    ...(appError.kind === 'conflict' && onRefresh
      ? { action: { label: '刷新数据', onClick: onRefresh } }
      : {}),
  });
}

export const SessionChatPage: React.FC = () => {
  const { sessionId } = useParams<{ sessionId?: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const dispatch = useAppDispatch();
  const { toast } = useToast();

  // 从刷题页面跳转时携带的初始消息
  const locationState = location.state as Partial<ExerciseTutorLaunchState> | null;
  const initialMessageHandled = useRef(false);

  // Redux state
  const currentSession = useAppSelector(selectCurrentSession);
  const messages = useAppSelector(selectMessages);
  const currentMode = useAppSelector(selectMode);
  const draftSessionId = useAppSelector(selectDraftSessionId);
  const draftSessionTopic = useAppSelector(selectDraftSessionTopic);
  const draftSessionMode = useAppSelector(selectDraftSessionMode);
  const draftSessionMaterialized = useAppSelector(selectDraftSessionMaterialized);
  const draftFirstTurnCompleted = useAppSelector(selectDraftFirstTurnCompleted);
  const streamStatus = useAppSelector(selectStreamStatus);
  const streamingMessageId = useAppSelector(selectStreamingMessageId);
  const loadingState = useAppSelector(selectSessionLoadingState);
  const sendingState = useAppSelector(selectSessionSendingState);
  const error = useAppSelector(selectSessionError);
  const historySessionId = useAppSelector(selectHistorySessionId);
  const historySessionStatus = useAppSelector(selectHistorySessionStatus);
  const reconcileState = useAppSelector(selectReconcileState);
  const sessions = useAppSelector(selectSessions);
  const sessionsLoadingState = useAppSelector(selectSessionsLoadingState);
  const sessionsError = useAppSelector(selectSessionsError);
  const modeUpdateState = useAppSelector(selectModeUpdateState);

  // Local state
  const [inputValue, setInputValue] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(() => window.matchMedia('(min-width: 768px)').matches);
  const [deletingSessionId, setDeletingSessionId] = useState<string | null>(null);
  const [isSelectMode, setIsSelectMode] = useState(false);
  const [selectedSessionIds, setSelectedSessionIds] = useState<string[]>([]);
  const [isBatchDeleting, setIsBatchDeleting] = useState(false);
  const inputValueRef = useRef('');
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const sseControllerRef = useRef<SSEController | null>(null);
  const scrollRafRef = useRef<number | null>(null);
  const settlementAttemptRef = useRef(0);
  const pageActiveRef = useRef(true);
  const composerHasContentRef = useRef(false);

  const isDraftSession = !sessionId || sessionId === 'new';
  const hasPreparedDraft = isDraftSession && draftSessionId !== null;
  const hasCompletedDraft = hasPreparedDraft && draftFirstTurnCompleted;
  const draftRecoveryPending = hasPreparedDraft && !draftFirstTurnCompleted;
  const showDraftWelcome = isDraftSession
    && messages.length === 0;
  const currentModeConfig = CHAT_MODES.find((m) => m.id === currentMode)!;
  const isStreaming = streamStatus === 'streaming';
  const isLoading = loadingState === 'loading';
  const isSending = sendingState === 'loading';
  const isBusy = isSending || isStreaming;
  const isModeUpdating = modeUpdateState === 'loading';
  const isReconciling = reconcileState === 'loading';
  const isDeleting = deletingSessionId !== null || isBatchDeleting;
  const interactionBusy = isBusy || isModeUpdating || isReconciling || isDeleting;
  const composerDisabled = isLoading || isModeUpdating || isReconciling || isDeleting;
  const persistedSessionReady = !isDraftSession
    && historySessionId === sessionId
    && historySessionStatus === 'active';
  const activeSessionId = isDraftSession
    ? hasCompletedDraft ? draftSessionId : null
    : persistedSessionReady
      ? sessionId ?? null
      : null;
  const draftWelcome = `你好，当前为${currentModeConfig.name}。${currentModeConfig.description}。输入问题后将开始并保存本次对话。`;

  const handleAttachmentError = useCallback((message: string) => {
    toast({
      type: 'error',
      title: '附件无法添加',
      description: message,
    });
  }, [toast]);

  // 自定义 hooks
  const { selectedImages, previewUrls, handleImageSelect, handleRemoveImage, clearImages } =
    useImageUpload({ onError: handleAttachmentError });

  const {
    files: uploadedFiles,
    isParsing: isFileParsing,
    handleFileSelect,
    handleRemoveFile,
    clearFiles,
    getParsedDocuments,
  } = useFileUpload({ onError: handleAttachmentError });
  composerHasContentRef.current = inputValue.length > 0
    || selectedImages.length > 0
    || uploadedFiles.length > 0;

  const resolveChatTarget = useCallback((): ChatTarget | null => {
    if (!isDraftSession) {
      return sessionId
        && historySessionId === sessionId
        && historySessionStatus === 'active'
        ? { kind: 'existing', sessionId }
        : null;
    }
    if (draftSessionId && draftSessionMaterialized) {
      return { kind: 'existing', sessionId: draftSessionId };
    }

    if (draftSessionId) {
      return {
        kind: 'draft',
        sessionId: draftSessionId,
        topic: draftSessionTopic ?? undefined,
        mode: draftSessionMode ?? currentMode,
      };
    }

    return {
      kind: 'draft',
      topic: locationState?.topic,
      mode: locationState?.mode ?? currentMode,
    };
  }, [currentMode, draftSessionId, draftSessionMaterialized, draftSessionMode, draftSessionTopic, historySessionId, historySessionStatus, isDraftSession, locationState, sessionId]);

  const handleSessionPrepared = useCallback((identity: DraftSessionIdentity) => {
    dispatch(prepareDraftSession(identity));
  }, [dispatch]);

  const handleSessionMaterialized = useCallback((materializedSessionId: string) => {
    dispatch(materializeDraftSession(materializedSessionId));
  }, [dispatch]);

  const handleFirstTurnCompleted = useCallback((completedSessionId: string) => {
    dispatch(completeDraftFirstTurn(completedSessionId));
  }, [dispatch]);

  const handleRequestAccepted = useCallback((sentInputText: string) => {
    settlementAttemptRef.current += 1;
    const currentValue = inputValueRef.current;
    const nextValue = currentValue === sentInputText ? '' : currentValue;
    inputValueRef.current = nextValue;
    composerHasContentRef.current = nextValue.length > 0;
    if (nextValue !== currentValue) setInputValue(nextValue);
  }, []);

  const refreshSessionList = useCallback(() => {
    void dispatch(fetchSessionsAsync({ force: true }));
  }, [dispatch]);

  const handleInputChange = useCallback((value: string) => {
    inputValueRef.current = value;
    setInputValue(value);
  }, []);

  const recoverMissingSession = useCallback((missingSessionId: string) => {
    settlementAttemptRef.current += 1;
    initialMessageHandled.current = false;
    dispatch(invalidateSession(missingSessionId));
    navigate('/session/new', { replace: true });
    refreshSessionList();
  }, [dispatch, navigate, refreshSessionList]);

  const discardDraftRecovery = useCallback((discardedSessionId: string) => {
    settlementAttemptRef.current += 1;
    dispatch(invalidateSession(discardedSessionId));
    refreshSessionList();
  }, [dispatch, refreshSessionList]);

  const handleChatSettled = useCallback((settlement: ChatSettlement) => {
    const {
      sessionId: settledSessionId,
      outcome,
      requestStarted,
      requestAccepted,
      error: settlementError,
      isFirstTurn,
    } = settlement;
    const settlementAttempt = ++settlementAttemptRef.current;
    if (outcome === 'done') {
      if (!settledSessionId) return;
      refreshSessionList();
      if (isDraftSession && !composerHasContentRef.current) {
        navigate(`/session/${settledSessionId}`, { replace: true });
      }
      return;
    }

    if (
      isFirstTurn &&
      settledSessionId &&
      isDefinitiveDraftIdentityError(settlementError)
    ) {
      toast({
        type: 'error',
        title: '首次会话无法继续',
        description: formatChatErrorDescription(
          settlementError?.message ?? '旧草稿已结束，输入和附件已保留，请重新发送',
          settlementError
        ),
      });
      discardDraftRecovery(settledSessionId);
      return;
    }
    if (
      !isFirstTurn &&
      settledSessionId &&
      isSessionNotFoundError(settlementError)
    ) {
      toast({
        type: 'error',
        title: '会话已失效',
        description: formatChatErrorDescription(
          '原会话已不存在，输入和附件已保留',
          settlementError
        ),
      });
      recoverMissingSession(settledSessionId);
      return;
    }
    if (outcome === 'cancelled') {
      toast({
        type: 'info',
        title: '已停止生成',
        description: formatChatErrorDescription(
          settlementError?.message ?? '已保留当前生成内容，可以继续输入新问题',
          settlementError,
        ),
      });
    } else {
      const feedback = toAppErrorFeedback(
        settlementError,
        requestAccepted ? '生成未完成，已保留当前内容' : '消息发送未完成，请再次发送',
      );
      if (feedback) {
        toast({
          ...feedback,
          ...(settlementError?.kind === 'conflict'
            ? { action: { label: '刷新数据', onClick: refreshSessionList } }
            : {}),
        });
      }
    }

    if (requestAccepted) {
      if (settledSessionId) refreshSessionList();
      return;
    }
    if (!settledSessionId || !requestStarted) return;

    // 请求已经发出但接收事件可能丢失时，只读探测稳定会话 ID。
    // 已确认接收的中断保留本地分片，不在这里用历史立即覆盖。
    void dispatch(reconcileHistoryAsync({
      sessionId: settledSessionId,
      preserveDraftOnNotFound: isFirstTurn,
    }))
      .then((result) => {
        if (
          !pageActiveRef.current ||
          settlementAttemptRef.current !== settlementAttempt
        ) {
          return;
        }

        if (reconcileHistoryAsync.fulfilled.match(result)) {
          if (
            isFirstTurn &&
            result.payload.firstTurnCompleted &&
            !composerHasContentRef.current
          ) {
            navigate(`/session/${settledSessionId}`, { replace: true });
          }
          return;
        }

        if (reconcileHistoryAsync.rejected.match(result) && !result.meta.condition) {
          if (isSessionNotFoundError(result.payload)) {
            if (isFirstTurn) {
              discardDraftRecovery(settledSessionId);
            } else {
              recoverMissingSession(settledSessionId);
            }
            return;
          }
          toast({
            type: 'error',
            title: '历史同步失败',
            description: result.payload?.message ?? '当前消息已保留，请稍后重试',
          });
        }
      });
  }, [discardDraftRecovery, dispatch, isDraftSession, navigate, recoverMissingSession, refreshSessionList, toast]);

  const {
    handleSendMessage: sendMessage,
    cancelCurrentSend,
  } = useChatStream({
    resolveChatTarget,
    isStreaming,
    attachmentsPending: isFileParsing,
    selectedImages,
    sseControllerRef,
    onRequestAccepted: handleRequestAccepted,
    onSessionPrepared: handleSessionPrepared,
    onSessionMaterialized: handleSessionMaterialized,
    onFirstTurnCompleted: handleFirstTurnCompleted,
    onChatSettled: handleChatSettled,
    onClearImages: clearImages,
    getParsedDocuments,
    onClearFiles: clearFiles,
  });

  // 加载历史会话列表
  useEffect(() => {
    dispatch(fetchSessionsAsync({}));
  }, [dispatch]);

  // 滚动到底部 — 流式时即时滚动，非流式时 smooth 动画
  const scrollToBottom = useCallback((smooth = false) => {
    if (scrollRafRef.current !== null) {
      cancelAnimationFrame(scrollRafRef.current);
    }
    scrollRafRef.current = requestAnimationFrame(() => {
      scrollRafRef.current = null;
      const el = messagesContainerRef.current;
      if (!el) return;
      if (smooth) {
        el.scrollTo({
          top: el.scrollHeight - el.clientHeight,
          behavior: 'smooth',
        });
      } else {
        el.scrollTop = el.scrollHeight - el.clientHeight;
      }
    });
  }, []);

  // 消息变化时滚动 — 流式过程用即时滚动避免动画排队
  useEffect(() => {
    scrollToBottom(streamStatus !== 'streaming');
  }, [messages, scrollToBottom, streamStatus]);

  // 路由变化时加载正式会话历史。
  useEffect(() => {
    if (!sessionId || sessionId === 'new') return;
    const loadAttempt = ++settlementAttemptRef.current;
    void dispatch(fetchHistoryAsync({ sessionId })).then((result) => {
      if (
        !pageActiveRef.current ||
        settlementAttemptRef.current !== loadAttempt ||
        !fetchHistoryAsync.rejected.match(result) ||
        !isSessionNotFoundError(result.payload)
      ) {
        return;
      }
      recoverMissingSession(sessionId);
    });
  }, [dispatch, recoverMissingSession, sessionId]);

  // 会话列表可能晚于历史返回，单独同步侧栏中的当前会话对象。
  useEffect(() => {
    if (!sessionId || sessionId === 'new' || currentSession?.id === sessionId) return;
    const existingSession = sessions.find((session) => session.id === sessionId);
    if (existingSession) {
      dispatch(setCurrentSession(existingSession));
    } else if (currentSession?.id) {
      dispatch(setCurrentSession(null));
    }
  }, [currentSession?.id, dispatch, sessionId, sessions]);

  // 新会话仅保留为前端草稿。
  useEffect(() => {
    if (sessionId && sessionId !== 'new') return;
    settlementAttemptRef.current += 1;
    initialMessageHandled.current = false;
    dispatch(clearCurrentSession());
    dispatch(setMode(locationState?.mode ?? currentMode));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // 清理 SSE 连接和 rAF
  useEffect(() => {
    pageActiveRef.current = true;
    return () => {
      pageActiveRef.current = false;
      settlementAttemptRef.current += 1;
      if (scrollRafRef.current !== null) {
        cancelAnimationFrame(scrollRafRef.current);
      }
    };
  }, []);

  // 发送消息
  const handleSendMessage = useCallback(
    async (customMessage?: string) => {
      if (isModeUpdating || isReconciling) return false;
      const messageContent = customMessage ?? inputValue;
      return sendMessage(messageContent);
    },
    [inputValue, isModeUpdating, isReconciling, sendMessage]
  );

  // 从刷题页面跳转时，自动发送初始消息
  useEffect(() => {
    if (
      isDraftSession &&
      locationState?.initialMessage &&
      !initialMessageHandled.current &&
      !interactionBusy
    ) {
      const initialMessage = locationState.initialMessage;
      // 延迟到当前 effect 稳定后发送，避免开发环境 StrictMode 回放取消首次请求。
      const sendTimer = window.setTimeout(() => {
        initialMessageHandled.current = true;
        navigate(location.pathname, { replace: true, state: null });
        void handleSendMessage(initialMessage).then((started) => {
          if (!started && pageActiveRef.current) {
            inputValueRef.current = initialMessage;
            setInputValue(initialMessage);
          }
        });
      }, 0);
      return () => window.clearTimeout(sendTimer);
    }
  }, [handleSendMessage, interactionBusy, isDraftSession, location.pathname, locationState, navigate]);

  // 取消响应
  const handleCancelResponse = useCallback(() => {
    void cancelCurrentSend();
  }, [cancelCurrentSend]);

  // 切换模式
  const handleModeChange = useCallback(
    (mode: ChatMode) => {
      if (
        interactionBusy ||
        draftRecoveryPending ||
        sessionsLoadingState === 'loading' ||
        (!isDraftSession && !persistedSessionReady)
      ) {
        return;
      }
      if (activeSessionId) {
        void dispatch(updateSessionModeAsync({ sessionId: activeSessionId, mode }))
          .then((result) => {
            if (updateSessionModeAsync.fulfilled.match(result)) {
              refreshSessionList();
              return;
            }
            if (
              pageActiveRef.current &&
              updateSessionModeAsync.rejected.match(result) &&
              !result.meta.condition &&
              !result.meta.aborted
            ) {
              showSessionRequestError(
                toast,
                result.payload ?? result.error,
                '切换模式失败',
                refreshSessionList,
              );
            }
          });
        return;
      }
      dispatch(setMode(mode));
    },
    [activeSessionId, dispatch, draftRecoveryPending, interactionBusy, isDraftSession, persistedSessionReady, refreshSessionList, sessionsLoadingState, toast]
  );

  const resetToDraft = useCallback(() => {
    settlementAttemptRef.current += 1;
    initialMessageHandled.current = false;
    dispatch(clearCurrentSession());
    navigate('/session/new', { replace: true });
  }, [dispatch, navigate]);

  // 新建会话
  const handleNewSession = useCallback(() => {
    if (interactionBusy) return;
    resetToDraft();
  }, [interactionBusy, resetToDraft]);

  // 切换到历史会话
  const handleSelectSession = useCallback(
    (targetSessionId: string) => {
      if (interactionBusy) return;
      if (draftRecoveryPending && targetSessionId === draftSessionId) return;
      settlementAttemptRef.current += 1;
      navigate(`/session/${targetSessionId}`);
    },
    [draftRecoveryPending, draftSessionId, interactionBusy, navigate]
  );

  // 删除会话
  const handleDeleteSession = useCallback(
    async (targetSessionId: string) => {
      if (interactionBusy) return;
      setDeletingSessionId(targetSessionId);
      const result = await dispatch(deleteSessionAsync(targetSessionId));
      setDeletingSessionId(null);

      if (deleteSessionAsync.rejected.match(result) && !result.meta.aborted) {
        showSessionRequestError(
          toast,
          result.payload ?? result.error,
          '删除会话失败',
          refreshSessionList,
        );
        return;
      }
      if (!deleteSessionAsync.fulfilled.match(result)) return;
      if (!isDraftSession && sessionId === targetSessionId) {
        resetToDraft();
      }
    },
    [dispatch, interactionBusy, isDraftSession, refreshSessionList, resetToDraft, sessionId, toast]
  );

  // 批量删除会话
  const handleBatchDeleteSessions = useCallback(async () => {
    if (interactionBusy || selectedSessionIds.length === 0) return;

    setIsBatchDeleting(true);
    const result = await dispatch(batchDeleteSessionsAsync(selectedSessionIds));
    setIsBatchDeleting(false);

    if (batchDeleteSessionsAsync.rejected.match(result) && !result.meta.aborted) {
      showSessionRequestError(
        toast,
        result.payload ?? result.error,
        '批量删除失败',
        refreshSessionList,
      );
      return;
    }
    if (!batchDeleteSessionsAsync.fulfilled.match(result)) return;
    setSelectedSessionIds([]);
    setIsSelectMode(false);

    if (!isDraftSession && sessionId && selectedSessionIds.includes(sessionId)) {
      resetToDraft();
    }
  }, [interactionBusy, selectedSessionIds, dispatch, isDraftSession, refreshSessionList, resetToDraft, sessionId, toast]);

  // 切换选择模式
  const handleToggleSelectMode = useCallback(() => {
    if (interactionBusy) return;
    setIsSelectMode((prev) => !prev);
    setSelectedSessionIds([]);
  }, [interactionBusy]);

  // 切换会话选中状态
  const handleToggleSessionSelection = useCallback((sessionId: string) => {
    if (interactionBusy) return;
    setSelectedSessionIds((prev) =>
      prev.includes(sessionId) ? prev.filter((id) => id !== sessionId) : [...prev, sessionId]
    );
  }, [interactionBusy]);

  // 全选/取消全选
  const handleSelectAllSessions = useCallback(() => {
    if (interactionBusy) return;
    if (selectedSessionIds.length === sessions.length) {
      setSelectedSessionIds([]);
    } else {
      setSelectedSessionIds(sessions.map((s) => s.id));
    }
  }, [interactionBusy, selectedSessionIds, sessions]);

  return (
    <MainLayout>
      <div className="relative flex h-[calc(100dvh-4rem)] min-w-0 overflow-hidden bg-surface-50 dark:bg-surface-900">
        {/* 侧边栏 */}
        <ChatSidebar
          isOpen={sidebarOpen}
          sessions={sessions}
          currentSessionId={isDraftSession ? draftSessionId ?? undefined : sessionId}
          isSelectMode={isSelectMode}
          selectedSessionIds={selectedSessionIds}
          deletingSessionId={deletingSessionId}
          isBatchDeleting={isBatchDeleting}
          loading={sessionsLoadingState === 'loading'}
          error={sessionsError}
          onRetry={refreshSessionList}
          interactionDisabled={interactionBusy}
          onToggleSidebar={() => setSidebarOpen((prev) => !prev)}
          onNewSession={handleNewSession}
          onSelectSession={(id) => {
            handleSelectSession(id);
            if (!window.matchMedia('(min-width: 768px)').matches) setSidebarOpen(false);
          }}
          onDeleteSession={handleDeleteSession}
          onToggleSelectMode={handleToggleSelectMode}
          onToggleSessionSelection={handleToggleSessionSelection}
          onSelectAll={handleSelectAllSessions}
          onBatchDelete={handleBatchDeleteSessions}
        />

        {/* 主聊天区域 */}
        <div className="flex min-w-0 flex-1 flex-col">
          {/* 顶部栏 + 同一行的模式选择器 */}
          <ChatHeader
            currentMode={currentModeConfig}
            sidebarOpen={sidebarOpen}
            onToggleSidebar={() => setSidebarOpen(!sidebarOpen)}
            rightSlot={
              <ModeSelector
                modes={CHAT_MODES}
                currentMode={currentMode}
                onModeChange={handleModeChange}
                disabled={
                  interactionBusy ||
                  draftRecoveryPending ||
                  sessionsLoadingState === 'loading' ||
                  (!isDraftSession && !persistedSessionReady)
                }
              />
            }
          />

          {/* 消息列表 */}
          <ChatMessages
            messages={showDraftWelcome ? [] : messages}
            draftWelcome={showDraftWelcome ? draftWelcome : undefined}
            draftModeName={showDraftWelcome ? currentModeConfig.name : undefined}
            streamingMessageId={streamingMessageId}
            isLoading={isLoading}
            error={error}
            onRetry={() => {
              if (sessionId && sessionId !== 'new') {
                void dispatch(fetchHistoryAsync({ sessionId }));
              }
            }}
            messagesContainerRef={messagesContainerRef}
          />

          {/* 快捷操作 */}
          {(showDraftWelcome || messages.length === 0) &&
            !isLoading &&
            !interactionBusy &&
            !isFileParsing &&
            (isDraftSession || persistedSessionReady) && (
            <QuickActions actions={QUICK_ACTIONS} onActionClick={handleSendMessage} />
          )}

          {/* 输入区域 */}
          <ChatInput
            value={inputValue}
            selectedImages={selectedImages}
            previewUrls={previewUrls}
            isStreaming={isStreaming}
            isSending={isSending}
            disabled={composerDisabled || (!isDraftSession && !persistedSessionReady)}
            files={uploadedFiles}
            isFileParsing={isFileParsing}
            onChange={handleInputChange}
            onSend={handleSendMessage}
            onCancel={handleCancelResponse}
            onImageSelect={handleImageSelect}
            onRemoveImage={handleRemoveImage}
            onFileSelect={handleFileSelect}
            onRemoveFile={handleRemoveFile}
          />
        </div>
      </div>
    </MainLayout>
  );
};

export default SessionChatPage;
