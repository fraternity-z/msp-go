import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CheckCircle,
  ChevronDown,
  ChevronUp,
  DatabaseZap,
  FlaskConical,
  Loader2,
  Save,
  Settings,
} from 'lucide-react';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import { useToast } from '@/components/ui/Toast';
import { RequestErrorNotice } from '@/components/feedback';
import type { AppError } from '@/libs/http/apiClient';
import { aiConfigService } from '@/modules/ai-config/services/aiConfigService';
import type {
  ConfigureEmbeddingRequest,
  EmbeddingMetric,
  EmbeddingModelVersion,
  EmbeddingProbeResult,
  LLMModel,
  LLMProvider,
} from '@/modules/ai-config/types/aiConfig';
import { isAdminRequestCancelled, toAdminAppError } from '@/modules/admin/utils/errorFeedback';

interface EmbeddingConfigPanelProps {
  providers: LLMProvider[];
  models: LLMModel[];
}

interface EmbeddingFormState {
  modelId: string;
  dimension: string;
  metric: EmbeddingMetric;
  tokenizer: string;
  normalization: string;
  maxTokens: string;
  sendDimensions: boolean;
  batchSize: string;
  timeoutSeconds: string;
  maxRetries: string;
}

const emptyForm: EmbeddingFormState = {
  modelId: '',
  dimension: '',
  metric: 'cosine',
  tokenizer: '',
  normalization: 'unicode_nfc',
  maxTokens: '8192',
  sendDimensions: false,
  batchSize: '32',
  timeoutSeconds: '30',
  maxRetries: '3',
};

const versionToForm = (version: EmbeddingModelVersion): EmbeddingFormState => ({
  modelId: version.llm_model_id ?? '',
  dimension: String(version.dimension),
  metric: version.metric,
  tokenizer: version.tokenizer ?? '',
  normalization: version.normalization ?? '',
  maxTokens: String(version.max_tokens),
  sendDimensions: version.send_dimensions,
  batchSize: String(version.batch_size),
  timeoutSeconds: String(version.timeout_seconds),
  maxRetries: String(version.max_retries),
});

const isSystemRevision = (revision: string) => /^auto-v\d+-/i.test(revision);

const matchesInteger = (value: string, expected: number): boolean => {
  const normalized = value.trim();
  return normalized !== '' && Number.isInteger(Number(normalized)) && Number(normalized) === expected;
};

const formMatchesVersion = (form: EmbeddingFormState, version: EmbeddingModelVersion): boolean => (
  form.modelId === version.llm_model_id
  && form.metric === version.metric
  && form.tokenizer.trim() === (version.tokenizer ?? '')
  && form.normalization.trim() === (version.normalization ?? '')
  && matchesInteger(form.maxTokens, version.max_tokens)
  && form.sendDimensions === version.send_dimensions
  && (!form.sendDimensions || matchesInteger(form.dimension, version.dimension))
  && matchesInteger(form.batchSize, version.batch_size)
  && matchesInteger(form.timeoutSeconds, version.timeout_seconds)
  && matchesInteger(form.maxRetries, version.max_retries)
);

export const EmbeddingConfigPanel: React.FC<EmbeddingConfigPanelProps> = ({ providers, models }) => {
  const { toast } = useToast();
  const [versions, setVersions] = useState<EmbeddingModelVersion[]>([]);
  const [form, setForm] = useState<EmbeddingFormState>(emptyForm);
  const [loading, setLoading] = useState(true);
  const [activeAction, setActiveAction] = useState<'test' | 'activate' | null>(null);
  const [error, setError] = useState<string | AppError | null>(null);
  const [probe, setProbe] = useState<EmbeddingProbeResult | null>(null);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const actionControllerRef = useRef<AbortController | null>(null);

  const loadVersions = useCallback(async (signal?: AbortSignal) => {
    const response = await aiConfigService.listEmbeddingModels(signal);
    if (signal?.aborted) return;

    setVersions(response.items);
    const active = response.items.find((item) => item.status === 'active');
    if (active) setForm(versionToForm(active));
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadVersions(controller.signal)
      .catch((loadError) => {
        if (!isAdminRequestCancelled(loadError)) {
          setError(toAdminAppError(loadError, '获取向量模型配置失败'));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });

    return () => {
      controller.abort();
      actionControllerRef.current?.abort();
    };
  }, [loadVersions]);

  const activeVersion = versions.find((item) => item.status === 'active') ?? null;
  const activeProviderIds = useMemo(
    () => new Set(providers.filter((provider) => provider.is_active).map((provider) => provider.id)),
    [providers]
  );
  const modelOptions = useMemo(
    () => models
      .filter((model) => model.is_active && activeProviderIds.has(model.provider_id))
      .sort((left, right) => {
        const leftActive = left.id === activeVersion?.llm_model_id ? 1 : 0;
        const rightActive = right.id === activeVersion?.llm_model_id ? 1 : 0;
        return rightActive - leftActive;
      })
      .map((model) => ({
        value: model.id,
        label: `${model.provider_name ?? model.provider_code ?? '未知渠道'} · ${model.name} (${model.model_id})`,
      })),
    [activeProviderIds, activeVersion?.llm_model_id, models]
  );
  const isBusy = activeAction !== null;
  const selectedIsActive = activeVersion !== null && formMatchesVersion(form, activeVersion);

  const updateForm = <K extends keyof EmbeddingFormState>(field: K, value: EmbeddingFormState[K]) => {
    setForm((current) => ({ ...current, [field]: value }));
    setProbe(null);
    setError(null);
  };

  const handleModelChange = (modelId: string) => {
    const current = activeVersion?.llm_model_id === modelId ? activeVersion : null;
    setForm(current ? versionToForm(current) : { ...emptyForm, modelId });
    setProbe(null);
    setError(null);
  };

  const buildRequest = (): ConfigureEmbeddingRequest | null => {
    const dimension = form.sendDimensions ? Number(form.dimension) : 0;
    const integerFields = {
      dimension,
      max_tokens: Number(form.maxTokens),
      batch_size: Number(form.batchSize),
      timeout_seconds: Number(form.timeoutSeconds),
      max_retries: Number(form.maxRetries),
    };

    if (!form.modelId) {
      setError('请选择渠道模型');
      return null;
    }
    if (!modelOptions.some((option) => option.value === form.modelId)) {
      setError('所选模型当前不可用，请选择已启用渠道下的模型');
      return null;
    }
    if (Object.values(integerFields).some((value) => !Number.isInteger(value))) {
      setError('维度、单条输入上限、批大小、超时和重试次数必须是整数');
      return null;
    }
    if (form.sendDimensions && (integerFields.dimension < 1 || integerFields.dimension > 65536)) {
      setError('指定输出维度必须在 1 到 65536 之间');
      return null;
    }
    if (integerFields.max_tokens < 1 || integerFields.max_tokens > 10_000_000) {
      setError('单条输入上限必须在 1 到 10000000 之间');
      return null;
    }
    if (integerFields.batch_size < 1 || integerFields.batch_size > 256) {
      setError('批大小必须在 1 到 256 之间');
      return null;
    }
    if (integerFields.timeout_seconds < 1 || integerFields.timeout_seconds > 300) {
      setError('超时必须在 1 到 300 秒之间');
      return null;
    }
    if (integerFields.max_retries < 0 || integerFields.max_retries > 10) {
      setError('最大重试次数必须在 0 到 10 之间');
      return null;
    }
    if (Array.from(form.tokenizer.trim()).length > 100) {
      setError('Tokenizer 长度不能超过 100 个字符');
      return null;
    }
    if (Array.from(form.normalization.trim()).length > 50) {
      setError('文本标准化规则长度不能超过 50 个字符');
      return null;
    }

    return {
      model_id: form.modelId,
      dimension: integerFields.dimension,
      metric: form.metric,
      tokenizer: form.tokenizer.trim(),
      normalization: form.normalization.trim(),
      max_tokens: integerFields.max_tokens,
      send_dimensions: form.sendDimensions,
      batch_size: integerFields.batch_size,
      timeout_seconds: integerFields.timeout_seconds,
      max_retries: integerFields.max_retries,
    };
  };

  const handleTest = async () => {
    const request = buildRequest();
    if (!request) return;

    setActiveAction('test');
    setError(null);
    actionControllerRef.current?.abort();
    const controller = new AbortController();
    actionControllerRef.current = controller;

    try {
      const result = await aiConfigService.testEmbeddingModel(request, controller.signal);
      if (controller.signal.aborted) return;

      setProbe(result);
      if (result.success && !request.send_dimensions) {
        setForm((current) => ({
          ...current,
          dimension: String(result.observed_dimension),
        }));
      }
    } catch (testError) {
      if (!controller.signal.aborted && !isAdminRequestCancelled(testError)) {
        setError(toAdminAppError(testError, '测试向量模型失败'));
      }
    } finally {
      if (!controller.signal.aborted) setActiveAction(null);
      if (actionControllerRef.current === controller) actionControllerRef.current = null;
    }
  };

  const handleActivate = async () => {
    if (!probe?.success) {
      setError('请先完成当前配置的连接测试');
      return;
    }
    const request = buildRequest();
    if (!request) return;

    setActiveAction('activate');
    setError(null);
    actionControllerRef.current?.abort();
    const controller = new AbortController();
    actionControllerRef.current = controller;

    try {
      const activated = await aiConfigService.activateEmbeddingModel(request, controller.signal);
      if (controller.signal.aborted) return;

      setVersions((current) => [
        activated,
        ...current
          .filter((version) => version.id !== activated.id)
          .map((version) => version.status === 'active'
            ? { ...version, status: 'retired' as const, retired_at: activated.activated_at }
            : version),
      ]);
      setForm(versionToForm(activated));
      setProbe({
        success: true,
        message: 'embedding 模型验证通过并已激活',
        latency_ms: probe.latency_ms,
        model_id: activated.llm_model_id ?? '',
        provider_model: activated.provider_model,
        observed_dimension: activated.dimension,
        resolved_revision: activated.revision,
      });
      toast({
        type: 'success',
        title: '向量模型已激活',
        description: `${activated.provider_model} · ${activated.dimension} 维`,
      });

      try {
        await loadVersions(controller.signal);
      } catch (refreshError) {
        if (!controller.signal.aborted && !isAdminRequestCancelled(refreshError)) {
          setError('向量模型已激活，但历史记录刷新失败，请点击页面“刷新”重试');
        }
      }
    } catch (activateError) {
      if (!controller.signal.aborted && !isAdminRequestCancelled(activateError)) {
        setError(toAdminAppError(activateError, '激活向量模型失败'));
      }
    } finally {
      if (!controller.signal.aborted) setActiveAction(null);
      if (actionControllerRef.current === controller) actionControllerRef.current = null;
    }
  };

  const handleReload = async () => {
    setLoading(true);
    setError(null);
    try {
      await loadVersions();
    } catch (loadError) {
      if (!isAdminRequestCancelled(loadError)) {
        setError(toAdminAppError(loadError, '获取向量模型配置失败'));
      }
    } finally {
      setLoading(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-8 w-8 animate-spin text-primary-600" />
      </div>
    );
  }

  return (
    <div className="space-y-5">
      {typeof error === 'string' ? (
        <div
          role="alert"
          className="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300"
        >
          {error}
        </div>
      ) : null}
      {error && typeof error !== 'string' ? (
        <RequestErrorNotice
          error={error}
          onRetry={() => void handleReload()}
          onRefresh={() => void handleReload()}
        />
      ) : null}

      <Card>
        <CardHeader className="border-b border-surface-200 dark:border-surface-700">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-center gap-3">
              <DatabaseZap className="h-5 w-5 text-emerald-600 dark:text-emerald-400" />
              <CardTitle className="text-lg">资源向量模型</CardTitle>
            </div>
            {activeVersion ? (
              <Badge variant="success">
                <CheckCircle className="mr-1 h-3 w-3" />
                {activeVersion.provider_model} · {activeVersion.dimension} 维
              </Badge>
            ) : (
              <Badge variant="warning">未配置</Badge>
            )}
          </div>
        </CardHeader>

        <CardContent className="pt-6">
          <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(12rem,auto)_auto] lg:items-end">
            <label htmlFor="embedding-model" className="min-w-0">
              <span className="mb-1.5 block text-sm font-medium">渠道模型</span>
              <Select
                id="embedding-model"
                value={form.modelId}
                disabled={isBusy}
                onChange={handleModelChange}
                options={[{ value: '', label: '请选择已启用模型' }, ...modelOptions]}
              />
            </label>

            <div className="min-w-0">
              <span className="mb-1.5 block text-sm font-medium">配置状态</span>
              <div
                role="status"
                aria-live="polite"
                className="flex min-h-10 items-center rounded-md border border-surface-200 bg-surface-50 px-3 text-sm text-surface-700 dark:border-surface-700 dark:bg-surface-800/60 dark:text-surface-200"
              >
                {probe?.success
                  ? `检测通过 · ${probe.observed_dimension} 维`
                  : selectedIsActive
                    ? `当前已激活 · ${activeVersion.dimension} 维`
                    : '待检测'}
              </div>
            </div>

            <div className="flex flex-wrap gap-3 lg:justify-end">
              <Button
                variant="outline"
                onClick={handleTest}
                disabled={modelOptions.length === 0 || isBusy}
                title="会调用真实 embeddings 接口；多 Key 渠道将逐个验证，可能产生费用"
              >
                {activeAction === 'test' ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                ) : (
                  <FlaskConical className="mr-2 h-4 w-4" />
                )}
                测试连接
              </Button>
              <Button
                onClick={handleActivate}
                disabled={modelOptions.length === 0 || isBusy || !probe?.success}
              >
                {activeAction === 'activate' ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                ) : (
                  <Save className="mr-2 h-4 w-4" />
                )}
                验证并激活
              </Button>
            </div>
          </div>

          {modelOptions.length === 0 ? (
            <p className="mt-5 rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
              暂无已启用的渠道模型。请先在“渠道管理”中启用渠道和模型。
            </p>
          ) : null}

          {probe ? (
            <div
              role="status"
              aria-live="polite"
              className={`mt-5 rounded-md border px-4 py-3 text-sm ${
                probe.success
                  ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300'
                  : 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300'
              }`}
            >
              {probe.message}
              {probe.observed_dimension > 0 ? ` · ${probe.observed_dimension} 维` : ''}
              {probe.latency_ms > 0 ? ` · ${probe.latency_ms.toFixed(1)} ms` : ''}
            </div>
          ) : null}

          <div className="mt-6 border-t border-surface-200 pt-4 dark:border-surface-700">
            <button
              type="button"
              onClick={() => setShowAdvanced((current) => !current)}
              disabled={isBusy}
              aria-expanded={showAdvanced}
              aria-controls="embedding-advanced-settings"
              className="flex w-full items-center justify-between gap-3 text-left text-sm font-medium text-surface-700 transition-colors hover:text-surface-950 disabled:cursor-not-allowed disabled:opacity-50 dark:text-surface-300 dark:hover:text-white"
            >
              <span className="flex items-center gap-2">
                <Settings className="h-4 w-4" />
                高级设置
              </span>
              {showAdvanced ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
            </button>

            {showAdvanced ? (
              <div id="embedding-advanced-settings" className="mt-5 space-y-6">
                <section>
                  <h3 className="text-sm font-medium text-surface-900 dark:text-surface-100">向量契约</h3>
                  <div className="mt-4 grid gap-5 md:grid-cols-2 xl:grid-cols-3">
                    <label htmlFor="embedding-metric">
                      <span className="mb-1.5 block text-sm font-medium">距离度量（Metric）</span>
                      <Select
                        id="embedding-metric"
                        disabled={isBusy}
                        value={form.metric}
                        onChange={(value) => updateForm('metric', value as EmbeddingMetric)}
                        options={[
                          { value: 'cosine', label: 'Cosine' },
                          { value: 'dot', label: 'Dot / Inner Product' },
                          { value: 'euclid', label: 'Euclidean / L2' },
                        ]}
                      />
                    </label>

                    <label htmlFor="embedding-max-tokens">
                      <span className="mb-1.5 block text-sm font-medium">单条输入上限（Tokens）</span>
                      <Input
                        id="embedding-max-tokens"
                        disabled={isBusy}
                        type="number"
                        min="1"
                        max="10000000"
                        step="1"
                        value={form.maxTokens}
                        onChange={(event) => updateForm('maxTokens', event.target.value)}
                      />
                    </label>

                    <label htmlFor="embedding-normalization">
                      <span className="mb-1.5 block text-sm font-medium">文本标准化（Normalization）</span>
                      <Input
                        id="embedding-normalization"
                        disabled={isBusy}
                        value={form.normalization}
                        maxLength={50}
                        onChange={(event) => updateForm('normalization', event.target.value)}
                      />
                    </label>

                    <label htmlFor="embedding-tokenizer">
                      <span className="mb-1.5 block text-sm font-medium">分词器标识（Tokenizer）</span>
                      <Input
                        id="embedding-tokenizer"
                        disabled={isBusy}
                        value={form.tokenizer}
                        maxLength={100}
                        onChange={(event) => updateForm('tokenizer', event.target.value)}
                      />
                    </label>

                    <label className="flex min-h-10 items-center gap-3 self-end rounded-md border border-surface-200 px-3 dark:border-surface-700">
                      <input
                        type="checkbox"
                        aria-label="向上游指定输出维度"
                        disabled={isBusy}
                        checked={form.sendDimensions}
                        onChange={(event) => updateForm('sendDimensions', event.target.checked)}
                        className="h-4 w-4 accent-primary-600"
                      />
                      <span className="text-sm">向上游指定输出维度</span>
                    </label>

                    {form.sendDimensions ? (
                      <label htmlFor="embedding-dimension">
                        <span className="mb-1.5 block text-sm font-medium">输出维度（Dimensions）</span>
                        <Input
                          id="embedding-dimension"
                          disabled={isBusy}
                          type="number"
                          min="1"
                          max="65536"
                          step="1"
                          value={form.dimension}
                          onChange={(event) => updateForm('dimension', event.target.value)}
                        />
                      </label>
                    ) : null}
                  </div>
                </section>

                <section className="border-t border-surface-200 pt-5 dark:border-surface-700">
                  <h3 className="text-sm font-medium text-surface-900 dark:text-surface-100">运行参数</h3>
                  <div className="mt-4 grid gap-5 md:grid-cols-3">
                    <label htmlFor="embedding-batch-size">
                      <span className="mb-1.5 block text-sm font-medium">批大小</span>
                      <Input
                        id="embedding-batch-size"
                        disabled={isBusy}
                        type="number"
                        min="1"
                        max="256"
                        step="1"
                        value={form.batchSize}
                        onChange={(event) => updateForm('batchSize', event.target.value)}
                      />
                    </label>

                    <label htmlFor="embedding-timeout">
                      <span className="mb-1.5 block text-sm font-medium">超时（秒）</span>
                      <Input
                        id="embedding-timeout"
                        disabled={isBusy}
                        type="number"
                        min="1"
                        max="300"
                        step="1"
                        value={form.timeoutSeconds}
                        onChange={(event) => updateForm('timeoutSeconds', event.target.value)}
                      />
                    </label>

                    <label htmlFor="embedding-max-retries">
                      <span className="mb-1.5 block text-sm font-medium">瞬时错误最大重试次数</span>
                      <Input
                        id="embedding-max-retries"
                        disabled={isBusy}
                        type="number"
                        min="0"
                        max="10"
                        step="1"
                        value={form.maxRetries}
                        onChange={(event) => updateForm('maxRetries', event.target.value)}
                      />
                    </label>
                  </div>
                </section>
              </div>
            ) : null}
          </div>
        </CardContent>
      </Card>

      {versions.length > 0 ? (
        <div className="overflow-x-auto rounded-md border border-surface-200 dark:border-surface-700">
          <Table className="min-w-[760px]">
            <caption className="sr-only">向量模型配置历史</caption>
            <TableHeader>
              <TableRow>
                <TableHead>状态</TableHead>
                <TableHead>渠道 / 模型</TableHead>
                <TableHead>内部版本</TableHead>
                <TableHead>契约</TableHead>
                <TableHead>运行参数</TableHead>
                <TableHead>激活时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {versions.map((version) => (
                <TableRow key={version.id}>
                  <TableCell>
                    <Badge
                      variant={
                        version.status === 'active'
                          ? 'success'
                          : version.status === 'draft'
                            ? 'warning'
                            : 'secondary'
                      }
                    >
                      {version.status === 'active' ? '当前' : version.status === 'draft' ? '草稿' : '已退役'}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className="font-medium">{version.model_name ?? version.provider_model}</div>
                    <div className="text-xs text-surface-500">{version.provider_name ?? version.provider}</div>
                  </TableCell>
                  <TableCell title={version.revision}>
                    {isSystemRevision(version.revision) ? '系统生成' : version.revision}
                  </TableCell>
                  <TableCell>{version.dimension} · {version.metric}</TableCell>
                  <TableCell>{version.batch_size} / {version.timeout_seconds}s / {version.max_retries}</TableCell>
                  <TableCell>
                    {version.activated_at ? new Date(version.activated_at).toLocaleString('zh-CN') : '-'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
    </div>
  );
};

export default EmbeddingConfigPanel;
