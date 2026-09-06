import React, { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { MainLayout } from '../../../components/layout/MainLayout';
import { resourceService } from '@/modules/resource/services/resourceService';
import type { Resource, ResourceFilter, ResourceStats } from '@/modules/resource/types/resource';
import type { FilterType, ViewMode } from './types';
import { resourcePageReducer, initialState } from './reducer';
import { ResourceStatsCards } from './components/ResourceStatsCards';
import { ResourceFilters } from './components/ResourceFilters';
import { BatchSelectionBar } from './components/BatchSelectionBar';
import { ResourceGridView } from './components/ResourceGridView';
import { ResourceListView } from './components/ResourceListView';
import { ResourceDetailModal } from './components/ResourceDetailModal';
import { ResourceEditModal } from './components/ResourceEditModal';
import { BatchImportModal } from './components/BatchImportModal';
import { IngestionPanel } from './components/IngestionPanel';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/Tabs';
import { ConfirmDialog } from '../../../components/ui/ConfirmDialog';
import { RequestErrorNotice } from '@/components/feedback';
import { Button } from '../../../components/ui/Button';
import { useToast } from '@/components/ui/Toast';
import { toAppError, type AppError } from '@/libs/http/apiClient';
import { Check, Upload, Loader2, FolderOpen } from 'lucide-react';

export const TeacherResourcesPage: React.FC = () => {
  const { toast } = useToast();
  const [state, localDispatch] = useReducer(resourcePageReducer, initialState);
  const [resources, setResources] = useState<Resource[]>([]);
  const [stats, setStats] = useState<ResourceStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [statsLoading, setStatsLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState(false);
  const [listError, setListError] = useState<AppError | null>(null);
  const [statsError, setStatsError] = useState<AppError | null>(null);
  const listRequestRef = useRef(0);
  const [activeView, setActiveView] = useState('resources');
  const [ingestionRevision, setIngestionRevision] = useState(0);

  const currentFilter = useMemo<ResourceFilter>(() => ({
    ...(state.selectedType !== 'all' ? { type: state.selectedType } : {}),
    ...(state.searchTerm ? { search: state.searchTerm } : {}),
  }), [state.searchTerm, state.selectedType]);

  const loadResources = useCallback(async (filter: ResourceFilter) => {
    const requestID = listRequestRef.current + 1;
    listRequestRef.current = requestID;
    setLoading(true);
    setListError(null);
    try {
      const response = await resourceService.getResources(filter);
      if (listRequestRef.current === requestID) setResources(response.items);
    } catch (error) {
      if (listRequestRef.current === requestID) {
        setListError(toAppError(error, '资源列表加载失败'));
      }
    } finally {
      if (listRequestRef.current === requestID) setLoading(false);
    }
  }, []);

  const loadStats = useCallback(async () => {
    setStatsLoading(true);
    setStatsError(null);
    try {
      setStats(await resourceService.getStats());
    } catch (error) {
      setStatsError(toAppError(error, '资源统计加载失败'));
    } finally {
      setStatsLoading(false);
    }
  }, []);

  const notifyRequestError = useCallback((error: unknown, fallback: string) => {
    const appError = toAppError(error, fallback);
    if (appError.kind === 'cancelled' || appError.kind === 'rate_limited') return;
    const details = [
      appError.retryAfter !== undefined && appError.retryAfter > 0
        ? `可在 ${appError.retryAfter} 秒后重试`
        : '',
      appError.requestId ? `请求编号：${appError.requestId}` : '',
    ].filter(Boolean);
    toast({
      type: 'error',
      title: appError.message,
      description: details.length > 0 ? details.join('；') : undefined,
    });
  }, [toast]);

  // 初始加载统计
  useEffect(() => {
    void loadStats();
  }, [loadStats]);

  // 筛选变化时重新加载（含搜索防抖）
  useEffect(() => {
    const timer = setTimeout(() => {
      void loadResources(currentFilter);
    }, state.searchTerm ? 300 : 0);

    return () => {
      clearTimeout(timer);
      listRequestRef.current += 1;
    };
  }, [currentFilter, loadResources, state.searchTerm]);

  // 处理删除
  const handleDelete = async (id: string) => {
    setActionLoading(true);
    try {
      await resourceService.deleteResource(id);
      setResources((current) => current.filter((resource) => resource.id !== id));
      localDispatch({ type: 'CLOSE_DELETE_CONFIRM' });
      await loadStats();
    } catch (error) {
      notifyRequestError(error, '删除资源失败');
    } finally {
      setActionLoading(false);
    }
  };

  // 批量删除
  const handleBatchDelete = async () => {
    const ids = Array.from(state.selectedResourceIds);
    setActionLoading(true);
    const failed: unknown[] = [];
    const deletedIDs: string[] = [];
    try {
      for (const id of ids) {
        try {
          await resourceService.deleteResource(id);
          deletedIDs.push(id);
        } catch (error) {
          failed.push(error);
        }
      }
      if (deletedIDs.length > 0) {
        const deleted = new Set(deletedIDs);
        setResources((current) => current.filter((resource) => !deleted.has(resource.id)));
        await loadStats();
      }
      if (failed.length > 0) {
        notifyRequestError(
          failed[0],
          failed.length === ids.length ? '批量删除失败' : `有 ${failed.length} 个资源删除失败`,
        );
        return;
      }
      localDispatch({ type: 'CLOSE_BATCH_DELETE_CONFIRM' });
      localDispatch({ type: 'CLEAR_SELECTION' });
      localDispatch({ type: 'EXIT_SELECTION_MODE' });
    } finally {
      setActionLoading(false);
    }
  };

  const displayStats = stats || {
    total: 0,
    videos: 0,
    documents: 0,
    favorites: 0,
  };

  return (
    <MainLayout>
      <div className="container mx-auto px-6 py-8 max-w-7xl">
        {/* 页面标题 */}
        <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4 mb-8">
          <div>
            <h1 className="text-2xl font-bold text-surface-900 dark:text-surface-100 mb-1">资源管理</h1>
            <p className="text-surface-500 dark:text-surface-400">管理和上传教学资源</p>
          </div>
          <div className="flex gap-2">
            {activeView === 'resources' && <Button
              variant={state.selectionMode ? "primary" : "outline"}
              onClick={() => localDispatch({ type: 'TOGGLE_SELECTION_MODE' })}
            >
              <Check className="w-4 h-4 mr-2" />
              {state.selectionMode ? '退出选择' : '批量选择'}
            </Button>}
            <Button onClick={() => localDispatch({ type: 'OPEN_BATCH_IMPORT' })}>
              <Upload className="w-4 h-4 mr-2" />
              上传资源
            </Button>
          </div>
        </div>

        <Tabs defaultValue="resources" value={activeView} onValueChange={(value) => {
          setActiveView(value);
          if (value === 'resources') {
            void loadResources(currentFilter);
            void loadStats();
          }
        }} keepMounted={false}>
          <TabsList className="mb-4">
            <TabsTrigger value="resources">资源列表</TabsTrigger>
            <TabsTrigger value="ingestions">文档处理</TabsTrigger>
          </TabsList>
          <TabsContent value="ingestions">
            <IngestionPanel key={ingestionRevision} onChanged={() => { void loadResources(currentFilter); void loadStats(); }} />
          </TabsContent>
          <TabsContent value="resources">
        {listError ? (
          <RequestErrorNotice
            error={listError}
            onRetry={() => void loadResources(currentFilter)}
            onRefresh={() => void loadResources(currentFilter)}
            className="mb-6"
          />
        ) : null}
        {statsError ? (
          <RequestErrorNotice
            error={statsError}
            onRetry={() => void loadStats()}
            onRefresh={() => void loadStats()}
            className="mb-6"
          />
        ) : null}

        {/* 批量操作栏 */}
        {state.selectionMode && (
          <BatchSelectionBar
            selectedCount={state.selectedResourceIds.size}
            totalCount={resources.length}
            onToggleSelectAll={() => {
              if (state.selectedResourceIds.size === resources.length) {
                localDispatch({ type: 'CLEAR_SELECTION' });
              } else {
                localDispatch({ type: 'SELECT_ALL', payload: resources.map(r => r.id) });
              }
            }}
            onBatchDelete={() => localDispatch({ type: 'OPEN_BATCH_DELETE_CONFIRM' })}
          />
        )}

        {/* 统计卡片 */}
        <ResourceStatsCards
          stats={displayStats}
          loading={statsLoading}
          onTypeSelect={(type: FilterType) => localDispatch({ type: 'SET_SELECTED_TYPE', payload: type })}
        />

        {/* 筛选和搜索 */}
        <ResourceFilters
          searchTerm={state.searchTerm}
          selectedType={state.selectedType}
          viewMode={state.viewMode}
          onSearchChange={(term: string) => localDispatch({ type: 'SET_SEARCH_TERM', payload: term })}
          onTypeChange={(type: FilterType) => localDispatch({ type: 'SET_SELECTED_TYPE', payload: type })}
          onViewModeChange={(mode: ViewMode) => localDispatch({ type: 'SET_VIEW_MODE', payload: mode })}
        />

        {/* 加载状态 */}
        {loading ? (
          <div className="flex items-center justify-center py-16">
            <Loader2 className="h-8 w-8 animate-spin text-primary-500" />
            <span className="ml-2 text-surface-500">加载中...</span>
          </div>
        ) : (
          <>
            {/* 资源列表 */}
            {state.viewMode === 'grid' ? (
              <ResourceGridView
                resources={resources}
                selectionMode={state.selectionMode}
                selectedResourceIds={state.selectedResourceIds}
                onToggleSelection={(id: string) => localDispatch({ type: 'TOGGLE_RESOURCE_SELECTION', payload: id })}
                onViewResource={(resource) => localDispatch({ type: 'SET_VIEWING_RESOURCE', payload: resource })}
                onEditResource={(resource) => localDispatch({ type: 'OPEN_EDIT', payload: resource })}
                onDeleteResource={(id: string) => localDispatch({ type: 'OPEN_DELETE_CONFIRM', payload: id })}
                onOpenBatchImport={() => localDispatch({ type: 'OPEN_BATCH_IMPORT' })}
              />
            ) : (
              <ResourceListView
                resources={resources}
                selectionMode={state.selectionMode}
                selectedResourceIds={state.selectedResourceIds}
                onToggleSelection={(id: string) => localDispatch({ type: 'TOGGLE_RESOURCE_SELECTION', payload: id })}
                onToggleSelectAll={() => {
                  if (state.selectedResourceIds.size === resources.length) {
                    localDispatch({ type: 'CLEAR_SELECTION' });
                  } else {
                    localDispatch({ type: 'SELECT_ALL', payload: resources.map(r => r.id) });
                  }
                }}
                onViewResource={(resource) => localDispatch({ type: 'SET_VIEWING_RESOURCE', payload: resource })}
                onEditResource={(resource) => localDispatch({ type: 'OPEN_EDIT', payload: resource })}
                onDeleteResource={(id: string) => localDispatch({ type: 'OPEN_DELETE_CONFIRM', payload: id })}
              />
            )}

            {resources.length === 0 && (
              <div className="text-center py-16">
                <FolderOpen className="w-16 h-16 text-surface-300 dark:text-surface-600 mx-auto mb-4" />
                <p className="text-surface-500 dark:text-surface-400 mb-4">没有找到匹配的资源</p>
                <Button onClick={() => localDispatch({ type: 'OPEN_BATCH_IMPORT' })}>
                  <Upload className="w-4 h-4 mr-2" />
                  上传第一个资源
                </Button>
              </div>
            )}
          </>
        )}
          </TabsContent>
        </Tabs>
      </div>

      {/* 删除确认模态框 */}
      <ConfirmDialog
        isOpen={!!state.deleteConfirmId}
        onClose={() => localDispatch({ type: 'CLOSE_DELETE_CONFIRM' })}
        onConfirm={() => state.deleteConfirmId && handleDelete(state.deleteConfirmId)}
        loading={actionLoading}
        title="确认删除"
        message="确定要删除这个资源吗？此操作无法撤销。"
      />

      {/* 批量删除确认模态框 */}
      <ConfirmDialog
        isOpen={state.showBatchDeleteConfirm}
        onClose={() => localDispatch({ type: 'CLOSE_BATCH_DELETE_CONFIRM' })}
        onConfirm={handleBatchDelete}
        loading={actionLoading}
        title="确认批量删除"
        message={`确定要删除选中的 ${state.selectedResourceIds.size} 个资源吗？此操作无法撤销。`}
        count={state.selectedResourceIds.size}
      />

      {/* 资源详情模态框 */}
      <ResourceDetailModal
        resource={state.viewingResource}
        onClose={() => localDispatch({ type: 'CLOSE_VIEWING_RESOURCE' })}
        onEdit={(resource) => {
          localDispatch({ type: 'OPEN_EDIT', payload: resource });
          localDispatch({ type: 'CLOSE_VIEWING_RESOURCE' });
        }}
      />

      {/* 编辑资源模态框 */}
      <ResourceEditModal
        resource={state.editingResource}
        onClose={() => localDispatch({ type: 'CLOSE_EDIT' })}
        onSuccess={() => {
          localDispatch({ type: 'CLOSE_EDIT' });
          void loadResources(currentFilter);
        }}
      />

      {/* 批量导入模态框 */}
      <BatchImportModal
        isOpen={state.showBatchImportModal}
        onClose={() => localDispatch({ type: 'CLOSE_BATCH_IMPORT' })}
        onSuccess={(documentsAccepted) => {
          localDispatch({ type: 'CLOSE_BATCH_IMPORT' });
          if (documentsAccepted) {
            setIngestionRevision((value) => value + 1);
            setActiveView('ingestions');
          }
          void loadResources(currentFilter);
          void loadStats();
        }}
      />
    </MainLayout>
  );
};

export default TeacherResourcesPage;
