/**
 * 学习会话服务
 *
 * 提供会话创建、消息发送、历史记录等 API
 */

import { apiClient } from '@/libs/http/apiClient';
import { createSSEConnection, cancelTask, type SSEHandlers, type SSEController } from '@/libs/http/sseClient';
import { logger } from '@/libs/utils/logger';
import type { SessionMode } from '@/modules/session/types';

export type { SessionMode } from '@/modules/session/types';

const sessionLogger = logger.createContextLogger('SessionService');

// ========== 类型定义 ==========

/** 会话状态 */
export type SessionStatus = 'active' | 'completed' | 'paused';

/** 消息响应 */
export interface MessageResponse {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  agent: string | null;
  timestamp: string;
  attachments: string[];
  knowledge?: import('@/modules/session/knowledge').SessionKnowledge | null;
}

/** 创建会话响应 */
export interface CreateSessionResponse {
  session_id: string;
  user_id: string;
  topic: string | null;
  mode: SessionMode;
  status: SessionStatus;
  created_at: string;
  welcome_message: MessageResponse;
}

/** 会话响应 */
export interface SessionResponse {
  session_id: string;
  user_id: string;
  topic: string | null;
  mode: SessionMode;
  status: SessionStatus;
  started_at: string;
  ended_at: string | null;
  message_count: number;
}

/** 会话列表响应 */
export interface SessionListResponse {
  sessions: SessionResponse[];
  total: number;
}

export interface GetSessionsOptions {
  withUserMessages?: boolean;
}

export interface StartChatStreamRequest {
  sessionId: string;
  topic?: string;
  mode: SessionMode;
  message: string;
  attachments?: string[];
}

/** 历史消息响应 */
export interface HistoryResponse {
  session_id: string;
  status: SessionStatus;
  mode: SessionMode;
  messages: MessageResponse[];
  total: number;
  has_more: boolean;
}

/** 更新模式响应 */
export interface UpdateModeResponse {
  session_id: string;
  mode: SessionMode;
  topic: string | null;
}

/** 删除会话响应 */
export interface DeleteSessionResponse {
  success: boolean;
  message: string;
}

/** 批量删除会话响应 */
export interface BatchDeleteResponse {
  success: boolean;
  deleted_count: number;
  message: string;
}

const MAX_HISTORY_PAGE_SIZE = 100;

const fetchHistoryPage = async (
  sessionId: string,
  limit: number,
  offset: number,
  signal?: AbortSignal
): Promise<HistoryResponse> => {
  sessionLogger.debug('Fetching history', { sessionId, limit, offset });

  const response = await apiClient.get<HistoryResponse>(
    `/session/${sessionId}/history`,
    {
      params: { limit, offset },
      signal,
    }
  );

  return response.data;
};

// ========== 服务实现 ==========

export const sessionService = {
  /**
   * 创建会话
   *
   * @param topic - 会话主题（可选）
   * @param mode - 会话模式
   * @returns 会话信息和欢迎消息
   */
  async createSession(
    topic?: string,
    mode: SessionMode = 'chat',
    signal?: AbortSignal
  ): Promise<CreateSessionResponse> {
    sessionLogger.debug('Creating session', { topic, mode });

    const response = await apiClient.post<CreateSessionResponse>(
      '/session/start',
      { topic, mode },
      { signal }
    );

    sessionLogger.info('Session created', { sessionId: response.data.session_id });

    return response.data;
  },

  /**
   * 原子创建草稿会话及首条用户消息，再以 SSE 接收导师回复。
   */
  startChatStream(
    request: StartChatStreamRequest,
    handlers: SSEHandlers
  ): SSEController {
    sessionLogger.debug('Starting first chat stream', {
      mode: request.mode,
      messageLength: request.message.length,
    });

    return createSSEConnection(
      '/api/v1/session/start-chat',
      {
        session_id: request.sessionId,
        topic: request.topic,
        mode: request.mode,
        message: request.message,
        attachments: request.attachments || null,
      },
      {
        ...handlers,
        onSessionInfo: (sessionId) => {
          sessionLogger.info('First chat session materialized', { sessionId });
          handlers.onSessionInfo?.(sessionId);
        },
        onOpen: () => {
          sessionLogger.debug('First chat stream opened');
          handlers.onOpen?.();
        },
        onClose: () => {
          sessionLogger.debug('First chat stream closed');
          handlers.onClose?.();
        },
        onError: (error) => {
          sessionLogger.error('First chat stream error', { error });
          handlers.onError?.(error);
        },
      }
    );
  },

  /**
   * 流式聊天
   *
   * 建立 SSE 连接，流式接收 AI 响应
   *
   * @param sessionId - 会话 ID
   * @param message - 用户消息
   * @param handlers - 事件处理器
   * @param attachments - 附件列表（可选）
   * @returns SSE 控制器
   */
  chatStream(
    sessionId: string,
    message: string,
    handlers: SSEHandlers,
    attachments?: string[]
  ): SSEController {
    sessionLogger.debug('Starting chat stream', {
      sessionId,
      messageLength: message.length,
    });

    return createSSEConnection(
      `/api/v1/session/${sessionId}/chat`,
      {
        message,
        attachments: attachments || null,
      },
      {
        ...handlers,
        onOpen: () => {
          sessionLogger.debug('Chat stream opened', { sessionId });
          handlers.onOpen?.();
        },
        onClose: () => {
          sessionLogger.debug('Chat stream closed', { sessionId });
          handlers.onClose?.();
        },
        onError: (error) => {
          sessionLogger.error('Chat stream error', { sessionId, error });
          handlers.onError?.(error);
        },
      }
    );
  },

  /**
   * 获取会话历史
   *
   * @param sessionId - 会话 ID
   * @param limit - 返回数量限制
   * @param offset - 偏移量
   * @returns 历史消息列表
   */
  async getHistory(
    sessionId: string,
    limit: number = 50,
    offset: number = 0,
    signal?: AbortSignal
  ): Promise<HistoryResponse> {
    return fetchHistoryPage(sessionId, limit, offset, signal);
  },

  /**
   * 获取最近一页会话历史。
   *
   * 保留 getHistory 的原始分页语义，并在服务边界内处理总数变化，
   * 避免页面加载和异常对账只取到长会话最早的消息。
   */
  async getLatestHistory(
    sessionId: string,
    limit: number = MAX_HISTORY_PAGE_SIZE,
    signal?: AbortSignal
  ): Promise<HistoryResponse> {
    const pageSize = Math.min(Math.max(Math.trunc(limit), 1), MAX_HISTORY_PAGE_SIZE);
    const firstPage = await fetchHistoryPage(sessionId, pageSize, 0, signal);
    if (!firstPage.has_more) return firstPage;

    const latestOffset = Math.max(firstPage.total - pageSize, 0);
    const latestPage = await fetchHistoryPage(sessionId, pageSize, latestOffset, signal);
    if (!latestPage.has_more) return latestPage;

    // 消息可能在两次请求之间继续落库；最多重算一次最新偏移。
    const refreshedOffset = Math.max(latestPage.total - pageSize, 0);
    return fetchHistoryPage(sessionId, pageSize, refreshedOffset, signal);
  },

  /**
   * 获取会话列表
   *
   * @param limit - 返回数量限制
   * @param offset - 偏移量
   * @returns 会话列表
   */
  async getSessions(
    limit: number = 20,
    offset: number = 0,
    options: GetSessionsOptions = {},
    signal?: AbortSignal
  ): Promise<SessionListResponse> {
    sessionLogger.debug('Fetching sessions', { limit, offset, ...options });

    const response = await apiClient.get<SessionListResponse>('/session/list', {
      params: {
        limit,
        offset,
        with_user_messages: options.withUserMessages || undefined,
      },
      signal,
    });

    return response.data;
  },

  /**
   * 结束会话
   *
   * @param sessionId - 会话 ID
   */
  async endSession(sessionId: string, signal?: AbortSignal): Promise<void> {
    sessionLogger.debug('Ending session', { sessionId });

    await apiClient.post(`/session/${sessionId}/end`, undefined, { signal });

    sessionLogger.info('Session ended', { sessionId });
  },

  /**
   * 取消任务
   *
   * @param taskId - 任务 ID
   * @returns 是否成功
   */
  async cancelTask(taskId: string, signal?: AbortSignal): Promise<boolean> {
    sessionLogger.debug('Cancelling task', { taskId });

    const success = await cancelTask(taskId, signal);

    if (success) {
      sessionLogger.info('Task cancelled', { taskId });
    } else {
      sessionLogger.warn('Failed to cancel task', { taskId });
    }

    return success;
  },

  /**
   * 更新会话模式
   *
   * @param sessionId - 会话 ID
   * @param mode - 新模式
   * @returns 更新结果
   */
  async updateSessionMode(
    sessionId: string,
    mode: SessionMode,
    signal?: AbortSignal,
  ): Promise<UpdateModeResponse> {
    sessionLogger.debug('Updating session mode', { sessionId, mode });

    const response = await apiClient.patch<UpdateModeResponse>(
      `/session/${sessionId}/mode`,
      { mode },
      { signal },
    );

    sessionLogger.info('Session mode updated', { sessionId, mode });

    return response.data;
  },

  /**
   * 删除会话
   *
   * @param sessionId - 会话 ID
   * @returns 删除结果
   */
  async deleteSession(sessionId: string, signal?: AbortSignal): Promise<DeleteSessionResponse> {
    sessionLogger.debug('Deleting session', { sessionId });

    const response = await apiClient.delete<DeleteSessionResponse>(
      `/session/${sessionId}`,
      { signal },
    );

    if (response.data.success) {
      sessionLogger.info('Session deleted', { sessionId });
    } else {
      sessionLogger.warn('Failed to delete session', { sessionId });
    }

    return response.data;
  },

  /**
   * 批量删除会话
   *
   * @param sessionIds - 会话 ID 列表
   * @returns 批量删除结果
   */
  async batchDeleteSessions(
    sessionIds: string[],
    signal?: AbortSignal,
  ): Promise<BatchDeleteResponse> {
    sessionLogger.debug('Batch deleting sessions', { count: sessionIds.length });

    const response = await apiClient.post<BatchDeleteResponse>(
      '/session/batch-delete',
      { session_ids: sessionIds },
      { signal },
    );

    if (response.data.success) {
      sessionLogger.info('Sessions batch deleted', { deletedCount: response.data.deleted_count });
    } else {
      sessionLogger.warn('Failed to batch delete sessions');
    }

    return response.data;
  },
};

export default sessionService;
