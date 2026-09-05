/**
 * SSE 客户端封装
 *
 * 提供 POST 请求的 SSE 流式连接支持
 */

import { logger } from '../utils/logger';
import { authTokenStorage } from '../auth/tokenStorage';
import {
  isRequestCancelled,
  parseRetryAfterSeconds,
  toAppError,
  type AppError,
} from './appError';

const sseLogger = logger.createContextLogger('SSE');
const MAX_SSE_BUFFER_CHARS = 1024 * 1024;
const MAX_SSE_EVENT_DATA_CHARS = 1024 * 1024;

export type SSEError = AppError & { code: string };

interface StructuredSSEError {
  code: string;
  message: string;
  requestId?: string;
  retryAfter?: number;
  source: 'sse';
  status?: number;
}

type SSEResponseMetadata = Pick<StructuredSSEError, 'requestId' | 'retryAfter'>;

class SSERequestError extends Error {
  readonly code: string;
  readonly requestId?: string;
  readonly retryAfter?: number;
  readonly status: number;

  constructor(
    code: string,
    message: string,
    status: number,
    requestId?: string,
    retryAfter?: number
  ) {
    super(message);
    this.name = 'SSERequestError';
    this.code = code;
    this.status = status;
    this.requestId = requestId;
    this.retryAfter = retryAfter;
  }
}

/**
 * SSE 事件处理器
 */
export interface SSEHandlers {
  /** 收到内容块 */
  onChunk?: (content: string, agent: string | null, messageId: string) => void;
  /** 流式响应完成 */
  onDone?: (messageId: string, agent: string | null, knowledge?: unknown) => void;
  /** 发生错误 */
  onError?: (error: SSEError) => void;
  /** 任务被取消 */
  onCancelled?: (messageId: string) => void;
  /** 收到任务信息 */
  onTaskInfo?: (taskId: string) => void;
  /** 首次聊天已原子创建服务端会话 */
  onSessionInfo?: (sessionId: string) => void;
  /** 连接打开 */
  onOpen?: () => void;
  /** 连接关闭 */
  onClose?: () => void;
}

/**
 * SSE 连接控制器
 */
export interface SSEController {
  /** 关闭连接 */
  close: () => void;
  /** 获取任务 ID */
  getTaskId: () => string | null;
}

/**
 * 创建 SSE 连接（使用 fetch + ReadableStream）
 *
 * 由于标准 EventSource 不支持 POST 请求和自定义 headers，
 * 这里使用 fetch API 实现 SSE 客户端
 *
 * @param url - SSE 端点 URL
 * @param body - 请求体
 * @param handlers - 事件处理器
 * @returns SSE 控制器
 */
export function createSSEConnection(
  url: string,
  body: object,
  handlers: SSEHandlers
): SSEController {
  let taskId: string | null = null;
  let abortController: AbortController | null = new AbortController();
  let isClosed = false;
  let responseMetadata: SSEResponseMetadata = {};

  const close = () => {
    if (isClosed) return;
    isClosed = true;
    abortController?.abort();
    abortController = null;
    handlers.onClose?.();
    sseLogger.debug('SSE connection closed');
  };

  // 启动连接
  (async () => {
    try {
      const token = authTokenStorage.get();

      const response = await fetch(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Accept': 'text/event-stream',
          ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
        },
        body: JSON.stringify(body),
        signal: abortController?.signal,
        credentials: 'include',
      });
      responseMetadata = {
        requestId: response.headers.get('X-Request-ID')?.trim() || undefined,
        retryAfter: parseRetryAfterSeconds(response.headers.get('Retry-After')),
      };

      if (!response.ok) {
        const rawError: unknown = await response.json().catch(() => ({}));
        const errorData = isRecord(rawError) ? rawError : {};
        throw new SSERequestError(
          sseErrorCode(errorData, `HTTP_${response.status}`),
          sseErrorMessage(errorData) || `HTTP ${response.status}`,
          response.status,
          responseMetadata.requestId,
          responseMetadata.retryAfter
        );
      }

      handlers.onOpen?.();
      sseLogger.debug('SSE connection opened', { url });

      const reader = response.body?.getReader();
      if (!reader) {
        throw new Error('Response body is not readable');
      }

      const decoder = new TextDecoder();
      let buffer = '';
      // 将 currentEvent 和 currentData 移到 while 循环外部
      // 以正确处理跨 chunk 的 SSE 事件
      let currentEvent = '';
      let currentData = '';

      while (!isClosed) {
        const { done, value } = await reader.read();

        if (done) {
          sseLogger.debug('SSE stream ended');
          // 处理最后可能未完成的事件
          if (currentData) {
            processEvent(currentEvent, currentData, handlers, (id) => {
              taskId = id;
            }, responseMetadata);
          }
          break;
        }

        const chunk = decoder.decode(value, { stream: true });
        buffer += chunk;

        // 解析 SSE 事件 - 统一处理 \r\n 和 \n 换行符
        const lines = buffer.split('\n');
        buffer = lines.pop() || ''; // 保留不完整的行
        assertWithinLimit(buffer, MAX_SSE_BUFFER_CHARS, 'SSE buffer exceeded size limit');

        for (const rawLine of lines) {
          // 移除行末的 \r（处理 Windows 风格换行符 \r\n）
          const line = rawLine.replace(/\r$/, '');

          if (line.startsWith('event:')) {
            currentEvent = line.slice(6).trim();
          } else if (line.startsWith('data:')) {
            currentData = appendEventDataLine(currentData, line.slice(5).trim());
          } else if (line === '' && currentData) {
            // 空行表示事件结束
            processEvent(currentEvent, currentData, handlers, (id) => {
              taskId = id;
            }, responseMetadata);
            currentEvent = '';
            currentData = '';
          }
        }
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        sseLogger.debug('SSE connection aborted');
        return;
      }

      sseLogger.error('SSE connection error', error);
      if (error instanceof SSERequestError) {
        handlers.onError?.(asSSEError({
          code: error.code,
          message: error.message,
          status: error.status,
          requestId: error.requestId,
          retryAfter: error.retryAfter,
          source: 'sse',
        }));
      } else {
        handlers.onError?.(asSSEError({
          code: 'CONNECTION_ERROR',
          message: '连接中断，请检查网络后重试',
          ...responseMetadata,
          source: 'sse',
        }));
      }
    } finally {
      close();
    }
  })();

  return {
    close,
    getTaskId: () => taskId,
  };
}

function assertWithinLimit(value: string, maxLength: number, message: string): void {
  if (value.length > maxLength) {
    throw new Error(message);
  }
}

function appendEventDataLine(currentData: string, lineData: string): string {
  const nextData = currentData ? `${currentData}\n${lineData}` : lineData;
  assertWithinLimit(nextData, MAX_SSE_EVENT_DATA_CHARS, 'SSE event exceeded size limit');
  return nextData;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function stringValue(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback;
}

function asSSEError(error: StructuredSSEError): SSEError {
  return { ...toAppError(error), code: error.code };
}

function sseErrorCode(payload: Record<string, unknown>, fallback: string): string {
  const nested = isRecord(payload.error) ? payload.error : undefined;
  return stringValue(payload.code)
    || (nested ? stringValue(nested.code) : '')
    || fallback;
}

function sseErrorMessage(payload: Record<string, unknown>): string {
  const nested = isRecord(payload.error) ? payload.error : undefined;
  const detail = payload.detail;
  if (typeof detail === 'string' && detail.trim()) return detail.trim();
  if (Array.isArray(detail)) {
    for (const item of detail) {
      if (!isRecord(item)) continue;
      const message = stringValue(item.msg).trim();
      if (message) return message;
    }
  }
  return stringValue(payload.message).trim()
    || (nested ? stringValue(nested.message).trim() : '');
}

function metadataForPayload(
  payload: Record<string, unknown>,
  fallback: SSEResponseMetadata
): SSEResponseMetadata {
  const nested = isRecord(payload.error) ? payload.error : undefined;
  const requestId = stringValue(payload.request_id).trim()
    || stringValue(payload.requestId).trim()
    || (nested
      ? stringValue(nested.request_id).trim() || stringValue(nested.requestId).trim()
      : '')
    || fallback.requestId;
  const retryAfterValue = payload.retry_after ?? payload.retryAfter
    ?? nested?.retry_after ?? nested?.retryAfter;
  const retryAfter = parseRetryAfterSeconds(retryAfterValue) ?? fallback.retryAfter;
  return {
    ...(requestId ? { requestId } : {}),
    ...(retryAfter !== undefined ? { retryAfter } : {}),
  };
}

function nullableStringValue(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function isStreamChunkType(value: string): boolean {
  return value === 'chunk' || value === 'stream';
}

function handleMessageEvent(parsed: Record<string, unknown>, handlers: SSEHandlers): void {
  const type = stringValue(parsed.type);
  if (isStreamChunkType(type)) {
    handlers.onChunk?.(
      stringValue(parsed.content),
      nullableStringValue(parsed.agent),
      stringValue(parsed.message_id)
    );
  } else if (type === 'done') {
    handlers.onDone?.(stringValue(parsed.message_id), nullableStringValue(parsed.agent), parsed.knowledge);
  }
}

/**
 * 处理 SSE 事件
 */
function processEvent(
  event: string,
  data: string,
  handlers: SSEHandlers,
  setTaskId: (id: string) => void,
  metadata: SSEResponseMetadata
): void {
  try {
    const parsed: unknown = JSON.parse(data);
    if (!isRecord(parsed)) {
      sseLogger.warn('Ignored invalid SSE event payload', {
        event,
        dataLength: data.length,
      });
      return;
    }

    switch (event || stringValue(parsed.type)) {
      case 'task_info':
        if (typeof parsed.task_id === 'string' && parsed.task_id) {
          setTaskId(parsed.task_id);
          handlers.onTaskInfo?.(parsed.task_id);
        }
        break;

      case 'session_info':
        if (typeof parsed.session_id === 'string' && parsed.session_id) {
          handlers.onSessionInfo?.(parsed.session_id);
        }
        break;

      case 'message':
        handleMessageEvent(parsed, handlers);
        break;

      case 'error':
        handlers.onError?.(asSSEError({
          code: sseErrorCode(parsed, 'UNKNOWN_ERROR'),
          message: sseErrorMessage(parsed) || '未知错误',
          ...metadataForPayload(parsed, metadata),
          source: 'sse',
        }));
        break;

      case 'cancelled':
        handlers.onCancelled?.(
          stringValue(parsed.message_id) || stringValue(parsed.task_id)
        );
        break;

      default:
        // 处理没有 event 字段的消息
        if (isStreamChunkType(stringValue(parsed.type)) || parsed.type === 'done') {
          handleMessageEvent(parsed, handlers);
        } else if (parsed.type === 'error') {
          handlers.onError?.(asSSEError({
            code: sseErrorCode(parsed, 'UNKNOWN_ERROR'),
            message: sseErrorMessage(parsed) || '未知错误',
            ...metadataForPayload(parsed, metadata),
            source: 'sse',
          }));
        }
    }
  } catch (e) {
    sseLogger.warn('Failed to parse SSE event', {
      event,
      dataLength: data.length,
      error: e,
    });
  }
}

/**
 * 取消任务
 *
 * @param taskId - 任务 ID
 * @returns 是否成功
 */
export async function cancelTask(taskId: string, signal?: AbortSignal): Promise<boolean> {
  try {
    const normalizedTaskId = taskId.trim();
    if (!normalizedTaskId) {
      return false;
    }

    const token = authTokenStorage.get();
    const encodedTaskId = encodeURIComponent(normalizedTaskId);

    const response = await fetch(`/api/v1/session/task/${encodedTaskId}/cancel`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      },
      credentials: 'include',
      signal,
    });

    const requestId = response.headers.get('X-Request-ID')?.trim() || undefined;
    const retryAfter = parseRetryAfterSeconds(response.headers.get('Retry-After'));
    const rawResult: unknown = await response.json().catch(() => ({}));
    const result = isRecord(rawResult) ? rawResult : {};

    if (!response.ok) {
      throw toAppError({
        status: response.status,
        code: stringValue(result.code, `HTTP_${response.status}`),
        message: stringValue(result.message)
          || stringValue(result.detail)
          || '取消任务失败',
        requestId,
        retryAfter,
        source: 'http',
      }, '取消任务失败');
    }

    return result.success === true;
  } catch (error) {
    if (isRequestCancelled(error)) throw error;
    sseLogger.error('Failed to cancel task', { taskId, error });
    const appError = toAppError(error, '取消任务失败');
    if (appError.kind === 'unknown') {
      throw toAppError({
        code: 'NETWORK_ERROR',
        message: '无法连接到服务器，请检查网络',
        source: 'http',
      }, '取消任务失败');
    }
    throw appError;
  }
}
