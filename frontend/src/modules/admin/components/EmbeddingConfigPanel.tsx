import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CheckCircle, DatabaseZap, FlaskConical, Loader2, Save } from 'lucide-react';
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
  revision: string;
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
  revision: '',
  dimension: '',
  metric: 'cosine',
  tokenizer: '',
  normalization: 'unicode_nfc',
  maxTokens: '',
  sendDimensions: false,
  batchSize: '32',
  timeoutSeconds: '30',
  maxRetries: '3',
};

export const EmbeddingConfigPanel: React.FC<EmbeddingConfigPanelProps> = ({ providers, models }) => {
  const { toast } = useToast();
  const [versions, setVersions] = useState<EmbeddingModelVersion[]>([]);
  const [form, setForm] = useState<EmbeddingFormState>(emptyForm);
  const [loading, setLoading] = useState(true);
  const [testing, setTesting] = useState(false);
  const [activating, setActivating] = useState(false);
  const [error, setError] = useState<string | AppError | null>(null);
  const [probe, setProbe] = useState<EmbeddingProbeResult | null>(null);
  const actionControllerRef = useRef<AbortController | null>(null);

  const loadVersions = useCallback(async (signal?: AbortSignal) => {
    const response = await aiConfigService.listEmbeddingModels(signal);
    if (signal?.aborted) return;
    setVersions(response.items);
    const active = response.items.find((item) => item.status === 'active');
    if (active) {
      setForm({
        modelId: active.llm_model_id ?? '',
        revision: active.revision,
        dimension: String(active.dimension),
        metric: active.metric,
        tokenizer: active.tokenizer ?? '',
        normalization: active.normalization ?? '',
        maxTokens: String(active.max_tokens),
        sendDimensions: active.send_dimensions,
        batchSize: String(active.batch_size),
        timeoutSeconds: String(active.timeout_seconds),
        maxRetries: String(active.max_retries),
      });
    }
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

  const activeProviderIds = useMemo(
    () => new Set(providers.filter((provider) => provider.is_active).map((provider) => provider.id)),
    [providers]
  );
  const modelOptions = useMemo(
    () => models
      .filter((model) => model.is_active && activeProviderIds.has(model.provider_id))
      .map((model) => ({
        value: model.id,
        label: `${model.provider_name ?? model.provider_code ?? '未知渠道'} · ${model.name} (${model.model_id})`,
      })),
    [activeProviderIds, models]
  );
  const activeVersion = versions.find((item) => item.status === 'active') ?? null;

  const updateForm = <K extends keyof EmbeddingFormState>(field: K, value: EmbeddingFormState[K]) => {
    setForm((current) => ({ ...current, [field]: value }));
    setProbe(null);
  };

  const buildRequest = (): ConfigureEmbeddingRequest | null => {
    const integerFields = {
      dimension: Number(form.dimension),
      max_tokens: Number(form.maxTokens),
      batch_size: Number(form.batchSize),
      timeout_seconds: Number(form.timeoutSeconds),
      max_retries: Number(form.maxRetries),
    };
    if (!form.modelId || !form.revision.trim()) {
      setError('请选择模型并填写 revision');
      return null;
    }
    if (!modelOptions.some((option) => option.value === form.modelId)) {
      setError('所选模型当前不可用，请选择已启用渠道下的模型');
      return null;
    }
    if (Array.from(form.revision.trim()).length > 100) {
      setError('Revision 长度不能超过 100 个字符');
      return null;
    }
    if (Object.values(integerFields).some((value) => !Number.isInteger(value))) {
      setError('维度、Token、批大小、超时和重试次数必须是整数');
      return null;
    }
    if (integerFields.dimension < 1 || integerFields.dimension > 65536) {
      setError('向量维度必须在 1 到 65536 之间');
      return null;
    }
    if (integerFields.max_tokens < 1 || integerFields.max_tokens > 10_000_000) {
      setError('Max Tokens 必须在 1 到 10000000 之间');
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
      setError('重试次数必须在 0 到 10 之间');
      return null;
    }
    if (Array.from(form.tokenizer.trim()).length > 100) {
      setError('Tokenizer 长度不能超过 100 个字符');
      return null;
    }
    if (Array.from(form.normalization.trim()).length > 50) {
      setError('Normalization 长度不能超过 50 个字符');
      return null;
    }
    return {
      model_id: form.modelId,
      revision: form.revision.trim(),
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
    setTesting(true);
    setError(null);
    actionControllerRef.current?.abort();
    const controller = new AbortController();
    actionControllerRef.current = controller;
    try {
      const result = await aiConfigService.testEmbeddingModel(request, controller.signal);
      if (!controller.signal.aborted) setProbe(result);
    } catch (testError) {
      if (!controller.signal.aborted && !isAdminRequestCancelled(testError)) {
        setError(toAdminAppError(testError, '测试向量模型失败'));
      }
    } finally {
      if (!controller.signal.aborted) setTesting(false);
      if (actionControllerRef.current === controller) actionControllerRef.current = null;
    }
  };

  const handleActivate = async () => {
    const request = buildRequest();
    if (!request) return;
    setActivating(true);
    setError(null);
    actionControllerRef.current?.abort();
    const controller = new AbortController();
    actionControllerRef.current = controller;
    try {
      const activated = await aiConfigService.activateEmbeddingModel(request, controller.signal);
      if (controller.signal.aborted) return;
      await loadVersions(controller.signal);
      if (controller.signal.aborted) return;
      setProbe({
        success: true,
        message: 'embedding 模型验证通过',
        latency_ms: 0,
        model_id: activated.llm_model_id ?? '',
        provider_model: activated.provider_model,
        observed_dimension: activated.dimension,
      });
      toast({
        type: 'success',
        title: '向量模型已激活',
        description: `${activated.provider_model} · ${activated.dimension} 维`,
      });
    } catch (activateError) {
      if (!controller.signal.aborted && !isAdminRequestCancelled(activateError)) {
        setError(toAdminAppError(activateError, '激活向量模型失败'));
      }
    } finally {
      if (!controller.signal.aborted) setActivating(false);
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
        <div role="alert" className="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
          {error}
        </div>
      ) : null}
      {error && typeof error !== 'string' ? <RequestErrorNotice error={error} onRetry={() => void handleReload()} onRefresh={() => void handleReload()} /> : null}

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
          <div className="grid gap-5 md:grid-cols-2 xl:grid-cols-4">
            <label className="md:col-span-2 xl:col-span-2" htmlFor="embedding-model">
              <span className="mb-1.5 block text-sm font-medium">渠道模型</span>
              <Select
                id="embedding-model"
                value={form.modelId}
                onChange={(value) => updateForm('modelId', value)}
                options={[{ value: '', label: '请选择已启用模型' }, ...modelOptions]}
              />
            </label>
            <label htmlFor="embedding-revision">
              <span className="mb-1.5 block text-sm font-medium">Revision</span>
              <Input id="embedding-revision" value={form.revision} maxLength={100} onChange={(event) => updateForm('revision', event.target.value)} />
            </label>
            <label htmlFor="embedding-dimension">
              <span className="mb-1.5 block text-sm font-medium">向量维度</span>
              <Input id="embedding-dimension" type="number" min="1" max="65536" step="1" value={form.dimension} onChange={(event) => updateForm('dimension', event.target.value)} />
            </label>
            <label htmlFor="embedding-metric">
              <span className="mb-1.5 block text-sm font-medium">距离度量</span>
              <Select
                id="embedding-metric"
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
              <span className="mb-1.5 block text-sm font-medium">Max Tokens</span>
              <Input id="embedding-max-tokens" type="number" min="1" max="10000000" step="1" value={form.maxTokens} onChange={(event) => updateForm('maxTokens', event.target.value)} />
            </label>
            <label htmlFor="embedding-tokenizer">
              <span className="mb-1.5 block text-sm font-medium">Tokenizer</span>
              <Input id="embedding-tokenizer" value={form.tokenizer} maxLength={100} onChange={(event) => updateForm('tokenizer', event.target.value)} />
            </label>
            <label htmlFor="embedding-normalization">
              <span className="mb-1.5 block text-sm font-medium">Normalization</span>
              <Input id="embedding-normalization" value={form.normalization} maxLength={50} onChange={(event) => updateForm('normalization', event.target.value)} />
            </label>
            <label htmlFor="embedding-batch-size">
              <span className="mb-1.5 block text-sm font-medium">批大小</span>
              <Input id="embedding-batch-size" type="number" min="1" max="256" step="1" value={form.batchSize} onChange={(event) => updateForm('batchSize', event.target.value)} />
            </label>
            <label htmlFor="embedding-timeout">
              <span className="mb-1.5 block text-sm font-medium">超时（秒）</span>
              <Input id="embedding-timeout" type="number" min="1" max="300" step="1" value={form.timeoutSeconds} onChange={(event) => updateForm('timeoutSeconds', event.target.value)} />
            </label>
            <label htmlFor="embedding-max-retries">
              <span className="mb-1.5 block text-sm font-medium">最大重试次数</span>
              <Input id="embedding-max-retries" type="number" min="0" max="10" step="1" value={form.maxRetries} onChange={(event) => updateForm('maxRetries', event.target.value)} />
            </label>
            <label className="flex min-h-10 items-center gap-3 self-end rounded-md border border-surface-200 px-3 dark:border-surface-700">
              <input
                type="checkbox"
                aria-label="请求发送 dimensions"
                checked={form.sendDimensions}
                onChange={(event) => updateForm('sendDimensions', event.target.checked)}
                className="h-4 w-4 accent-primary-600"
              />
              <span className="text-sm">请求发送 dimensions</span>
            </label>
          </div>

          {modelOptions.length === 0 ? (
            <p className="mt-5 rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
              暂无已启用的渠道模型。请先在“渠道管理”中启用渠道和模型。
            </p>
          ) : null}

          {probe ? (
            <div role="status" aria-live="polite" className={`mt-5 rounded-md border px-4 py-3 text-sm ${probe.success
              ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300'
              : 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300'
            }`}>
              {probe.message}
              {probe.observed_dimension > 0 ? ` · ${probe.observed_dimension} 维` : ''}
              {probe.latency_ms > 0 ? ` · ${probe.latency_ms.toFixed(1)} ms` : ''}
            </div>
          ) : null}

          <div className="mt-6 flex flex-wrap justify-end gap-3">
            <Button variant="outline" onClick={handleTest} disabled={modelOptions.length === 0 || testing || activating}>
              {testing ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <FlaskConical className="mr-2 h-4 w-4" />}
              测试连接
            </Button>
            <Button onClick={handleActivate} disabled={modelOptions.length === 0 || testing || activating}>
              {activating ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
              验证并激活
            </Button>
          </div>
        </CardContent>
      </Card>

      {versions.length > 0 ? (
        <div className="overflow-hidden rounded-md border border-surface-200 dark:border-surface-700">
          <Table className="min-w-[760px]">
            <caption className="sr-only">向量模型配置历史</caption>
            <TableHeader>
              <TableRow>
                <TableHead>状态</TableHead>
                <TableHead>渠道 / 模型</TableHead>
                <TableHead>Revision</TableHead>
                <TableHead>契约</TableHead>
                <TableHead>批处理</TableHead>
                <TableHead>激活时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {versions.map((version) => (
                <TableRow key={version.id}>
                  <TableCell>
                    <Badge variant={version.status === 'active' ? 'success' : version.status === 'draft' ? 'warning' : 'secondary'}>
                      {version.status === 'active' ? '当前' : version.status === 'draft' ? '草稿' : '已退役'}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className="font-medium">{version.model_name ?? version.provider_model}</div>
                    <div className="text-xs text-surface-500">{version.provider_name ?? version.provider}</div>
                  </TableCell>
                  <TableCell>{version.revision}</TableCell>
                  <TableCell>{version.dimension} · {version.metric}</TableCell>
                  <TableCell>{version.batch_size} / {version.timeout_seconds}s / {version.max_retries}</TableCell>
                  <TableCell>{version.activated_at ? new Date(version.activated_at).toLocaleString('zh-CN') : '-'}</TableCell>
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
