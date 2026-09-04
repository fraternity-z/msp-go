/**
 * AI 模型设置页面
 *
 * 管理 AI 渠道（提供商 + 模型）和智能体配置
 */

import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { AdminLayout } from '@/modules/admin/components/AdminLayout';
import { Card, CardContent } from '../../components/ui/Card';
import { Button } from '../../components/ui/Button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/Tabs';
import { useToast } from '../../components/ui/Toast';
import { RequestErrorNotice } from '@/components/feedback';
import {
  Brain,
  Bot,
  Plus,
  RefreshCw,
  Loader2,
  CheckCircle,
  Server,
  DatabaseZap,
} from 'lucide-react';

// 导入 Admin 模块组件
import {
  ChannelFormModal,
  ChannelCard,
  AgentConfigPanel,
  EmbeddingConfigPanel,
} from '@/modules/admin';

// 导入 Hooks 和类型
import { useAppDispatch, useAppSelector } from '../../store';
import {
  fetchProviders,
  fetchModels,
  fetchAgentConfigs,
  fetchAgentTypes,
  createProviderWithModels,
  updateProvider,
  deleteProvider,
  testProviderConnection,
  fetchAvailableModels,
  updateProviderModels,
  updateAgentConfig,
  deleteAgentConfig,
  setDefaultModel,
  selectProviders,
  selectProvidersLoading,
  selectProvidersError,
  selectModels,
  selectModelsLoading,
  selectModelsError,
  selectAgentConfigs,
  selectAgentTypes,
  selectAgentConfigsLoading,
  selectAgentConfigsError,
} from '@/modules/ai-config/store/aiConfigSlice';
import type {
  LLMProvider,
  CreateProviderWithModelsRequest,
  UpdateProviderRequest,
  UpdateAgentConfigRequest,
  ProviderTestResult,
  FetchModelsResponse,
  ModelCreateSimple,
  ModelsUpdateResponse,
} from '@/modules/ai-config/types/aiConfig';
import { getAdminErrorToast } from '@/modules/admin/utils/errorFeedback';

export const AIModelSettingsPage: React.FC = () => {
  const dispatch = useAppDispatch();
  const { toast } = useToast();

  // Redux 状态 (添加防御性默认值)
  const providers = useAppSelector(selectProviders) ?? [];
  const providersLoading = useAppSelector(selectProvidersLoading) ?? 'idle';
  const providersError = useAppSelector(selectProvidersError) ?? null;
  const models = useAppSelector(selectModels);
  const modelsLoading = useAppSelector(selectModelsLoading) ?? 'idle';
  const modelsError = useAppSelector(selectModelsError) ?? null;
  const agentConfigs = useAppSelector(selectAgentConfigs) ?? [];
  const agentTypes = useAppSelector(selectAgentTypes) ?? [];
  const agentConfigsLoading = useAppSelector(selectAgentConfigsLoading) ?? 'idle';
  const agentConfigsError = useAppSelector(selectAgentConfigsError) ?? null;

  // 本地状态
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<LLMProvider | null>(null);
  const [isRefreshing, setIsRefreshing] = useState(false);

  // 初始化加载数据
  useEffect(() => {
    let requests: Array<{ abort: () => void }> = [];
    // Defer dispatch so StrictMode's probe mount is cleaned up before requests begin.
    const timer = window.setTimeout(() => {
      requests = [
        dispatch(fetchProviders(true)),
        dispatch(fetchModels({ includeInactive: true })),
        dispatch(fetchAgentConfigs()),
        dispatch(fetchAgentTypes()),
      ];
    }, 0);
    return () => {
      window.clearTimeout(timer);
      requests.forEach((request) => request.abort());
    };
  }, [dispatch]);

  // 刷新数据
  const handleRefresh = async () => {
    setIsRefreshing(true);
    try {
      await Promise.all([
        dispatch(fetchProviders(true)),
        dispatch(fetchModels({ includeInactive: true })),
        dispatch(fetchAgentConfigs()),
        dispatch(fetchAgentTypes()),
      ]);
    } finally {
      setIsRefreshing(false);
    }
  };

  // 打开新建 Modal
  const handleOpenCreateModal = () => {
    setEditingProvider(null);
    setIsModalOpen(true);
  };

  // 打开编辑 Modal
  const handleOpenEditModal = (provider: LLMProvider) => {
    setEditingProvider(provider);
    setIsModalOpen(true);
  };

  // 关闭 Modal
  const handleCloseModal = () => {
    setIsModalOpen(false);
    setEditingProvider(null);
  };

  // 创建渠道（提供商 + 模型）
  const handleCreateChannel = async (data: CreateProviderWithModelsRequest) => {
    return dispatch(createProviderWithModels(data)).unwrap();
  };

  // 更新提供商
  const handleUpdateProvider = async (id: string, data: UpdateProviderRequest) => {
    await dispatch(updateProvider({ id, data })).unwrap();
  };

  // 删除提供商
  const handleDeleteProvider = async (provider: LLMProvider) => {
    if (window.confirm(`确定要删除渠道 "${provider.name}" 吗？这将同时删除所有关联的模型。`)) {
      try {
        await dispatch(deleteProvider(provider.id)).unwrap();
      } catch (error) {
        const feedback = getAdminErrorToast(error, '删除渠道失败');
        if (feedback) toast(feedback);
      }
    }
  };

  // 切换提供商状态
  const handleToggleProviderActive = async (provider: LLMProvider) => {
    try {
      await dispatch(
        updateProvider({
          id: provider.id,
          data: { is_active: !provider.is_active },
        })
      ).unwrap();
    } catch (error) {
      const feedback = getAdminErrorToast(error, '更新渠道状态失败');
      if (feedback) toast(feedback);
    }
  };

  // 测试连接
  const handleTestConnection = useCallback(
    async (provider: LLMProvider, modelId?: string): Promise<ProviderTestResult> => {
      return dispatch(testProviderConnection({ id: provider.id, modelId })).unwrap();
    },
    [dispatch]
  );

  // 获取可用模型列表
  const handleFetchModels = useCallback(
    async (providerId: string): Promise<FetchModelsResponse> => {
      return dispatch(fetchAvailableModels(providerId)).unwrap();
    },
    [dispatch]
  );

  // 更新提供商的模型列表
  const handleUpdateProviderModels = useCallback(
    async (providerId: string, models: ModelCreateSimple[]): Promise<ModelsUpdateResponse> => {
      return dispatch(updateProviderModels({ providerId, data: { models } })).unwrap();
    },
    [dispatch]
  );

  // 设置默认模型
  const handleSetDefaultModel = async (modelId: string) => {
    try {
      await dispatch(setDefaultModel(modelId)).unwrap();
    } catch (error) {
      const feedback = getAdminErrorToast(error, '设置默认模型失败');
      if (feedback) toast(feedback);
    }
  };

  // 更新智能体配置
  const handleUpdateAgentConfig = async (agentType: string, data: UpdateAgentConfigRequest) => {
    await dispatch(updateAgentConfig({ agentType, data })).unwrap();
  };

  // 删除智能体配置
  const handleDeleteAgentConfig = async (agentType: string) => {
    await dispatch(deleteAgentConfig(agentType)).unwrap();
  };

  // 获取提供商的模型
  const getProviderModels = (providerId: string) => {
    return models.filter((m) => m.provider_id === providerId);
  };

  const editingProviderModels = useMemo(
    () => editingProvider ? models.filter((model) => model.provider_id === editingProvider.id) : [],
    [editingProvider, models]
  );

  // 统计数据
  const stats = {
    totalProviders: providers.length,
    activeProviders: providers.filter((p) => p.is_active).length,
    totalModels: models.length,
    configuredAgents: agentConfigs.length,
  };

  // 加载状态
  const isLoading = providersLoading === 'loading' || modelsLoading === 'loading';

  return (
    <AdminLayout>
      <div className="container mx-auto max-w-7xl">
        {/* 页面标题 */}
        <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="text-3xl font-bold text-surface-900 dark:text-surface-100 mb-2">
              AI 模型设置
            </h1>
            <p className="text-surface-500 dark:text-surface-400">
              配置和管理系统使用的 AI 渠道和智能体
            </p>
          </div>
          <div className="flex w-full gap-3 sm:w-auto">
            <Button className="flex-1 whitespace-nowrap sm:flex-none" variant="outline" onClick={handleRefresh} disabled={isRefreshing}>
              {isRefreshing ? (
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
              ) : (
                <RefreshCw className="w-4 h-4 mr-2" />
              )}
              刷新
            </Button>
            <Button className="flex-1 whitespace-nowrap sm:flex-none" onClick={handleOpenCreateModal}>
              <Plus className="w-4 h-4 mr-2" />
              添加渠道
            </Button>
          </div>
        </div>

        {/* 统计卡片 */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6 mb-8">
          <Card>
            <CardContent className="pt-6">
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-2xl font-bold text-surface-900 dark:text-surface-100">
                    {stats.totalProviders}
                  </div>
                  <div className="text-sm text-surface-500 dark:text-surface-400 mt-1">
                    渠道总数
                  </div>
                </div>
                <Server className="w-8 h-8 text-primary-600 dark:text-primary-400" />
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-6">
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-2xl font-bold text-emerald-600 dark:text-emerald-400">
                    {stats.activeProviders}
                  </div>
                  <div className="text-sm text-surface-500 dark:text-surface-400 mt-1">
                    运行中
                  </div>
                </div>
                <CheckCircle className="w-8 h-8 text-emerald-600 dark:text-emerald-400" />
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-6">
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-2xl font-bold text-surface-900 dark:text-surface-100">
                    {stats.totalModels}
                  </div>
                  <div className="text-sm text-surface-500 dark:text-surface-400 mt-1">
                    模型总数
                  </div>
                </div>
                <Brain className="w-8 h-8 text-blue-600 dark:text-blue-400" />
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-6">
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-2xl font-bold text-surface-900 dark:text-surface-100">
                    {stats.configuredAgents}
                  </div>
                  <div className="text-sm text-surface-500 dark:text-surface-400 mt-1">
                    已配置智能体
                  </div>
                </div>
                <Bot className="w-8 h-8 text-purple-600 dark:text-purple-400" />
              </div>
            </CardContent>
          </Card>
        </div>

        {/* 错误提示 */}
        {providersError && (
          <RequestErrorNotice
            error={providersError}
            onRetry={() => { void dispatch(fetchProviders(true)); }}
            onRefresh={() => { void dispatch(fetchProviders(true)); }}
            className="mb-6"
          />
        )}

        {/* 主要内容 */}
        <Tabs defaultValue="channels" className="space-y-6">
          <div className="overflow-x-auto pb-1">
            <TabsList className="min-w-max">
              <TabsTrigger value="channels">
                <Server className="w-4 h-4 mr-2" />
                渠道管理
              </TabsTrigger>
              <TabsTrigger value="agents">
                <Bot className="w-4 h-4 mr-2" />
                智能体配置
              </TabsTrigger>
              <TabsTrigger value="embeddings">
                <DatabaseZap className="w-4 h-4 mr-2" />
                向量模型
              </TabsTrigger>
            </TabsList>
          </div>

          {/* 渠道管理 Tab */}
          <TabsContent value="channels" className="space-y-4">
            {modelsError ? (
              <RequestErrorNotice
                error={modelsError}
                onRetry={() => { void dispatch(fetchModels({ includeInactive: true })); }}
                onRefresh={() => { void dispatch(fetchModels({ includeInactive: true })); }}
              />
            ) : null}
            {isLoading ? (
              <div className="flex items-center justify-center py-12">
                <Loader2 className="w-8 h-8 animate-spin text-primary-600" />
              </div>
            ) : providers.length === 0 ? (
              <Card>
                <CardContent className="py-12">
                  <div className="text-center">
                    <Server className="w-12 h-12 mx-auto mb-4 text-surface-400 dark:text-surface-500" />
                    <h3 className="text-lg font-medium text-surface-900 dark:text-surface-100 mb-2">
                      暂无渠道
                    </h3>
                    <p className="text-surface-500 dark:text-surface-400 mb-4">
                      点击"添加渠道"按钮创建您的第一个 AI 渠道
                    </p>
                    <Button onClick={handleOpenCreateModal}>
                      <Plus className="w-4 h-4 mr-2" />
                      添加渠道
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-4">
                {providers.map((provider) => (
                  <ChannelCard
                    key={provider.id}
                    provider={provider}
                    models={getProviderModels(provider.id)}
                    onEdit={handleOpenEditModal}
                    onDelete={handleDeleteProvider}
                    onToggleActive={handleToggleProviderActive}
                    onTestConnection={handleTestConnection}
                    onSetDefaultModel={handleSetDefaultModel}
                  />
                ))}
              </div>
            )}
          </TabsContent>

          {/* 智能体配置 Tab */}
          <TabsContent value="agents">
            {agentConfigsError ? (
              <RequestErrorNotice
                error={agentConfigsError}
                onRetry={() => {
                  void dispatch(fetchAgentConfigs());
                  void dispatch(fetchAgentTypes());
                }}
                onRefresh={() => {
                  void dispatch(fetchAgentConfigs());
                  void dispatch(fetchAgentTypes());
                }}
                className="mb-4"
              />
            ) : null}
            <AgentConfigPanel
              agentTypes={agentTypes}
              agentConfigs={agentConfigs}
              providers={providers}
              models={models}
              onUpdateConfig={handleUpdateAgentConfig}
              onDeleteConfig={handleDeleteAgentConfig}
              loading={agentConfigsLoading === 'loading'}
            />
          </TabsContent>

          <TabsContent value="embeddings">
            <EmbeddingConfigPanel providers={providers} models={models} />
          </TabsContent>
        </Tabs>

        {/* 渠道表单 Modal */}
        <ChannelFormModal
          isOpen={isModalOpen}
          onClose={handleCloseModal}
          onSubmit={handleCreateChannel}
          onUpdate={handleUpdateProvider}
          onUpdateModels={handleUpdateProviderModels}
          onFetchModels={handleFetchModels}
          editingProvider={editingProvider}
          existingModels={editingProviderModels}
        />
      </div>
    </AdminLayout>
  );
};

export default AIModelSettingsPage;
