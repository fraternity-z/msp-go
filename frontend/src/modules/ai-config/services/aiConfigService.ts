/**
 * AI 配置 API 服务
 *
 * 提供 AI 模型配置的 API 调用
 */

import { apiClient } from '@/libs/http/apiClient';
import { logger } from '@/libs/utils/logger';
import type {
  LLMProvider,
  LLMModel,
  AgentModelConfig,
  AgentTypeInfo,
  CreateProviderRequest,
  UpdateProviderRequest,
  CreateProviderWithModelsRequest,
  ProviderWithModelsResponse,
  CreateModelRequest,
  UpdateModelRequest,
  UpdateAgentConfigRequest,
  ProviderTestResult,
  FetchModelsResponse,
  ModelsUpdateRequest,
  ModelsUpdateResponse,
  EmbeddingModelVersion,
  ConfigureEmbeddingRequest,
  EmbeddingProbeResult,
  ListResponse,
  SuccessResponse,
} from '@/modules/ai-config/types/aiConfig';

// 创建 AI 配置专用日志记录器
const aiConfigLogger = logger.createContextLogger('AIConfig');

// API 基础路径
const BASE_PATH = '/admin/ai-config';

/**
 * AI 配置 API 服务
 */
export const aiConfigService = {
  // ========== 提供商管理 ==========

  /**
   * 获取提供商列表
   */
  async listProviders(includeInactive = false, signal?: AbortSignal): Promise<ListResponse<LLMProvider>> {
    try {
      const response = await apiClient.get<ListResponse<LLMProvider>>(
        `${BASE_PATH}/providers`,
        { params: { include_inactive: includeInactive }, signal }
      );
      aiConfigLogger.debug('获取提供商列表成功', { total: response.data.total });
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('获取提供商列表失败', error);
      throw error;
    }
  },

  /**
   * 获取单个提供商
   */
  async getProvider(providerId: string): Promise<LLMProvider> {
    try {
      const response = await apiClient.get<LLMProvider>(
        `${BASE_PATH}/providers/${providerId}`
      );
      return response.data;
    } catch (error) {
      aiConfigLogger.error('获取提供商失败', { providerId, error });
      throw error;
    }
  },

  /**
   * 创建提供商
   */
  async createProvider(data: CreateProviderRequest): Promise<LLMProvider> {
    try {
      const response = await apiClient.post<LLMProvider>(
        `${BASE_PATH}/providers`,
        data
      );
      aiConfigLogger.info('创建提供商成功', { id: response.data.id, name: response.data.name });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('创建提供商失败', { name: data.name, error });
      throw error;
    }
  },

  /**
   * 更新提供商
   */
  async updateProvider(providerId: string, data: UpdateProviderRequest): Promise<LLMProvider> {
    try {
      const response = await apiClient.put<LLMProvider>(
        `${BASE_PATH}/providers/${providerId}`,
        data
      );
      aiConfigLogger.info('更新提供商成功', { id: providerId });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('更新提供商失败', { providerId, error });
      throw error;
    }
  },

  /**
   * 删除提供商
   */
  async deleteProvider(providerId: string): Promise<SuccessResponse> {
    try {
      const response = await apiClient.delete<SuccessResponse>(
        `${BASE_PATH}/providers/${providerId}`
      );
      aiConfigLogger.info('删除提供商成功', { id: providerId });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('删除提供商失败', { providerId, error });
      throw error;
    }
  },

  /**
   * 测试提供商连接
   */
  async testProvider(providerId: string, modelId?: string): Promise<ProviderTestResult> {
    try {
      const params: Record<string, string> = {};
      if (modelId) {
        params.model_id = modelId;
      }
      const response = await apiClient.post<ProviderTestResult>(
        `${BASE_PATH}/providers/${providerId}/test`,
        null,
        { params }
      );
      aiConfigLogger.info('测试提供商连接', {
        id: providerId,
        success: response.data.success,
        latency: response.data.latency_ms,
        model_id: modelId,
      });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('测试提供商连接失败', { providerId, error });
      throw error;
    }
  },

  /**
   * 创建提供商并同时创建模型
   */
  async createProviderWithModels(data: CreateProviderWithModelsRequest): Promise<ProviderWithModelsResponse> {
    try {
      const response = await apiClient.post<ProviderWithModelsResponse>(
        `${BASE_PATH}/providers/with-models`,
        data
      );
      aiConfigLogger.info('创建提供商和模型成功', {
        providerId: response.data.provider.id,
        providerName: response.data.provider.name,
        modelsCount: response.data.models_count,
      });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('创建提供商和模型失败', { name: data.name, error });
      throw error;
    }
  },

  /**
   * 从提供商 API 获取可用模型列表
   */
  async fetchAvailableModels(providerId: string): Promise<FetchModelsResponse> {
    try {
      const response = await apiClient.get<FetchModelsResponse>(
        `${BASE_PATH}/providers/${providerId}/fetch-models`
      );
      aiConfigLogger.info('获取可用模型列表', {
        providerId,
        success: response.data.success,
        modelsCount: response.data.models.length,
      });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('获取可用模型列表失败', { providerId, error });
      throw error;
    }
  },

  /**
   * 根据凭据获取可用模型列表（用于新建渠道时）
   */
  async fetchModelsByCredentials(baseUrl: string, apiKey: string): Promise<FetchModelsResponse> {
    try {
      const response = await apiClient.post<FetchModelsResponse>(
        `${BASE_PATH}/channels/fetch-models`,
        { base_url: baseUrl, api_key: apiKey }
      );
      aiConfigLogger.info('根据凭据获取可用模型列表', {
        success: response.data.success,
        modelsCount: response.data.models.length,
      });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('根据凭据获取可用模型列表失败', { error });
      throw error;
    }
  },

  /**
   * 更新提供商的模型列表（全量替换）
   */
  async updateProviderModels(providerId: string, data: ModelsUpdateRequest): Promise<ModelsUpdateResponse> {
    try {
      const response = await apiClient.put<ModelsUpdateResponse>(
        `${BASE_PATH}/providers/${providerId}/models`,
        data
      );
      aiConfigLogger.info('更新提供商模型列表', {
        providerId,
        added: response.data.added,
        removed: response.data.removed,
        unchanged: response.data.unchanged,
      });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('更新提供商模型列表失败', { providerId, error });
      throw error;
    }
  },

  // ========== 模型管理 ==========

  /**
   * 获取模型列表
   */
  async listModels(providerId?: string, includeInactive = false, signal?: AbortSignal): Promise<ListResponse<LLMModel>> {
    try {
      const params: Record<string, unknown> = { include_inactive: includeInactive };
      if (providerId) {
        params.provider_id = providerId;
      }
      const response = await apiClient.get<ListResponse<LLMModel>>(
        `${BASE_PATH}/models`,
        { params, signal }
      );
      aiConfigLogger.debug('获取模型列表成功', { total: response.data.total });
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('获取模型列表失败', error);
      throw error;
    }
  },

  /**
   * 获取单个模型
   */
  async getModel(modelId: string): Promise<LLMModel> {
    try {
      const response = await apiClient.get<LLMModel>(
        `${BASE_PATH}/models/${modelId}`
      );
      return response.data;
    } catch (error) {
      aiConfigLogger.error('获取模型失败', { modelId, error });
      throw error;
    }
  },

  /**
   * 创建模型
   */
  async createModel(data: CreateModelRequest): Promise<LLMModel> {
    try {
      const response = await apiClient.post<LLMModel>(
        `${BASE_PATH}/models`,
        data
      );
      aiConfigLogger.info('创建模型成功', { id: response.data.id, name: response.data.name });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('创建模型失败', { name: data.name, error });
      throw error;
    }
  },

  /**
   * 更新模型
   */
  async updateModel(modelId: string, data: UpdateModelRequest): Promise<LLMModel> {
    try {
      const response = await apiClient.put<LLMModel>(
        `${BASE_PATH}/models/${modelId}`,
        data
      );
      aiConfigLogger.info('更新模型成功', { id: modelId });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('更新模型失败', { modelId, error });
      throw error;
    }
  },

  /**
   * 删除模型
   */
  async deleteModel(modelId: string): Promise<SuccessResponse> {
    try {
      const response = await apiClient.delete<SuccessResponse>(
        `${BASE_PATH}/models/${modelId}`
      );
      aiConfigLogger.info('删除模型成功', { id: modelId });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('删除模型失败', { modelId, error });
      throw error;
    }
  },

  /**
   * 设置默认模型
   */
  async setDefaultModel(modelId: string): Promise<SuccessResponse> {
    try {
      const response = await apiClient.post<SuccessResponse>(
        `${BASE_PATH}/models/${modelId}/set-default`
      );
      aiConfigLogger.info('设置默认模型成功', { id: modelId });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('设置默认模型失败', { modelId, error });
      throw error;
    }
  },

  // ========== 向量模型配置 ==========

  async listEmbeddingModels(signal?: AbortSignal): Promise<ListResponse<EmbeddingModelVersion>> {
    try {
      const response = await apiClient.get<ListResponse<EmbeddingModelVersion>>(
        `${BASE_PATH}/embeddings`,
        { signal }
      );
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('获取向量模型配置失败', error);
      throw error;
    }
  },

  async testEmbeddingModel(data: ConfigureEmbeddingRequest, signal?: AbortSignal): Promise<EmbeddingProbeResult> {
    try {
      const response = await apiClient.post<EmbeddingProbeResult>(
        `${BASE_PATH}/embeddings/test`,
        data,
        { signal }
      );
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('测试向量模型失败', { modelId: data.model_id, error });
      throw error;
    }
  },

  async activateEmbeddingModel(data: ConfigureEmbeddingRequest, signal?: AbortSignal): Promise<EmbeddingModelVersion> {
    try {
      const response = await apiClient.put<EmbeddingModelVersion>(
        `${BASE_PATH}/embeddings/active`,
        data,
        { signal }
      );
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('激活向量模型失败', { modelId: data.model_id, error });
      throw error;
    }
  },

  // ========== 智能体配置管理 ==========

  /**
   * 获取智能体配置列表
   */
  async listAgentConfigs(signal?: AbortSignal): Promise<ListResponse<AgentModelConfig>> {
    try {
      const response = await apiClient.get<ListResponse<AgentModelConfig>>(
        `${BASE_PATH}/agents`,
        { signal }
      );
      aiConfigLogger.debug('获取智能体配置列表成功', { total: response.data.total });
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('获取智能体配置列表失败', error);
      throw error;
    }
  },

  /**
   * 获取智能体类型列表
   */
  async listAgentTypes(signal?: AbortSignal): Promise<{ items: AgentTypeInfo[] }> {
    try {
      const response = await apiClient.get<{ items: AgentTypeInfo[] }>(
        `${BASE_PATH}/agents/types`,
        { signal }
      );
      return response.data;
    } catch (error) {
      if (!signal?.aborted) aiConfigLogger.error('获取智能体类型列表失败', error);
      throw error;
    }
  },

  /**
   * 获取单个智能体配置
   */
  async getAgentConfig(agentType: string): Promise<AgentModelConfig> {
    try {
      const response = await apiClient.get<AgentModelConfig>(
        `${BASE_PATH}/agents/${agentType}`
      );
      return response.data;
    } catch (error) {
      aiConfigLogger.error('获取智能体配置失败', { agentType, error });
      throw error;
    }
  },

  /**
   * 更新智能体配置
   */
  async updateAgentConfig(
    agentType: string,
    data: UpdateAgentConfigRequest
  ): Promise<AgentModelConfig> {
    try {
      const response = await apiClient.put<AgentModelConfig>(
        `${BASE_PATH}/agents/${agentType}`,
        data
      );
      aiConfigLogger.info('更新智能体配置成功', { agentType });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('更新智能体配置失败', { agentType, error });
      throw error;
    }
  },

  /**
   * 删除智能体配置
   */
  async deleteAgentConfig(agentType: string): Promise<SuccessResponse> {
    try {
      const response = await apiClient.delete<SuccessResponse>(
        `${BASE_PATH}/agents/${agentType}`
      );
      aiConfigLogger.info('删除智能体配置成功', { agentType });
      return response.data;
    } catch (error) {
      aiConfigLogger.error('删除智能体配置失败', { agentType, error });
      throw error;
    }
  },
};

export default aiConfigService;
