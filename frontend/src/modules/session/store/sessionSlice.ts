import { createSlice, createAsyncThunk, type PayloadAction } from '@reduxjs/toolkit';
import type { RootState } from '@/store';
import type { LearningSession, SessionMessage, LoadingState } from '@/types';
import { createFieldSelector } from '@/store/utils/sliceFactory';
import {
  sessionService,
  type HistoryResponse,
} from '@/modules/session/services/sessionService';
import type {
  ChatMode,
  ChatSessionListItem,
  DraftSessionIdentity,
  SessionMode,
} from '@/modules/session/types';
import {
  isSessionNotFoundError,
  toSessionRequestError,
  type SessionRequestError,
} from '@/modules/session/errors';
import { formatChatMessageForDisplay } from '@/modules/session/documentMessage';
import { normalizeSessionKnowledge } from '@/modules/session/knowledge';

export type { ChatMode, ChatSessionListItem } from '@/modules/session/types';

/**
 * 流式状态
 */
export type StreamStatus = 'idle' | 'streaming' | 'cancelled' | 'error';

/**
 * 会话状态
 */
export interface SessionState {
  currentSession: LearningSession | null;
  messages: SessionMessage[];
  mode: ChatMode;
  draftSessionId: string | null;
  draftSessionTopic: string | null;
  draftSessionMode: SessionMode | null;
  draftSessionMaterialized: boolean;
  draftFirstTurnCompleted: boolean;
  loadingState: LoadingState;
  sendingState: LoadingState;
  error: SessionRequestError | null;
  historyRequestId: string | null;
  historySessionId: string | null;
  historySessionStatus: LearningSession['status'] | null;
  reconcileState: LoadingState;
  reconcileRequestId: string | null;
  reconcileSessionId: string | null;
  sessions: ChatSessionListItem[];
  sessionsLoadingState: LoadingState;
  sessionsError: SessionRequestError | null;
  sessionsRequestId: string | null;
  modeUpdateState: LoadingState;
  modeUpdateRequestId: string | null;
  modeUpdateSessionId: string | null;
  // 流式响应相关
  currentTaskId: string | null;
  streamStatus: StreamStatus;
  streamingMessageId: string | null;
}

const initialState: SessionState = {
  currentSession: null,
  messages: [],
  mode: 'chat',
  draftSessionId: null,
  draftSessionTopic: null,
  draftSessionMode: null,
  draftSessionMaterialized: false,
  draftFirstTurnCompleted: false,
  loadingState: 'idle',
  sendingState: 'idle',
  error: null,
  historyRequestId: null,
  historySessionId: null,
  historySessionStatus: null,
  reconcileState: 'idle',
  reconcileRequestId: null,
  reconcileSessionId: null,
  sessions: [],
  sessionsLoadingState: 'idle',
  sessionsError: null,
  sessionsRequestId: null,
  modeUpdateState: 'idle',
  modeUpdateRequestId: null,
  modeUpdateSessionId: null,
  // 流式响应相关
  currentTaskId: null,
  streamStatus: 'idle',
  streamingMessageId: null,
};

// ============ Async Thunks ============

const hasCompletedFirstTurn = (messages: HistoryResponse['messages']): boolean => {
  const firstUserIndex = messages.findIndex((message) => message.role === 'user');
  return firstUserIndex >= 0
    && messages.slice(firstUserIndex + 1).some((message) => message.role === 'assistant');
};

const mapHistoryResponse = (
  sessionId: string,
  response: HistoryResponse
) => ({
  sessionId,
  status: response.status,
  mode: response.mode,
  messages: response.messages.map<SessionMessage>((msg) => ({
    id: msg.id,
    sessionId,
    role: msg.role,
    content: msg.role === 'user'
      ? formatChatMessageForDisplay(msg.content)
      : msg.content,
    timestamp: msg.timestamp,
    attachments: msg.attachments,
    knowledge: normalizeSessionKnowledge(msg.knowledge),
    metadata: {
      agent: msg.agent,
    },
  })),
  firstTurnCompleted: hasCompletedFirstTurn(response.messages),
  total: response.total,
  hasMore: response.has_more,
});

type HistoryResult = ReturnType<typeof mapHistoryResponse>;
type FetchHistoryArgs = { sessionId: string; limit?: number; offset?: number };
type SessionHistoryThunkConfig = { rejectValue: SessionRequestError };

/**
 * 创建会话
 */
export const createSessionAsync = createAsyncThunk<
  { session: LearningSession; welcomeMessage: SessionMessage; mode: SessionMode },
  { topic?: string; mode?: SessionMode },
  SessionHistoryThunkConfig
>(
  'session/createSession',
  async (
    { topic, mode }: { topic?: string; mode?: SessionMode },
    { rejectWithValue, signal }
  ) => {
    try {
      const response = await sessionService.createSession(topic, mode, signal);

      // 转换为前端格式
      const session: LearningSession = {
        id: response.session_id,
        studentId: response.user_id,
        title: response.topic || '新会话',
        status: response.status,
        startedAt: response.created_at,
        messageCount: 1,
      };

      const welcomeMessage: SessionMessage = {
        id: response.welcome_message.id,
        sessionId: response.session_id,
        role: response.welcome_message.role as 'user' | 'assistant' | 'system',
        content: response.welcome_message.content,
        timestamp: response.welcome_message.timestamp,
        metadata: {
          agent: response.welcome_message.agent,
        },
      };

      return { session, welcomeMessage, mode: response.mode };
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '创建会话失败'));
    }
  }
);

/**
 * 获取会话历史
 */
export const fetchHistoryAsync = createAsyncThunk<
  HistoryResult,
  FetchHistoryArgs,
  SessionHistoryThunkConfig
>(
  'session/fetchHistory',
  async (
    { sessionId, limit, offset },
    { rejectWithValue, signal }
  ) => {
    try {
      const response = offset === undefined
        ? await sessionService.getLatestHistory(sessionId, limit, signal)
        : await sessionService.getHistory(sessionId, limit, offset, signal);

      return mapHistoryResponse(sessionId, response);
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '获取历史失败'));
    }
  }
);

/**
 * 发送异常后重新获取服务端历史。
 * 与页面首次加载分离，避免同步失败时清空仍可重试的本地消息。
 */
export const reconcileHistoryAsync = createAsyncThunk<
  HistoryResult,
  { sessionId: string; preserveDraftOnNotFound?: boolean },
  SessionHistoryThunkConfig
>(
  'session/reconcileHistory',
  async ({ sessionId }, { rejectWithValue, signal }) => {
    try {
      const response = await sessionService.getLatestHistory(sessionId, undefined, signal);
      return mapHistoryResponse(sessionId, response);
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '同步历史失败'));
    }
  },
  {
    condition: (_, { getState }) => {
      const { reconcileState } = (getState() as RootState).session;
      return reconcileState !== 'loading';
    },
  }
);

/**
 * 获取会话列表
 */
export const fetchSessionsAsync = createAsyncThunk<
  { sessions: ChatSessionListItem[]; total: number },
  { limit?: number; offset?: number; force?: boolean } | undefined,
  SessionHistoryThunkConfig
>(
  'session/fetchSessions',
  async (
    { limit, offset }: { limit?: number; offset?: number; force?: boolean } = {},
    { rejectWithValue, signal }
  ) => {
    try {
      const response = await sessionService.getSessions(limit, offset, {
        withUserMessages: true,
      }, signal);

      // 转换为前端格式
      const sessions: ChatSessionListItem[] = response.sessions.map((s) => ({
        id: s.session_id,
        studentId: s.user_id,
        title: s.topic || '会话',
        mode: s.mode,
        status: s.status,
        startedAt: s.started_at,
        endedAt: s.ended_at || undefined,
        messageCount: s.message_count,
      }));

      return { sessions, total: response.total };
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '获取会话列表失败'));
    }
  },
  {
    condition: (args, { getState }) => {
      const { sessionsLoadingState, modeUpdateState } = (getState() as RootState).session;
      if (modeUpdateState === 'loading') return false;
      return args?.force === true || sessionsLoadingState !== 'loading';
    },
  }
);

/**
 * 结束会话
 */
export const endSessionAsync = createAsyncThunk<
  string,
  string,
  SessionHistoryThunkConfig
>(
  'session/endSession',
  async (sessionId: string, { rejectWithValue, signal }) => {
    try {
      await sessionService.endSession(sessionId, signal);
      return sessionId;
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '结束会话失败'));
    }
  }
);

/**
 * 更新会话模式
 */
export const updateSessionModeAsync = createAsyncThunk<
  { sessionId: string; mode: SessionMode },
  { sessionId: string; mode: ChatMode },
  SessionHistoryThunkConfig
>(
  'session/updateSessionMode',
  async (
    { sessionId, mode }: { sessionId: string; mode: ChatMode },
    { rejectWithValue, signal }
  ) => {
    try {
      const response = await sessionService.updateSessionMode(sessionId, mode, signal);
      return { sessionId: response.session_id, mode: response.mode };
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '更新模式失败'));
    }
  },
  {
    condition: (_, { getState }) => {
      const state = (getState() as RootState).session;
      return (
        state.modeUpdateState !== 'loading' &&
        state.sessionsLoadingState !== 'loading' &&
        state.loadingState !== 'loading' &&
        state.sendingState !== 'loading' &&
        state.streamStatus !== 'streaming' &&
        state.reconcileState !== 'loading'
      );
    },
  }
);

/**
 * 删除会话
 */
export const deleteSessionAsync = createAsyncThunk<
  string,
  string,
  SessionHistoryThunkConfig
>(
  'session/deleteSession',
  async (sessionId: string, { rejectWithValue, signal }) => {
    try {
      const response = await sessionService.deleteSession(sessionId, signal);
      if (!response.success) {
        return rejectWithValue(toSessionRequestError({
          status: 409,
          code: 'CONFLICT',
          message: response.message || '会话状态已发生变化',
          source: 'http',
        }, '删除会话失败'));
      }
      return sessionId;
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '删除会话失败'));
    }
  }
);

/**
 * 批量删除会话
 */
export const batchDeleteSessionsAsync = createAsyncThunk<
  string[],
  string[],
  SessionHistoryThunkConfig
>(
  'session/batchDeleteSessions',
  async (sessionIds: string[], { rejectWithValue, signal }) => {
    try {
      const response = await sessionService.batchDeleteSessions(sessionIds, signal);
      if (!response.success) {
        return rejectWithValue(toSessionRequestError({
          status: 409,
          code: 'CONFLICT',
          message: response.message || '会话状态已发生变化',
          source: 'http',
        }, '批量删除会话失败'));
      }
      return sessionIds;
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '批量删除会话失败'));
    }
  }
);

/**
 * 取消当前任务
 */
export const cancelCurrentTaskAsync = createAsyncThunk<
  string,
  void,
  SessionHistoryThunkConfig
>(
  'session/cancelCurrentTask',
  async (_, { getState, rejectWithValue, signal }) => {
    const state = getState() as { session: SessionState };
    const taskId = state.session.currentTaskId;

    if (!taskId) {
      return rejectWithValue(toSessionRequestError({
        code: 'CANCELLED',
        message: '没有正在进行的任务',
        source: 'ui',
      }, '没有正在进行的任务'));
    }

    try {
      const success = await sessionService.cancelTask(taskId, signal);
      if (!success) {
        return rejectWithValue(toSessionRequestError({
          status: 404,
          code: 'NOT_FOUND',
          message: '任务不存在或已完成',
          source: 'http',
        }, '取消任务失败'));
      }
      return taskId;
    } catch (error) {
      if (signal.aborted) throw error;
      return rejectWithValue(toSessionRequestError(error, '取消任务失败'));
    }
  }
);

// ============ Slice ============

const sessionSlice = createSlice({
  name: 'session',
  initialState,
  reducers: {
    // 设置当前会话
    setCurrentSession(state, action: PayloadAction<LearningSession | null>) {
      state.currentSession = action.payload;
    },

    // 设置消息列表
    setMessages(state, action: PayloadAction<SessionMessage[]>) {
      state.messages = action.payload;
    },

    // 添加消息
    addMessage(state, action: PayloadAction<SessionMessage>) {
      state.messages.push(action.payload);

      // 更新会话消息计数
      if (state.currentSession?.id === action.payload.sessionId) {
        state.currentSession.messageCount = state.messages.length;
      }
    },

    // 移除尚未在服务端物化的乐观消息，不清空其他会话状态。
    removeMessagesById(state, action: PayloadAction<string[]>) {
      const messageIds = new Set(action.payload);
      state.messages = state.messages.filter((message) => !messageIds.has(message.id));
      if (state.currentSession) {
        state.currentSession.messageCount = state.messages.length;
      }
    },

    // 首次发送前保存稳定的客户端会话 ID，网络结果不确定时可用同一 ID 重放或对账。
    prepareDraftSession(state, action: PayloadAction<DraftSessionIdentity>) {
      const { sessionId, topic, mode } = action.payload;
      if (state.draftSessionId === sessionId) return;
      if (state.draftSessionId) {
        state.messages = [];
      }
      state.draftSessionId = sessionId;
      state.draftSessionTopic = topic ?? null;
      state.draftSessionMode = mode;
      state.draftSessionMaterialized = false;
      state.draftFirstTurnCompleted = false;
    },

    // 服务端确认事务提交后，统一草稿身份并重绑本轮乐观消息。
    materializeDraftSession(state, action: PayloadAction<string>) {
      const previousSessionId = state.draftSessionId;
      state.draftSessionId = action.payload;
      state.draftSessionMaterialized = true;
      if (previousSessionId && previousSessionId !== action.payload) {
        for (const message of state.messages) {
          if (message.sessionId === previousSessionId) {
            message.sessionId = action.payload;
          }
        }
      }
    },

    // 首轮回答完成后才允许从幂等 start-chat 切换到普通会话聊天。
    completeDraftFirstTurn(state, action: PayloadAction<string>) {
      if (state.draftSessionId !== action.payload) return;
      state.draftSessionMaterialized = true;
      state.draftFirstTurnCompleted = true;
    },

    // 更新最后一条消息（用于流式响应）
    updateLastMessage(state, action: PayloadAction<Partial<SessionMessage>>) {
      if (state.messages.length > 0) {
        const lastMessage = state.messages[state.messages.length - 1];
        state.messages[state.messages.length - 1] = {
          ...lastMessage,
          ...action.payload,
        };
      }
    },

    setMessageKnowledge(state, action: PayloadAction<{ id: string; knowledge: SessionMessage['knowledge'] }>) {
      const message = state.messages.find((item) => item.id === action.payload.id && item.role === 'assistant');
      if (message) message.knowledge = action.payload.knowledge;
    },

    // 追加内容到流式消息（用于流式响应）
    // 优先按 streamingMessageId 精确定位目标消息，fallback 到最后一条
    // 利用 Immer Proxy 机制，O(1) 修改，未变更的消息保持原引用
    appendToLastMessage(state, action: PayloadAction<string>) {
      const targetId = state.streamingMessageId;
      const target = targetId
        ? state.messages.find((m) => m.id === targetId)
        : state.messages[state.messages.length - 1];
      if (target) {
        target.content += action.payload;
      }
    },

    // 设置聊天模式
    setMode(state, action: PayloadAction<ChatMode>) {
      state.mode = action.payload;
    },

    // 清除当前会话
    clearCurrentSession(state) {
      state.currentSession = null;
      state.messages = [];
      state.loadingState = 'idle';
      state.error = null;
      state.historyRequestId = null;
      state.historySessionId = null;
      state.historySessionStatus = null;
      state.draftSessionId = null;
      state.draftSessionTopic = null;
      state.draftSessionMode = null;
      state.draftSessionMaterialized = false;
      state.draftFirstTurnCompleted = false;
      state.reconcileState = 'idle';
      state.reconcileRequestId = null;
      state.reconcileSessionId = null;
      state.currentTaskId = null;
      state.streamStatus = 'idle';
      state.streamingMessageId = null;
    },

    // 仅使指定会话失效，避免迟到错误清空用户已经切换到的新会话。
    invalidateSession(state, action: PayloadAction<string>) {
      const sessionId = action.payload;
      const invalidatesActiveSession =
        state.currentSession?.id === sessionId ||
        state.historySessionId === sessionId ||
        state.draftSessionId === sessionId;

      state.sessions = state.sessions.filter((session) => session.id !== sessionId);
      if (state.currentSession?.id === sessionId) {
        state.currentSession = null;
      }
      if (invalidatesActiveSession) {
        state.messages = [];
        state.loadingState = 'idle';
        state.sendingState = 'idle';
        state.error = null;
        state.historyRequestId = null;
        state.historySessionId = null;
        state.historySessionStatus = null;
        state.currentTaskId = null;
        state.streamStatus = 'idle';
        state.streamingMessageId = null;
        state.draftSessionId = null;
        state.draftSessionTopic = null;
        state.draftSessionMode = null;
        state.draftSessionMaterialized = false;
        state.draftFirstTurnCompleted = false;
      }
      if (state.reconcileSessionId === sessionId) {
        state.reconcileState = 'idle';
        state.reconcileRequestId = null;
        state.reconcileSessionId = null;
      }
      if (state.modeUpdateSessionId === sessionId) {
        state.modeUpdateState = 'idle';
        state.modeUpdateRequestId = null;
        state.modeUpdateSessionId = null;
      }
    },

    // 设置加载状态
    setLoadingState(state, action: PayloadAction<LoadingState>) {
      state.loadingState = action.payload;
      if (action.payload === 'loading') {
        state.error = null;
      }
    },

    // 设置发送状态
    setSendingState(state, action: PayloadAction<LoadingState>) {
      state.sendingState = action.payload;
      if (action.payload === 'loading') {
        state.error = null;
      }
    },

    // 设置错误信息
    setError(state, action: PayloadAction<SessionRequestError>) {
      state.error = action.payload;
      state.loadingState = 'error';
    },

    // 设置会话列表
    setSessions(state, action: PayloadAction<ChatSessionListItem[]>) {
      state.sessions = action.payload;
    },

    // 添加会话到列表
    addSession(state, action: PayloadAction<ChatSessionListItem>) {
      state.sessions.unshift(action.payload);
    },

    // 更新会话状态
    updateSessionStatus(
      state,
      action: PayloadAction<{ sessionId: string; status: LearningSession['status'] }>
    ) {
      const { sessionId, status } = action.payload;

      // 更新当前会话
      if (state.currentSession?.id === sessionId) {
        state.currentSession.status = status;
        if (status === 'completed') {
          state.currentSession.endedAt = new Date().toISOString();
        }
      }

      // 更新会话列表
      const session = state.sessions.find(s => s.id === sessionId);
      if (session) {
        session.status = status;
        if (status === 'completed') {
          session.endedAt = new Date().toISOString();
        }
      }
    },

    // 设置会话列表加载状态
    setSessionsLoadingState(state, action: PayloadAction<LoadingState>) {
      state.sessionsLoadingState = action.payload;
    },

    // 设置当前任务 ID
    setCurrentTaskId(state, action: PayloadAction<string | null>) {
      state.currentTaskId = action.payload;
    },

    // 设置流式状态
    setStreamStatus(state, action: PayloadAction<StreamStatus>) {
      state.streamStatus = action.payload;
    },

    // 设置正在流式接收的消息 ID
    setStreamingMessageId(state, action: PayloadAction<string | null>) {
      state.streamingMessageId = action.payload;
    },

    // 重置状态
    resetSessionState() {
      return initialState;
    },
  },
  extraReducers: (builder) => {
    // 创建会话
    builder
      .addCase(createSessionAsync.pending, (state) => {
        state.loadingState = 'loading';
        state.error = null;
      })
      .addCase(createSessionAsync.fulfilled, (state, action) => {
        state.loadingState = 'success';
        state.currentSession = action.payload.session;
        state.messages = [action.payload.welcomeMessage];
        state.mode = action.payload.mode;
        state.draftSessionId = null;
        state.draftSessionTopic = null;
        state.draftSessionMode = null;
        state.draftSessionMaterialized = false;
        state.draftFirstTurnCompleted = false;
      })
      .addCase(createSessionAsync.rejected, (state, action) => {
        if (action.meta.aborted) {
          state.loadingState = 'idle';
          return;
        }
        state.loadingState = 'error';
        state.error = action.payload ?? toSessionRequestError(action.error, '创建会话失败');
      });

    // 获取历史
    builder
      .addCase(fetchHistoryAsync.pending, (state, action) => {
        state.loadingState = 'loading';
        state.error = null;
        state.messages = [];
        state.historyRequestId = action.meta.requestId;
        state.historySessionId = null;
        state.historySessionStatus = null;
        state.draftSessionId = null;
        state.draftSessionTopic = null;
        state.draftSessionMode = null;
        state.draftSessionMaterialized = false;
        state.draftFirstTurnCompleted = false;
        state.reconcileState = 'idle';
        state.reconcileRequestId = null;
        state.reconcileSessionId = null;
      })
      .addCase(fetchHistoryAsync.fulfilled, (state, action) => {
        if (state.historyRequestId !== action.meta.requestId) return;

        state.loadingState = 'success';
        state.error = null;
        state.messages = action.payload.messages;
        state.historyRequestId = null;
        state.historySessionId = action.payload.sessionId;
        state.historySessionStatus = action.payload.status;
        state.mode = action.payload.mode;
      })
      .addCase(fetchHistoryAsync.rejected, (state, action) => {
        if (state.historyRequestId !== action.meta.requestId) return;

        if (action.meta.aborted) {
          state.loadingState = 'idle';
          state.error = null;
        } else {
          state.loadingState = 'error';
          state.error = action.payload ?? toSessionRequestError(action.error, '获取历史失败');
        }
        state.historyRequestId = null;
        state.historySessionId = null;
        state.historySessionStatus = null;
      });

    // 异常对账不复用首屏加载状态，失败时保留本地消息供用户重试。
    builder
      .addCase(reconcileHistoryAsync.pending, (state, action) => {
        state.reconcileState = 'loading';
        state.reconcileRequestId = action.meta.requestId;
        state.reconcileSessionId = action.meta.arg.sessionId;
      })
      .addCase(reconcileHistoryAsync.fulfilled, (state, action) => {
        if (
          state.reconcileRequestId !== action.meta.requestId ||
          state.reconcileSessionId !== action.payload.sessionId
        ) {
          return;
        }

        state.reconcileState = 'success';
        state.reconcileRequestId = null;
        state.reconcileSessionId = null;

        const activeSessionId = state.historySessionId
          ?? state.currentSession?.id
          ?? state.draftSessionId;
        if (activeSessionId !== action.payload.sessionId) return;
        state.messages = action.payload.messages;
        state.historySessionStatus = action.payload.status;
        state.mode = action.payload.mode;
        if (state.draftSessionId === action.payload.sessionId) {
          state.draftSessionMaterialized = true;
          state.draftFirstTurnCompleted = action.payload.firstTurnCompleted;
        }
      })
      .addCase(reconcileHistoryAsync.rejected, (state, action) => {
        if (
          state.reconcileRequestId !== action.meta.requestId ||
          state.reconcileSessionId !== action.meta.arg.sessionId
        ) {
          return;
        }

        const missingSession = isSessionNotFoundError(action.payload);
        const preserveDraft = missingSession
          && action.meta.arg.preserveDraftOnNotFound === true
          && state.draftSessionId === action.meta.arg.sessionId;
        const activeSessionId = state.historySessionId
          ?? state.currentSession?.id
          ?? state.draftSessionId;

        state.reconcileState = action.meta.condition || missingSession ? 'idle' : 'error';
        state.reconcileRequestId = null;
        state.reconcileSessionId = null;
        if (missingSession) {
          state.sessions = state.sessions.filter(
            (session) => session.id !== action.meta.arg.sessionId
          );
        }
        if (missingSession && !preserveDraft && activeSessionId === action.meta.arg.sessionId) {
          state.currentSession = null;
          state.messages = [];
          state.loadingState = 'idle';
          state.sendingState = 'idle';
          state.error = null;
          state.historyRequestId = null;
          state.historySessionId = null;
          state.historySessionStatus = null;
          state.currentTaskId = null;
          state.streamStatus = 'idle';
          state.streamingMessageId = null;
          state.draftSessionId = null;
          state.draftSessionTopic = null;
          state.draftSessionMode = null;
          state.draftSessionMaterialized = false;
          state.draftFirstTurnCompleted = false;
        }
      });

    // 获取会话列表
    builder
      .addCase(fetchSessionsAsync.pending, (state, action) => {
        state.sessionsLoadingState = 'loading';
        state.sessionsError = null;
        state.sessionsRequestId = action.meta.requestId;
      })
      .addCase(fetchSessionsAsync.fulfilled, (state, action) => {
        if (state.sessionsRequestId !== action.meta.requestId) return;

        state.sessionsLoadingState = 'success';
        state.sessions = action.payload.sessions;
        state.sessionsError = null;
        state.sessionsRequestId = null;
      })
      .addCase(fetchSessionsAsync.rejected, (state, action) => {
        if (state.sessionsRequestId !== action.meta.requestId) return;

        state.sessionsLoadingState = action.meta.aborted ? 'idle' : 'error';
        state.sessionsError = action.meta.aborted
          ? null
          : action.payload ?? toSessionRequestError(action.error, '获取会话列表失败');
        state.sessionsRequestId = null;
      });

    // 结束会话
    builder
      .addCase(endSessionAsync.pending, (state) => {
        state.error = null;
      })
      .addCase(endSessionAsync.fulfilled, (state, action) => {
        state.error = null;
        const sessionId = action.payload;
        if (state.currentSession?.id === sessionId) {
          state.currentSession.status = 'completed';
          state.currentSession.endedAt = new Date().toISOString();
        }
        if (state.historySessionId === sessionId) {
          state.historySessionStatus = 'completed';
        }
        const session = state.sessions.find(s => s.id === sessionId);
        if (session) {
          session.status = 'completed';
          session.endedAt = new Date().toISOString();
        }
      })
      .addCase(endSessionAsync.rejected, (state, action) => {
        state.error = action.meta.aborted
          ? null
          : action.payload ?? toSessionRequestError(action.error, '结束会话失败');
      });

    // 取消任务
    builder
      .addCase(cancelCurrentTaskAsync.pending, (state) => {
        state.error = null;
      })
      .addCase(cancelCurrentTaskAsync.fulfilled, (state) => {
        state.error = null;
        state.streamStatus = 'cancelled';
        state.currentTaskId = null;
      })
      .addCase(cancelCurrentTaskAsync.rejected, (state, action) => {
        // 即使取消失败，也重置状态
        state.streamStatus = 'idle';
        state.error = action.meta.aborted || action.payload?.kind === 'cancelled'
          ? null
          : action.payload ?? toSessionRequestError(action.error, '取消任务失败');
      });

    // 更新会话模式：同一时刻只允许一个请求，并忽略已失效的响应。
    builder
      .addCase(updateSessionModeAsync.pending, (state, action) => {
        state.modeUpdateState = 'loading';
        state.error = null;
        state.modeUpdateRequestId = action.meta.requestId;
        state.modeUpdateSessionId = action.meta.arg.sessionId;
      })
      .addCase(updateSessionModeAsync.fulfilled, (state, action) => {
        if (
          state.modeUpdateRequestId !== action.meta.requestId ||
          state.modeUpdateSessionId !== action.payload.sessionId
        ) {
          return;
        }

        state.modeUpdateState = 'success';
        state.error = null;
        state.modeUpdateRequestId = null;
        state.modeUpdateSessionId = null;

        const listSession = state.sessions.find((session) => session.id === action.payload.sessionId);
        if (listSession) listSession.mode = action.payload.mode;

        const activeSessionId = state.historySessionId
          ?? state.currentSession?.id
          ?? state.draftSessionId;
        if (activeSessionId === action.payload.sessionId) {
          state.mode = action.payload.mode;
        }
      })
      .addCase(updateSessionModeAsync.rejected, (state, action) => {
        if (
          state.modeUpdateRequestId !== action.meta.requestId ||
          state.modeUpdateSessionId !== action.meta.arg.sessionId
        ) {
          return;
        }

        state.modeUpdateState = action.meta.aborted ? 'idle' : 'error';
        state.error = action.meta.aborted
          ? null
          : action.payload ?? toSessionRequestError(action.error, '更新模式失败');
        state.modeUpdateRequestId = null;
        state.modeUpdateSessionId = null;
      });

    // 删除会话
    builder
      .addCase(deleteSessionAsync.pending, (state) => {
        state.error = null;
      })
      .addCase(deleteSessionAsync.fulfilled, (state, action) => {
        state.error = null;
        const sessionId = action.payload;
        // 从列表中移除
        state.sessions = state.sessions.filter((s) => s.id !== sessionId);
        // 如果删除的是当前会话，清空当前会话
        if (state.currentSession?.id === sessionId) {
          state.currentSession = null;
          state.messages = [];
        }
        if (state.historySessionId === sessionId) {
          state.historySessionId = null;
          state.historySessionStatus = null;
        }
        if (state.draftSessionId === sessionId) {
          state.draftSessionId = null;
          state.draftSessionTopic = null;
          state.draftSessionMode = null;
          state.draftSessionMaterialized = false;
          state.draftFirstTurnCompleted = false;
          state.messages = [];
        }
      })
      .addCase(deleteSessionAsync.rejected, (state, action) => {
        state.error = action.meta.aborted
          ? null
          : action.payload ?? toSessionRequestError(action.error, '删除会话失败');
      });

    // 批量删除会话
    builder
      .addCase(batchDeleteSessionsAsync.pending, (state) => {
        state.error = null;
      })
      .addCase(batchDeleteSessionsAsync.fulfilled, (state, action) => {
        state.error = null;
        const sessionIds = new Set(action.payload);
        // 从列表中移除
        state.sessions = state.sessions.filter((s) => !sessionIds.has(s.id));
        // 如果删除的包含当前会话，清空当前会话
        if (state.currentSession && sessionIds.has(state.currentSession.id)) {
          state.currentSession = null;
          state.messages = [];
        }
        if (state.historySessionId && sessionIds.has(state.historySessionId)) {
          state.historySessionId = null;
          state.historySessionStatus = null;
        }
        if (state.draftSessionId && sessionIds.has(state.draftSessionId)) {
          state.draftSessionId = null;
          state.draftSessionTopic = null;
          state.draftSessionMode = null;
          state.draftSessionMaterialized = false;
          state.draftFirstTurnCompleted = false;
          state.messages = [];
        }
      })
      .addCase(batchDeleteSessionsAsync.rejected, (state, action) => {
        state.error = action.meta.aborted
          ? null
          : action.payload ?? toSessionRequestError(action.error, '批量删除会话失败');
      });
  },
});

export const {
  setCurrentSession,
  setMessages,
  addMessage,
  removeMessagesById,
  prepareDraftSession,
  materializeDraftSession,
  completeDraftFirstTurn,
  updateLastMessage,
  setMessageKnowledge,
  appendToLastMessage,
  setMode,
  clearCurrentSession,
  invalidateSession,
  setLoadingState,
  setSendingState,
  setError,
  setSessions,
  addSession,
  updateSessionStatus,
  setSessionsLoadingState,
  setCurrentTaskId,
  setStreamStatus,
  setStreamingMessageId,
  resetSessionState,
} = sessionSlice.actions;

// ============ Selectors ============
// 使用工厂函数生成字段 selectors
export const selectCurrentSession = createFieldSelector<SessionState, 'session', 'currentSession'>('session', 'currentSession');
export const selectMessages = createFieldSelector<SessionState, 'session', 'messages'>('session', 'messages');
export const selectMode = createFieldSelector<SessionState, 'session', 'mode'>('session', 'mode');
export const selectDraftSessionId = createFieldSelector<SessionState, 'session', 'draftSessionId'>('session', 'draftSessionId');
export const selectDraftSessionTopic = createFieldSelector<SessionState, 'session', 'draftSessionTopic'>('session', 'draftSessionTopic');
export const selectDraftSessionMode = createFieldSelector<SessionState, 'session', 'draftSessionMode'>('session', 'draftSessionMode');
export const selectDraftSessionMaterialized = createFieldSelector<SessionState, 'session', 'draftSessionMaterialized'>('session', 'draftSessionMaterialized');
export const selectDraftFirstTurnCompleted = createFieldSelector<SessionState, 'session', 'draftFirstTurnCompleted'>('session', 'draftFirstTurnCompleted');
export const selectSessionLoadingState = createFieldSelector<SessionState, 'session', 'loadingState'>('session', 'loadingState');
export const selectSessionSendingState = createFieldSelector<SessionState, 'session', 'sendingState'>('session', 'sendingState');
export const selectSessionError = createFieldSelector<SessionState, 'session', 'error'>('session', 'error');
export const selectHistorySessionId = createFieldSelector<SessionState, 'session', 'historySessionId'>('session', 'historySessionId');
export const selectHistorySessionStatus = createFieldSelector<SessionState, 'session', 'historySessionStatus'>('session', 'historySessionStatus');
export const selectReconcileState = createFieldSelector<SessionState, 'session', 'reconcileState'>('session', 'reconcileState');
export const selectSessions = createFieldSelector<SessionState, 'session', 'sessions'>('session', 'sessions');
export const selectSessionsLoadingState = createFieldSelector<SessionState, 'session', 'sessionsLoadingState'>('session', 'sessionsLoadingState');
export const selectSessionsError = createFieldSelector<SessionState, 'session', 'sessionsError'>('session', 'sessionsError');
export const selectModeUpdateState = createFieldSelector<SessionState, 'session', 'modeUpdateState'>('session', 'modeUpdateState');
export const selectCurrentTaskId = createFieldSelector<SessionState, 'session', 'currentTaskId'>('session', 'currentTaskId');
export const selectStreamStatus = createFieldSelector<SessionState, 'session', 'streamStatus'>('session', 'streamStatus');
export const selectStreamingMessageId = createFieldSelector<SessionState, 'session', 'streamingMessageId'>('session', 'streamingMessageId');

// 派生 selectors
export const selectIsSessionLoading = (state: { session: SessionState }) =>
  state.session.loadingState === 'loading';

export const selectIsMessageSending = (state: { session: SessionState }) =>
  state.session.sendingState === 'loading';

export const selectIsStreaming = (state: { session: SessionState }) =>
  state.session.streamStatus === 'streaming';

export default sessionSlice.reducer;
