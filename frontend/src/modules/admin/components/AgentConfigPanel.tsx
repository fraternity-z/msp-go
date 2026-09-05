/**
 * 智能体配置面板
 *
 * 用于配置各智能体使用的模型和参数覆盖
 */

import React, { useState, useEffect, useMemo } from 'react';
import {
  Bot,
  Save,
  RotateCcw,
  ChevronDown,
  ChevronUp,
  CheckCircle,
  Settings,
  Loader2,
} from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { Badge } from '@/components/ui/Badge';
import { Card } from '@/components/ui/Card';
import { useToast } from '@/components/ui/Toast';
import { RequestErrorNotice } from '@/components/feedback';
import type { AppError } from '@/libs/http/apiClient';
import { AgentTypeDisplayNames } from '@/modules/ai-config/types/aiConfig';
import type {
  AgentModelConfig,
  AgentTypeInfo,
  LLMModel,
  LLMProvider,
  UpdateAgentConfigRequest,
} from '@/modules/ai-config/types/aiConfig';
import { buildLogicalModelOptions, resolveAgentModelKey } from './agentModelOptions';
import { isAdminRequestCancelled, toAdminAppError } from '@/modules/admin/utils/errorFeedback';

interface AgentConfigPanelProps {
  agentTypes: AgentTypeInfo[];
  agentConfigs: AgentModelConfig[];
  providers: LLMProvider[];
  models: LLMModel[];
  onUpdateConfig: (agentType: string, data: UpdateAgentConfigRequest) => Promise<void>;
  onDeleteConfig: (agentType: string) => Promise<void>;
  loading?: boolean;
}

interface AgentConfigFormData {
  model_key: string;
  temperature_override: string;
  max_tokens_override: string;
  timeout_override: string;
  max_retries_override: string;
}

const parameterOverrideDefaults = {
  temperature: '1.0',
  maxTokens: '4096',
  timeoutSeconds: '1800',
  maxRetries: '3',
} as const;

const defaultFormData: AgentConfigFormData = {
  model_key: '',
  temperature_override: '',
  max_tokens_override: '',
  timeout_override: '',
  max_retries_override: '',
};

export const AgentConfigPanel: React.FC<AgentConfigPanelProps> = ({
  agentTypes,
  agentConfigs,
  providers,
  models,
  onUpdateConfig,
  onDeleteConfig,
  loading = false,
}) => {
  const { toast } = useToast();
  const [expandedAgent, setExpandedAgent] = useState<string | null>(null);
  const [formData, setFormData] = useState<Record<string, AgentConfigFormData>>({});
  const [savingAgent, setSavingAgent] = useState<string | null>(null);
  const [error, setError] = useState<string | AppError | null>(null);

  // 初始化表单数据
  useEffect(() => {
    const initialData: Record<string, AgentConfigFormData> = {};
    agentTypes.forEach((type) => {
      const config = agentConfigs.find((c) => c.agent_type === type.type);
      if (config) {
        initialData[type.type] = {
          model_key: resolveAgentModelKey(config, models),
          temperature_override: config.temperature_override?.toString() || '',
          max_tokens_override: config.max_tokens_override?.toString() || '',
          timeout_override: config.timeout_override?.toString() || '',
          max_retries_override: config.max_retries_override?.toString() || '',
        };
      } else {
        initialData[type.type] = { ...defaultFormData };
      }
    });
    setFormData(initialData);
  }, [agentTypes, agentConfigs, models]);

  // 获取配置
  const getConfig = (agentType: string) => {
    return agentConfigs.find((c) => c.agent_type === agentType);
  };

  // 获取按逻辑模型聚合的可用渠道。
  const modelOptions = useMemo(() => {
    return buildLogicalModelOptions(providers, models);
  }, [providers, models]);

  // 更新表单字段
  const updateField = (agentType: string, field: keyof AgentConfigFormData, value: string) => {
    setFormData((prev) => ({
      ...prev,
      [agentType]: {
        ...prev[agentType],
        [field]: value,
      },
    }));
  };

  // 保存配置
  const handleSave = async (agentType: string) => {
    const data = formData[agentType];
    if (!data?.model_key) {
      setError('请选择逻辑模型');
      return;
    }
	const selectedModel = modelOptions.find((option) => option.value === data.model_key);
	if (!selectedModel) {
	  setError('所选逻辑模型当前没有可用渠道');
	  return;
	}

    setSavingAgent(agentType);
    setError(null);

    try {
      const request: UpdateAgentConfigRequest = {
		model_key: data.model_key,
		model_id: selectedModel.representativeModelId,
        temperature_override: data.temperature_override
          ? parseFloat(data.temperature_override)
          : null,
        max_tokens_override: data.max_tokens_override
          ? parseInt(data.max_tokens_override, 10)
          : null,
        timeout_override: data.timeout_override ? parseInt(data.timeout_override, 10) : null,
        max_retries_override: data.max_retries_override
          ? parseInt(data.max_retries_override, 10)
          : null,
      };

      await onUpdateConfig(agentType, request);
      setExpandedAgent((currentAgent) => currentAgent === agentType ? null : currentAgent);
      toast({
        type: 'success',
        title: '智能体配置已保存',
        description: `${getAgentDisplayName(agentType)}已使用 ${data.model_key}`,
      });
    } catch (err: unknown) {
      if (!isAdminRequestCancelled(err)) {
        setError(toAdminAppError(err, '保存智能体配置失败'));
      }
    } finally {
      setSavingAgent(null);
    }
  };

  // 重置配置
  const handleReset = async (agentType: string) => {
    const config = getConfig(agentType);
    if (config) {
      setSavingAgent(agentType);
      try {
        await onDeleteConfig(agentType);
        setFormData((prev) => ({
          ...prev,
          [agentType]: { ...defaultFormData },
        }));
      } catch (err: unknown) {
        if (!isAdminRequestCancelled(err)) {
          setError(toAdminAppError(err, '重置智能体配置失败'));
        }
      } finally {
        setSavingAgent(null);
      }
    } else {
      setFormData((prev) => ({
        ...prev,
        [agentType]: { ...defaultFormData },
      }));
    }
  };

  // 切换展开
  const toggleExpand = (agentType: string) => {
    setExpandedAgent(expandedAgent === agentType ? null : agentType);
    setError(null);
  };

  // 获取智能体显示名称
  const getAgentDisplayName = (type: string) => {
    return AgentTypeDisplayNames[type as keyof typeof AgentTypeDisplayNames] || type;
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="w-8 h-8 animate-spin text-primary-600" />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* 错误提示 */}
      {typeof error === 'string' && (
        <div className="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg text-red-600 dark:text-red-400 text-sm">
          {error}
        </div>
      )}
      {error && typeof error !== 'string' ? <RequestErrorNotice error={error} /> : null}

      {/* 智能体列表 */}
      {agentTypes.map((agentType) => {
        const config = getConfig(agentType.type);
        const isExpanded = expandedAgent === agentType.type;
        const data = formData[agentType.type] || defaultFormData;
		const configuredModelKey = resolveAgentModelKey(config, models);
		const configuredModel = modelOptions.find((option) => option.value === configuredModelKey);

        return (
          <Card key={agentType.type} className="overflow-hidden">
            {/* 标题栏 */}
            <div
              className="flex items-center justify-between p-4 cursor-pointer hover:bg-surface-50 dark:hover:bg-surface-800 transition-colors"
              onClick={() => toggleExpand(agentType.type)}
            >
              <div className="flex items-center gap-4">
                <div className="p-2 bg-primary-100 dark:bg-primary-900/30 rounded-lg">
                  <Bot className="w-5 h-5 text-primary-600 dark:text-primary-400" />
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="font-medium text-surface-900 dark:text-surface-100">
                      {getAgentDisplayName(agentType.type)}
                    </h3>
                    {agentType.configured ? (
                      <Badge variant="success" className="text-xs">
                        <CheckCircle className="w-3 h-3 mr-1" />
                        已配置
                      </Badge>
                    ) : (
                      <Badge variant="default" className="text-xs">
                        {agentType.type === 'resource_reranker' ? '未启用' : '使用默认'}
                      </Badge>
                    )}
                  </div>
				  {configuredModelKey && (
                    <p className="text-sm text-surface-500 dark:text-surface-400">
					  {configuredModelKey} · {configuredModel?.channelCount ?? 0} 个可用渠道
                    </p>
                  )}
                </div>
              </div>
              <div className="flex items-center gap-2">
                {isExpanded ? (
                  <ChevronUp className="w-5 h-5 text-surface-400" />
                ) : (
                  <ChevronDown className="w-5 h-5 text-surface-400" />
                )}
              </div>
            </div>

            {/* 配置表单（展开时显示） */}
            {isExpanded && (
              <div className="border-t border-surface-200 dark:border-surface-700 p-4 bg-surface-50 dark:bg-surface-900">
                <div className="space-y-4">
                  {/* 模型选择 */}
                  <div>
                    <label className="block text-sm font-medium text-surface-900 dark:text-surface-100 mb-2">
					  选择逻辑模型 <span className="text-red-500">*</span>
                    </label>
                    <Select
					  value={data.model_key}
					  onChange={(value) => updateField(agentType.type, 'model_key', value)}
                      className="w-full"
                      options={[
						{ value: '', label: '请选择逻辑模型...' },
                        ...modelOptions,
                      ]}
                    />
                  </div>

                  {/* 参数覆盖 */}
                  <div className="bg-white dark:bg-surface-800 rounded-lg p-4 border border-surface-200 dark:border-surface-700">
                    <div className="flex items-center gap-2 mb-4">
                      <Settings className="w-4 h-4 text-surface-500" />
                      <span className="text-sm font-medium text-surface-700 dark:text-surface-300">
                        参数覆盖（留空使用服务商模型默认值）
                      </span>
                    </div>

                    <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
                      <div>
                        <label className="block text-xs text-surface-500 dark:text-surface-400 mb-1">
                          Temperature
                        </label>
                        <Input
                          type="number"
                          step="0.1"
                          min="0"
                          max="2"
                          value={data.temperature_override}
                          onChange={(e) =>
                            updateField(agentType.type, 'temperature_override', e.target.value)
                          }
                          placeholder={parameterOverrideDefaults.temperature}
                          className="w-full"
                        />
                      </div>
                      <div>
                        <label className="block text-xs text-surface-500 dark:text-surface-400 mb-1">
                          Max Tokens
                        </label>
                        <Input
                          type="number"
                          min="1"
                          max="128000"
                          value={data.max_tokens_override}
                          onChange={(e) =>
                            updateField(agentType.type, 'max_tokens_override', e.target.value)
                          }
                          placeholder={parameterOverrideDefaults.maxTokens}
                          className="w-full"
                        />
                      </div>
                      <div>
                        <label className="block text-xs text-surface-500 dark:text-surface-400 mb-1">
                          超时时间 (秒)
                        </label>
                        <Input
                          type="number"
                          min="1"
                          max="1800"
                          value={data.timeout_override}
                          onChange={(e) =>
                            updateField(agentType.type, 'timeout_override', e.target.value)
                          }
                          placeholder={parameterOverrideDefaults.timeoutSeconds}
                          className="w-full"
                        />
                      </div>
                      <div>
                        <label className="block text-xs text-surface-500 dark:text-surface-400 mb-1">
                          最大重试次数
                        </label>
                        <Input
                          type="number"
                          min="0"
                          max="10"
                          value={data.max_retries_override}
                          onChange={(e) =>
                            updateField(agentType.type, 'max_retries_override', e.target.value)
                          }
                          placeholder={parameterOverrideDefaults.maxRetries}
                          className="w-full"
                        />
                      </div>
                    </div>
                  </div>

                  {/* 操作按钮 */}
                  <div className="flex justify-end gap-3 pt-2">
                    <Button
                      variant="outline"
                      onClick={() => handleReset(agentType.type)}
                      disabled={savingAgent === agentType.type}
                    >
                      <RotateCcw className="w-4 h-4 mr-2" />
                      {agentType.type === 'resource_reranker' ? '关闭重排' : '重置为默认'}
                    </Button>
                    <Button
                      onClick={() => handleSave(agentType.type)}
					  disabled={savingAgent === agentType.type || !data.model_key}
                    >
                      {savingAgent === agentType.type ? (
                        <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                      ) : (
                        <Save className="w-4 h-4 mr-2" />
                      )}
                      保存配置
                    </Button>
                  </div>
                </div>
              </div>
            )}
          </Card>
        );
      })}

      {/* 空状态 */}
      {agentTypes.length === 0 && (
        <div className="text-center py-12 text-surface-500 dark:text-surface-400">
          <Bot className="w-12 h-12 mx-auto mb-4 opacity-50" />
          <p>暂无智能体类型</p>
        </div>
      )}
    </div>
  );
};

export default AgentConfigPanel;
