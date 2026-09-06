import React, { useRef, useState } from 'react';
import { Modal } from '../../../../components/ui/Modal';
import { RequestErrorNotice } from '@/components/feedback';
import { Button } from '../../../../components/ui/Button';
import { Input } from '../../../../components/ui/Input';
import { Loader2, Upload, Search, Link as LinkIcon, Check, X, Plus } from 'lucide-react';
import { cn } from '../../../../libs/utils/cn';
import { resourceService } from '@/modules/resource/services/resourceService';
import { uploadResourceFile, validateResourceFile } from '@/modules/upload/services/uploadService';
import { ingestionService, validateIngestionFile } from '@/modules/resource/services/ingestionService';
import type { ResourceCreateRequest, BatchImportItem, ResourceType as ResourceTypeEnum } from '@/modules/resource/types/resource';
import { toAppError, type AppError } from '@/libs/http/apiClient';
import {
  extractTitleFromUrl,
  detectResourceTypeFromUrl,
  extractSourceFromUrl,
  parseLinksFromText,
  generateTempId,
  extractTitleFromFilename,
  detectResourceTypeFromFile,
} from '@/libs/utils/resourceUtils';

interface BatchImportModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (documentsAccepted?: boolean) => void;
}

function formatRequestError(error: unknown, fallback: string): string {
  const appError = toAppError(error, fallback);
  return [
    appError.message,
    appError.retryAfter !== undefined && appError.retryAfter > 0
      ? `可在 ${appError.retryAfter} 秒后重试`
      : '',
    appError.requestId ? `请求编号：${appError.requestId}` : '',
  ].filter(Boolean).join('；');
}

// 文件上传进度状态
interface FileUploadProgress {
  id: string;
  percent: number;
  status: 'pending' | 'uploading' | 'done' | 'error';
  url?: string;
  error?: string;
  accepted?: boolean;
}

function validateUploadFile(file: File) {
  return detectResourceTypeFromFile(file.name) === 'video' ? validateResourceFile(file) : validateIngestionFile(file);
}

export const BatchImportModal = React.memo<BatchImportModalProps>(({ isOpen, onClose, onSuccess }) => {
  const [tab, setTab] = useState<'link' | 'file'>('link');
  const [linksText, setLinksText] = useState('');
  const [importItems, setImportItems] = useState<BatchImportItem[]>([]);
  const [defaultChapter, setDefaultChapter] = useState('');
  const [defaultTopic, setDefaultTopic] = useState('');
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [requestError, setRequestError] = useState<AppError | null>(null);
  const completedLinkImportsRef = useRef(new Map<string, string>());
  const documentRequestIDsRef = useRef(new Map<string, { key: string; id: string }>());

  // 文件上传状态
  const [files, setFiles] = useState<File[]>([]);
  const [fileItems, setFileItems] = useState<BatchImportItem[]>([]);
  const [fileProgress, setFileProgress] = useState<Record<string, FileUploadProgress>>({});

  // 解析批量链接
  const handleParseLinks = () => {
    const urls = parseLinksFromText(linksText);
    const items: BatchImportItem[] = urls.map((url) => ({
      id: generateTempId(),
      url,
      title: extractTitleFromUrl(url),
      type: detectResourceTypeFromUrl(url),
      source: extractSourceFromUrl(url),
      selected: true,
    }));
    completedLinkImportsRef.current.clear();
    setImportItems(items);
  };

  const handleUpdateItem = (id: string, updates: Partial<BatchImportItem>) => {
    setImportItems((items) => items.map((item) => (item.id === id ? { ...item, ...updates } : item)));
  };

  const handleToggleItem = (id: string) => {
    setImportItems((items) => items.map((item) => (item.id === id ? { ...item, selected: !item.selected } : item)));
  };

  const handleToggleAll = () => {
    const allSelected = importItems.every((item) => item.selected);
    setImportItems((items) => items.map((item) => ({ ...item, selected: !allSelected })));
  };

  // 执行批量导入（链接）
  const handleBatchImport = async () => {
    const selectedItems = importItems.filter((item) => item.selected);
    if (selectedItems.length === 0) return;

    const pendingItems = selectedItems.filter((item) => {
      const data: ResourceCreateRequest = {
        title: item.title,
        type: item.type,
        url: item.url,
        source: item.source,
        chapter: defaultChapter || undefined,
        topic: defaultTopic || undefined,
        storage_type: 'external',
      };
      return completedLinkImportsRef.current.get(item.id) !== JSON.stringify(data);
    });
    if (pendingItems.length === 0) {
      handleClose();
      onSuccess();
      return;
    }

    setImporting(true);
    setImportError(null);
    setRequestError(null);
    try {
      for (const item of pendingItems) {
        const data: ResourceCreateRequest = {
          title: item.title,
          type: item.type,
          url: item.url,
          source: item.source,
          chapter: defaultChapter || undefined,
          topic: defaultTopic || undefined,
          storage_type: 'external',
        };
        await resourceService.createResource(data);
        completedLinkImportsRef.current.set(item.id, JSON.stringify(data));
      }
      handleClose();
      onSuccess();
    } catch (error) {
      setRequestError(toAppError(error, '资源导入失败'));
    } finally {
      setImporting(false);
    }
  };

  // 处理文件选择
  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const selectedFiles = Array.from(e.target.files || []);
    if (selectedFiles.length === 0) return;
    if (selectedFiles.length > 10) {
      setImportError('每批最多上传 10 个文件');
      return;
    }

    // 验证文件类型
    const validFiles: File[] = [];
    const validationErrors: string[] = [];
    for (const file of selectedFiles) {
      const validation = validateUploadFile(file);
      if (!validation.valid) {
        validationErrors.push(`文件 "${file.name}" 不支持：${validation.error}`);
        continue;
      }
      validFiles.push(file);
    }
    setImportError(validationErrors.length > 0 ? validationErrors.join('；') : null);

    if (validFiles.length === 0) return;

    setFiles(validFiles);
    setFileProgress({});
    documentRequestIDsRef.current.clear();
    const items: BatchImportItem[] = validFiles.map((file) => ({
      id: generateTempId(),
      url: '',
      title: extractTitleFromFilename(file.name),
      type: detectResourceTypeFromFile(file.name),
      source: '云存储',
      selected: true,
    }));
    setFileItems(items);
  };

  const handleUpdateFileItem = (id: string, updates: Partial<BatchImportItem>) => {
    setFileItems((items) => items.map((item) => (item.id === id ? { ...item, ...updates } : item)));
  };

  const handleToggleFileItem = (id: string) => {
    setFileItems((items) => items.map((item) => (item.id === id ? { ...item, selected: !item.selected } : item)));
  };

  const handleToggleAllFiles = () => {
    const allSelected = fileItems.every((item) => item.selected);
    setFileItems((items) => items.map((item) => ({ ...item, selected: !allSelected })));
  };

  // 执行文件上传（真正上传到七牛云）
  const handleFileUpload = async () => {
    const selectedIndices = fileItems
      .map((item, index) => ({ item, index }))
      .filter(({ item }) => item.selected && fileProgress[item.id]?.status !== 'done');

    if (selectedIndices.length === 0) {
      if (fileItems.some((item) => item.selected && fileProgress[item.id]?.status === 'done')) {
        const documentsAccepted = fileItems.some((item) => fileProgress[item.id]?.accepted);
        handleClose();
        onSuccess(documentsAccepted);
      }
      return;
    }

    setImporting(true);
    setImportError(null);
    setRequestError(null);
    let hadFailure = false;
    let documentsAccepted = fileItems.some((item) => fileProgress[item.id]?.accepted);

    // 初始化进度状态
    const initialProgress: Record<string, FileUploadProgress> = { ...fileProgress };
    for (const { item } of selectedIndices) {
      initialProgress[item.id] = {
        id: item.id,
        percent: fileProgress[item.id]?.url ? 100 : 0,
        status: 'pending',
        ...(fileProgress[item.id]?.url ? { url: fileProgress[item.id].url } : {}),
      };
    }
    setFileProgress(initialProgress);

    for (const { item, index } of selectedIndices) {
      const file = files[index];
      if (!file) continue;

      // 更新为上传中
      setFileProgress((prev) => ({
        ...prev,
        [item.id]: { ...prev[item.id], status: 'uploading', percent: 0 },
      }));

      try {
        if (detectResourceTypeFromFile(file.name) === 'document') {
          const requestKey = JSON.stringify([item.title, defaultChapter, defaultTopic]);
          const savedRequest = documentRequestIDsRef.current.get(item.id);
          const requestID = savedRequest?.key === requestKey ? savedRequest.id : crypto.randomUUID();
          documentRequestIDsRef.current.set(item.id, { key: requestKey, id: requestID });
          await ingestionService.upload({ file, title: item.title, chapter: defaultChapter, topic: defaultTopic, client_request_id: requestID }, (percent) => {
            setFileProgress((prev) => ({ ...prev, [item.id]: { ...prev[item.id], percent } }));
          });
          documentsAccepted = true;
          setFileProgress((prev) => ({ ...prev, [item.id]: { ...prev[item.id], status: 'done', percent: 100, accepted: true } }));
          continue;
        }
        // 已经完成对象存储上传但资源记录失败时，重试只提交资源记录，避免重复上传。
        const existingUrl = fileProgress[item.id]?.url;
        const resourceUrl = existingUrl ?? (await uploadResourceFile(file, (percent) => {
          setFileProgress((prev) => ({
            ...prev,
            [item.id]: { ...prev[item.id], percent },
          }));
        })).url;
        setFileProgress((prev) => ({
          ...prev,
          [item.id]: { ...prev[item.id], percent: 100, status: 'uploading', url: resourceUrl },
        }));

        // 上传成功，创建资源记录
        const data: ResourceCreateRequest = {
          title: item.title,
          type: item.type,
          url: resourceUrl,
          source: item.source,
          chapter: defaultChapter || undefined,
          topic: defaultTopic || undefined,
          storage_type: 'cloud',
        };
        await resourceService.createResource(data);

        setFileProgress((prev) => ({
          ...prev,
          [item.id]: { ...prev[item.id], status: 'done', percent: 100, url: resourceUrl },
        }));
      } catch (err) {
        hadFailure = true;
        setRequestError((current) => current ?? toAppError(err, '上传失败'));
        const errorMsg = formatRequestError(err, '上传失败');
        setFileProgress((prev) => ({
          ...prev,
          [item.id]: { ...prev[item.id], status: 'error', error: errorMsg },
        }));
      }
    }

    setImporting(false);
    if (!hadFailure) {
      handleClose();
      onSuccess(documentsAccepted);
    }
  };

  const handleClose = () => {
    setTab('link');
    setLinksText('');
    setImportItems([]);
    setDefaultChapter('');
    setDefaultTopic('');
    setFiles([]);
    setFileItems([]);
    setFileProgress({});
    setImportError(null);
    setRequestError(null);
    completedLinkImportsRef.current.clear();
    documentRequestIDsRef.current.clear();
    onClose();
  };

  return (
    <Modal isOpen={isOpen} onClose={() => { if (!importing) handleClose(); }} title="上传资源" stickyHeader className="max-w-2xl max-h-[85dvh] overflow-y-auto rounded-lg p-4 sm:p-6 [&_h3]:text-lg">
      <div className="space-y-4">
        {requestError ? (
          <RequestErrorNotice
            error={requestError}
            onRetry={tab === 'link' ? handleBatchImport : handleFileUpload}
            onDismiss={() => setRequestError(null)}
          />
        ) : null}
        {importError ? (
          <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-200">
            {importError}
          </div>
        ) : null}
        {/* 标签页切换 */}
        <div className="flex border-b border-surface-200 dark:border-surface-700">
          <button
            onClick={() => setTab('link')}
            disabled={importing}
            className={cn(
              "flex-1 whitespace-nowrap px-2 py-2 text-sm font-medium border-b-2 transition-colors",
              tab === 'link'
                ? "border-primary-500 text-primary-600 dark:text-primary-400"
                : "border-transparent text-surface-500 hover:text-surface-700 dark:hover:text-surface-300"
            )}
          >
            <LinkIcon className="w-4 h-4 inline-block mr-2" />
            导入链接
          </button>
          <button
            onClick={() => setTab('file')}
            disabled={importing}
            className={cn(
              "flex-1 whitespace-nowrap px-2 py-2 text-sm font-medium border-b-2 transition-colors",
              tab === 'file'
                ? "border-primary-500 text-primary-600 dark:text-primary-400"
                : "border-transparent text-surface-500 hover:text-surface-700 dark:hover:text-surface-300"
            )}
          >
            <Upload className="w-4 h-4 inline-block mr-2" />
            上传文件
          </button>
        </div>

        {/* 链接导入标签页 */}
        {tab === 'link' && (
          <>
            {importItems.length === 0 ? (
              <>
                <div>
                  <label className="block text-sm font-medium text-surface-700 dark:text-surface-300 mb-1">
                    请粘贴资源链接（每行一个）
                  </label>
                  <textarea
                    value={linksText}
                    onChange={(e) => setLinksText(e.target.value)}
                    placeholder="https://www.bilibili.com/video/BV1xx...&#10;https://example.com/docs/calculus.pdf&#10;..."
                    rows={6}
                    className="w-full px-3 py-2 rounded-lg border border-surface-200 dark:border-surface-700 bg-white dark:bg-surface-800 text-surface-900 dark:text-surface-100 text-sm focus:outline-none focus:ring-2 focus:ring-primary-500 resize-none font-mono"
                  />
                </div>
                <div className="flex justify-end gap-2">
                  <Button variant="outline" onClick={handleClose}>取消</Button>
                  <Button onClick={handleParseLinks} disabled={!linksText.trim()}>
                    <Search className="w-4 h-4 mr-2" />
                    解析链接
                  </Button>
                </div>
              </>
            ) : (
              <>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-surface-600 dark:text-surface-400">
                    解析结果（{importItems.filter((i) => i.selected).length}/{importItems.length} 个已选）
                  </span>
                  <Button variant="ghost" size="sm" onClick={handleToggleAll}>
                    {importItems.every((i) => i.selected) ? '取消全选' : '全选'}
                  </Button>
                </div>

                <div className="max-h-64 overflow-y-auto space-y-2 border border-surface-200 dark:border-surface-700 rounded-lg p-2">
                  {importItems.map((item) => (
                    <div
                      key={item.id}
                      className={cn(
                        "flex items-center gap-3 p-2 rounded-lg transition-colors",
                        item.selected ? "bg-primary-50 dark:bg-primary-900/20" : "bg-surface-50 dark:bg-surface-800"
                      )}
                    >
                      <button
                        onClick={() => handleToggleItem(item.id)}
                        className={cn(
                          "w-5 h-5 rounded border-2 flex items-center justify-center shrink-0 transition-colors",
                          item.selected ? "bg-primary-500 border-primary-500 text-white" : "border-surface-300 dark:border-surface-600"
                        )}
                      >
                        {item.selected && <Check className="w-3 h-3" />}
                      </button>
                      <div className="flex-1 min-w-0">
                        <Input
                          value={item.title}
                          onChange={(e) => handleUpdateItem(item.id, { title: e.target.value })}
                          className="mb-1 text-sm"
                          placeholder="标题"
                        />
                        <div className="flex items-center gap-2 text-xs text-surface-500">
                          <select
                            value={item.type}
                            onChange={(e) => handleUpdateItem(item.id, { type: e.target.value as ResourceTypeEnum })}
                            className="px-2 py-1 rounded border border-surface-200 dark:border-surface-700 bg-white dark:bg-surface-800 text-surface-700 dark:text-surface-300"
                          >
                            <option value="video">视频</option>
                            <option value="document">文档</option>
                          </select>
                          <span className="truncate" title={item.source}>{item.source}</span>
                        </div>
                      </div>
                      <button
                        onClick={() => setImportItems((items) => items.filter((i) => i.id !== item.id))}
                        className="p-1 text-surface-400 hover:text-red-500 transition-colors"
                      >
                        <X className="w-4 h-4" />
                      </button>
                    </div>
                  ))}
                </div>

                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="block text-sm font-medium text-surface-700 dark:text-surface-300 mb-1">默认章节</label>
                    <Input value={defaultChapter} onChange={(e) => setDefaultChapter(e.target.value)} placeholder="如：第一章" />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-surface-700 dark:text-surface-300 mb-1">默认主题</label>
                    <Input value={defaultTopic} onChange={(e) => setDefaultTopic(e.target.value)} placeholder="如：极限" />
                  </div>
                </div>

                <div className="flex justify-between gap-2 pt-4">
                  <Button variant="ghost" onClick={() => { setImportItems([]); setLinksText(''); }}>返回修改</Button>
                  <div className="flex gap-2">
                    <Button variant="outline" onClick={handleClose}>取消</Button>
                    <Button onClick={handleBatchImport} disabled={importing || importItems.filter((i) => i.selected).length === 0}>
                      {importing ? <><Loader2 className="w-4 h-4 mr-2 animate-spin" />导入中...</> : <><Upload className="w-4 h-4 mr-2" />导入 {importItems.filter((i) => i.selected).length} 个资源</>}
                    </Button>
                  </div>
                </div>
              </>
            )}
          </>
        )}

        {/* 文件上传标签页 */}
        {tab === 'file' && (
          <>
            {fileItems.length === 0 ? (
              <>
                <div
                  className="border-2 border-dashed border-surface-300 dark:border-surface-600 rounded-lg p-8 text-center hover:border-primary-400 dark:hover:border-primary-600 transition-colors cursor-pointer"
                  onClick={() => document.getElementById('file-upload-input')?.click()}
                >
                  <Upload className="w-12 h-12 text-surface-400 mx-auto mb-4" />
                  <p className="text-surface-600 dark:text-surface-400 mb-2">选择文件</p>
                  <p className="text-xs text-surface-500">文档：PDF、DOCX、TXT、MD，最大 50 MiB；视频最大 500 MiB。每批最多 10 个文件。</p>
                  <input
                    id="file-upload-input"
                    type="file"
                    multiple
                    className="hidden"
                    accept=".mp4,.avi,.mov,.mkv,.webm,.pdf,.docx,.txt,.md"
                    onChange={handleFileSelect}
                  />
                </div>
                <div className="flex justify-end gap-2">
                  <Button variant="outline" onClick={handleClose}>取消</Button>
                </div>
              </>
            ) : (
              <>
                <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <span className="text-sm text-surface-600 dark:text-surface-400">
                    已选文件（{fileItems.filter((i) => i.selected).length}/{fileItems.length} 个）
                  </span>
                  <div className="flex gap-2">
                    <Button variant="ghost" size="sm" className="whitespace-nowrap" disabled={importing} onClick={handleToggleAllFiles}>
                      {fileItems.every((i) => i.selected) ? '取消全选' : '全选'}
                    </Button>
                    <Button variant="ghost" size="sm" className="whitespace-nowrap" disabled={importing} onClick={() => document.getElementById('file-upload-input-add')?.click()}>
                      <Plus className="w-4 h-4 mr-1" />添加
                    </Button>
                    <input
                      id="file-upload-input-add"
                      type="file"
                      multiple
                      className="hidden"
                      accept=".mp4,.avi,.mov,.mkv,.webm,.pdf,.docx,.txt,.md"
                      onChange={(e) => {
                        const selectedFiles = Array.from(e.target.files || []);
                        if (selectedFiles.length === 0) return;
                        if (files.length + selectedFiles.length > 10) {
                          setImportError('每批最多上传 10 个文件');
                          return;
                        }
                        const invalid = selectedFiles.map((file) => ({ file, validation: validateUploadFile(file) })).filter(({ validation }) => !validation.valid);
                        setImportError(invalid.length ? invalid.map(({ file, validation }) => `${file.name}：${validation.error}`).join('；') : null);
                        const validFiles = selectedFiles.filter((file) => validateUploadFile(file).valid);
                        setFiles((prev) => [...prev, ...validFiles]);
                        const newItems: BatchImportItem[] = validFiles.map((file) => ({
                          id: generateTempId(),
                          url: '',
                          title: extractTitleFromFilename(file.name),
                          type: detectResourceTypeFromFile(file.name),
                          source: '云存储',
                          selected: true,
                        }));
                        setFileItems((prev) => [...prev, ...newItems]);
                      }}
                    />
                  </div>
                </div>

                <div className="max-h-64 overflow-y-auto space-y-2 border border-surface-200 dark:border-surface-700 rounded-lg p-2">
                  {fileItems.map((item, index) => {
                    const progress = fileProgress[item.id];
                    return (
                      <div
                        key={item.id}
                        className={cn(
                          "flex items-center gap-3 p-2 rounded-lg transition-colors",
                          item.selected ? "bg-primary-50 dark:bg-primary-900/20" : "bg-surface-50 dark:bg-surface-800"
                        )}
                      >
                        <button
                          onClick={() => handleToggleFileItem(item.id)}
                          disabled={importing}
                          className={cn(
                            "w-5 h-5 rounded border-2 flex items-center justify-center shrink-0 transition-colors",
                            item.selected ? "bg-primary-500 border-primary-500 text-white" : "border-surface-300 dark:border-surface-600"
                          )}
                        >
                          {item.selected && <Check className="w-3 h-3" />}
                        </button>
                        <div className="flex-1 min-w-0">
                          <Input
                            value={item.title}
                            onChange={(e) => handleUpdateFileItem(item.id, { title: e.target.value })}
                            className="mb-1 text-sm"
                            placeholder="标题"
                            disabled={importing}
                          />
                          <div className="flex items-center gap-2 text-xs text-surface-500">
                            <select
                              value={item.type}
                              onChange={(e) => handleUpdateFileItem(item.id, { type: e.target.value as ResourceTypeEnum })}
                              disabled
                              className="px-2 py-1 rounded border border-surface-200 dark:border-surface-700 bg-white dark:bg-surface-800 text-surface-700 dark:text-surface-300"
                            >
                              <option value="video">视频</option>
                              <option value="document">文档</option>
                            </select>
                            <span className="truncate" title={files[index]?.name}>{files[index]?.name}</span>
                          </div>
                          {/* 上传进度条 */}
                          {progress && progress.status === 'uploading' && (
                            <div className="mt-1">
                              <div className="w-full bg-surface-200 dark:bg-surface-700 rounded-full h-1.5">
                                <div
                                  className="bg-primary-500 h-1.5 rounded-full transition-all"
                                  style={{ width: `${progress.percent}%` }}
                                />
                              </div>
                              <span className="text-xs text-surface-400">{progress.percent}%</span>
                            </div>
                          )}
                          {progress && progress.status === 'done' && (
                            <span className="text-xs text-green-500">{progress.accepted ? '已接收，等待处理' : '上传成功'}</span>
                          )}
                          {progress && progress.status === 'error' && (
                            <span className="text-xs text-red-500">✗ {progress.error}</span>
                          )}
                        </div>
                        <button
                          onClick={() => {
                            setFileItems((items) => items.filter((i) => i.id !== item.id));
                            setFiles((f) => f.filter((_, i) => i !== index));
                          }}
                          disabled={importing}
                          className="p-1 text-surface-400 hover:text-red-500 transition-colors disabled:opacity-50"
                        >
                          <X className="w-4 h-4" />
                        </button>
                      </div>
                    );
                  })}
                </div>

                <div className="grid grid-cols-2 gap-3 sm:gap-4">
                  <div>
                    <label className="block text-sm font-medium text-surface-700 dark:text-surface-300 mb-1">默认章节</label>
                    <Input value={defaultChapter} onChange={(e) => setDefaultChapter(e.target.value)} placeholder="如：第一章" />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-surface-700 dark:text-surface-300 mb-1">默认主题</label>
                    <Input value={defaultTopic} onChange={(e) => setDefaultTopic(e.target.value)} placeholder="如：极限" />
                  </div>
                </div>

                <div className="flex flex-col gap-2 pt-4 sm:flex-row sm:justify-between">
                  <Button variant="ghost" onClick={() => { setFileItems([]); setFiles([]); }} disabled={importing}>
                    清空列表
                  </Button>
                  <div className="flex flex-col-reverse gap-2 sm:flex-row [&_button]:whitespace-nowrap">
                    <Button variant="outline" onClick={handleClose} disabled={importing}>取消</Button>
                    <Button
                      onClick={handleFileUpload}
                      disabled={importing || fileItems.filter((i) => i.selected).length === 0}
                    >
                      {importing ? (
                        <><Loader2 className="w-4 h-4 mr-2 animate-spin" />上传中...</>
                      ) : (
                        <><Upload className="w-4 h-4 mr-2" />上传 {fileItems.filter((i) => i.selected).length} 个文件</>
                      )}
                    </Button>
                  </div>
                </div>
              </>
            )}
          </>
        )}
      </div>
    </Modal>
  );
});

BatchImportModal.displayName = 'BatchImportModal';

