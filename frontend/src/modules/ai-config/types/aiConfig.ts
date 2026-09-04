/**
 * AI 配置相关类型定义
 */

// ========== 提供商预设类型 ==========

/**
 * 提供商预设配置
 */
export interface ProviderPreset {
  code: string;
  name: string;
  defaultBaseUrl: string;
  models: string[];
}

// ========== 提供商类型 ==========

/**
 * LLM 提供商
 */
export interface LLMProvider {
  id: string;
  name: string;
  code: string;
  base_url: string;
  priority: number;
  weight: number;
  is_active: boolean;
  description: string | null;
  created_at: string;
  updated_at: string;
}

export type ProviderKeyStrategy = 'round_robin' | 'random';

/**
 * 创建提供商请求
 */
export interface CreateProviderRequest {
  name: string;
  code: string;
  base_url: string;
  api_key: string;
  api_keys?: string[];
  key_strategy?: ProviderKeyStrategy;
  priority?: number;
  weight?: number;
  is_active?: boolean;
  description?: string;
}

/**
 * 简化的模型创建（用于批量创建）
 */
export interface ModelCreateSimple {
  model_id: string;
  name?: string;
}

/**
 * 创建提供商并同时创建模型请求
 */
export interface CreateProviderWithModelsRequest {
  name: string;
  code: string;
  base_url: string;
  api_key: string;
  api_keys?: string[];
  key_strategy?: ProviderKeyStrategy;
  priority?: number;
  weight?: number;
  is_active?: boolean;
  description?: string;
  models: ModelCreateSimple[];
}

/**
 * 创建提供商并同时创建模型响应
 */
export interface ProviderWithModelsResponse {
  provider: LLMProvider;
  models: LLMModel[];
  models_count: number;
}

/**
 * 更新提供商请求
 */
export interface UpdateProviderRequest {
  name?: string;
  base_url?: string;
  api_key?: string;
  api_keys?: string[];
  key_strategy?: ProviderKeyStrategy;
  priority?: number;
  weight?: number;
  is_active?: boolean;
  description?: string;
}

/**
 * 提供商连接测试结果
 */
export interface ProviderTestResult {
  success: boolean;
  message: string;
  latency_ms: number;
  model_id?: string | null;
}

/**
 * 获取可用模型列表响应
 */
export interface FetchModelsResponse {
  success: boolean;
  models: string[];
  message: string;
}

/**
 * 模型批量更新请求
 */
export interface ModelsUpdateRequest {
  models: ModelCreateSimple[];
}

/**
 * 模型批量更新响应
 */
export interface ModelsUpdateResponse {
  added: number;
  removed: number;
  unchanged: number;
  models: LLMModel[];
}

// ========== 模型类型 ==========

/**
 * LLM 模型
 */
export interface LLMModel {
  id: string;
  provider_id: string;
  name: string;
  model_id: string;
  default_temperature: number;
  default_max_tokens: number | null;
  /** @deprecated Top P is ignored by the runtime. */
  default_top_p: number | null;
  default_timeout: number;
  default_max_retries: number;
  is_active: boolean;
  is_default: boolean;
  capabilities: Record<string, unknown>;
  description: string | null;
  created_at: string;
  updated_at: string;
  // 关联的提供商信息
  provider_name: string | null;
  provider_code: string | null;
}

/**
 * 创建模型请求
 */
export interface CreateModelRequest {
  provider_id: string;
  name: string;
  model_id: string;
  default_temperature?: number;
  default_max_tokens?: number;
  default_top_p?: number;
  default_timeout?: number;
  default_max_retries?: number;
  capabilities?: Record<string, unknown>;
  description?: string;
}

/**
 * 更新模型请求
 */
export interface UpdateModelRequest {
  name?: string;
  model_id?: string;
  default_temperature?: number;
  default_max_tokens?: number;
  default_top_p?: number;
  default_timeout?: number;
  default_max_retries?: number;
  is_active?: boolean;
  capabilities?: Record<string, unknown>;
  description?: string;
}

// ========== 向量模型配置 ==========

export type EmbeddingMetric = 'cosine' | 'dot' | 'euclid';

export interface EmbeddingModelVersion {
  id: string;
  logical_name: string;
  llm_model_id: string | null;
  provider: string;
  provider_model: string;
  revision: string;
  dimension: number;
  metric: EmbeddingMetric;
  tokenizer: string | null;
  normalization: string | null;
  max_tokens: number;
  send_dimensions: boolean;
  batch_size: number;
  timeout_seconds: number;
  max_retries: number;
  status: 'draft' | 'active' | 'retired';
  metadata: Record<string, unknown>;
  verified_at: string | null;
  activated_at: string | null;
  retired_at: string | null;
  created_at: string;
  updated_at: string;
  provider_name: string | null;
  model_name: string | null;
}

export interface ConfigureEmbeddingRequest {
  model_id: string;
  revision: string;
  dimension: number;
  metric: EmbeddingMetric;
  tokenizer: string;
  normalization: string;
  max_tokens: number;
  send_dimensions: boolean;
  batch_size: number;
  timeout_seconds: number;
  max_retries: number;
}

export interface EmbeddingProbeResult {
  success: boolean;
  message: string;
  latency_ms: number;
  model_id: string;
  provider_model: string;
  observed_dimension: number;
}

// ========== 智能体配置类型 ==========

/**
 * 智能体模型配置
 */
export interface AgentModelConfig {
  id: string;
  agent_type: string;
  model_id: string | null;
  model_key: string | null;
  temperature_override: number | null;
  max_tokens_override: number | null;
  /** @deprecated Top P is ignored by the runtime. */
  top_p_override: number | null;
  timeout_override: number | null;
  max_retries_override: number | null;
  extra_config: Record<string, unknown>;
  is_active: boolean;
  created_at: string;
  updated_at: string;
  // 关联的模型信息
  model_name: string | null;
  model_model_id: string | null;
  provider_name: string | null;
}

/**
 * 更新智能体配置请求
 */
export interface UpdateAgentConfigRequest {
  model_key: string;
  model_id?: string;
  temperature_override?: number | null;
  max_tokens_override?: number | null;
  timeout_override?: number | null;
  max_retries_override?: number | null;
  extra_config?: Record<string, unknown>;
}

/**
 * 智能体类型信息
 */
export interface AgentTypeInfo {
  type: string;
  name: string;
  configured: boolean;
}

// ========== API 响应类型 ==========

/**
 * 列表响应
 */
export interface ListResponse<T> {
  items: T[];
  total: number;
}

/**
 * 成功响应
 */
export interface SuccessResponse {
  success: boolean;
  message: string;
}

/**
 * 错误响应
 */
export interface ErrorResponse {
  success: boolean;
  error: string;
  detail?: string;
}

// ========== 智能体类型常量 ==========

/**
 * 智能体类型枚举
 */
export const AgentTypes = {
  MATH_SOLVER: 'math_solver',
  OCR: 'ocr',
  TUTOR: 'tutor',
  DIAGNOSTICIAN: 'diagnostician',
  PORTRAIT: 'portrait',
  QUESTION_PARSER: 'question_parser',
  QUESTION_GENERATOR: 'question_generator',
  CONTENT_MODERATOR: 'content_moderator',
} as const;

export type AgentType = (typeof AgentTypes)[keyof typeof AgentTypes];

/**
 * 智能体类型显示名称
 */
export const AgentTypeDisplayNames: Record<AgentType, string> = {
  [AgentTypes.MATH_SOLVER]: '数学求解智能体',
  [AgentTypes.OCR]: '图片识别智能体',
  [AgentTypes.TUTOR]: '导师智能体',
  [AgentTypes.DIAGNOSTICIAN]: '诊断智能体',
  [AgentTypes.PORTRAIT]: '学生画像',
  [AgentTypes.QUESTION_PARSER]: '题目解析智能体',
  [AgentTypes.QUESTION_GENERATOR]: '题目生成智能体',
  [AgentTypes.CONTENT_MODERATOR]: '内容审核智能体',
};

/**
 * 所有智能体类型列表
 */
export const AllAgentTypes: AgentType[] = Object.values(AgentTypes);
