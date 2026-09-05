import React, { useEffect, useState } from 'react';
import { MainLayout } from '../../components/layout/MainLayout';
import { Card, CardContent, CardHeader } from '../../components/ui/Card';
import { Button } from '../../components/ui/Button';
import { RequestErrorNotice } from '@/components/feedback';
import { Badge } from '../../components/ui/Badge';
import { Input } from '../../components/ui/Input';
import { Select } from '../../components/ui/Select';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/Tabs';
import {
  Search,
  Video,
  FileText,
  Download,
  ExternalLink,
  Star,
  Eye,
  Heart,
  Play,
  Loader2,
} from 'lucide-react';
import { useAppDispatch, useAppSelector } from '@/store';
import {
  fetchResources,
  fetchResourceStats,
  toggleFavorite,
} from '@/modules/resource/store/resourceSlice';
import { getInitialResourceSearch, openResourceUrl } from '@/libs/utils/resourceUtils';
import type { ResourceType, Resource } from '@/modules/resource/types/resource';
import { toAppErrorFeedback } from '@/libs/http/appError';
import { useToast } from '@/components/ui/Toast';
import { KnowledgeSearch } from '@/modules/resource/components/KnowledgeSearch';

const typeOptions = [
  { value: '', label: '全部类型' },
  { value: 'video', label: '视频' },
  { value: 'document', label: '文档' },
];

const chapterOptions = [
  { value: '', label: '全部章节' },
  { value: '第一章', label: '第一章 - 极限' },
  { value: '第二章', label: '第二章 - 导数' },
  { value: '第三章', label: '第三章 - 导数应用' },
  { value: '第四章', label: '第四章 - 不定积分' },
  { value: '第五章', label: '第五章 - 定积分' },
];

const getTypeIcon = (type: string) => {
  switch (type) {
    case 'video':
      return <Video className="h-5 w-5" />;
    case 'document':
      return <FileText className="h-5 w-5" />;
    default:
      return <FileText className="h-5 w-5" />;
  }
};

const getTypeBadge = (type: string) => {
  switch (type) {
    case 'video':
      return <Badge variant="default">视频</Badge>;
    case 'document':
      return <Badge variant="secondary">文档</Badge>;
    default:
      return <Badge variant="outline">{type}</Badge>;
  }
};

export const ResourcesPage: React.FC = () => {
  const dispatch = useAppDispatch();
  const { resources, stats, loading, statsLoading, actionLoading, loadError, statsError } = useAppSelector(
    (state) => state.resource
  );
  const { toast } = useToast();

  const [searchTerm, setSearchTerm] = useState(() =>
    getInitialResourceSearch(window.location.search)
  );
  const [selectedType, setSelectedType] = useState('');
  const [selectedChapter, setSelectedChapter] = useState('');
  const [activeTab, setActiveTab] = useState('all');

  // 初始加载统计
  useEffect(() => {
    dispatch(fetchResourceStats());
  }, [dispatch]);

  // 筛选变化时重新加载（含搜索防抖）
  useEffect(() => {
    if (activeTab === 'knowledge') return;
    const timer = setTimeout(() => {
      const filter: {
        type?: ResourceType;
        chapter?: string;
        search?: string;
        favorites_only?: boolean;
      } = {};

      if (selectedType) {
        filter.type = selectedType as ResourceType;
      }
      if (selectedChapter) {
        filter.chapter = selectedChapter;
      }
      if (searchTerm) {
        filter.search = searchTerm;
      }
      if (activeTab === 'favorites') {
        filter.favorites_only = true;
      }

      dispatch(fetchResources(filter));
    }, searchTerm ? 300 : 0);

    return () => clearTimeout(timer);
  }, [dispatch, selectedType, selectedChapter, activeTab, searchTerm]);

  const handleToggleFavorite = async (id: string) => {
    const result = await dispatch(toggleFavorite(id));
    if (toggleFavorite.rejected.match(result)) {
      const feedback = toAppErrorFeedback(result.payload, '收藏状态更新失败');
      if (feedback) toast(feedback);
    }
  };

  const retryResources = () => {
    const filter: {
      type?: ResourceType;
      chapter?: string;
      search?: string;
      favorites_only?: boolean;
    } = {};
    if (selectedType) filter.type = selectedType as ResourceType;
    if (selectedChapter) filter.chapter = selectedChapter;
    if (searchTerm) filter.search = searchTerm;
    if (activeTab === 'favorites') filter.favorites_only = true;
    void dispatch(fetchResources(filter));
  };

  const handleOpenResource = (resource: Resource) => {
    openResourceUrl(resource.url);
  };

  const displayStats = stats || {
    videos: 0,
    documents: 0,
    favorites: 0,
  };

  return (
    <MainLayout>
      <div className="container mx-auto px-6 py-8 max-w-7xl">
        {/* 页面标题 */}
        <div className="mb-8">
          <h1 className="text-3xl font-bold text-surface-900 dark:text-surface-100 mb-2">
            资源中心
          </h1>
          <p className="text-surface-500 dark:text-surface-400">
            精选学习资源，助你更好地理解高等数学
          </p>
        </div>

        {/* 统计卡片 */}
        {statsError ? (
          <RequestErrorNotice
            error={statsError}
            onRetry={() => void dispatch(fetchResourceStats())}
            onRefresh={() => void dispatch(fetchResourceStats())}
            className="mb-6"
          />
        ) : null}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-8">
          <Card>
            <CardContent className="p-4 flex items-center gap-4">
              <div className="w-10 h-10 rounded-lg bg-primary-100 dark:bg-primary-900/30 flex items-center justify-center">
                <Video className="h-5 w-5 text-primary-600 dark:text-primary-400" />
              </div>
              <div>
                <div className="text-2xl font-bold text-surface-900 dark:text-surface-100">
                  {statsLoading ? '-' : displayStats.videos}
                </div>
                <div className="text-xs text-surface-500 dark:text-surface-400">教学视频</div>
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="p-4 flex items-center gap-4">
              <div className="w-10 h-10 rounded-lg bg-secondary-100 dark:bg-secondary-900/30 flex items-center justify-center">
                <FileText className="h-5 w-5 text-secondary-600 dark:text-secondary-400" />
              </div>
              <div>
                <div className="text-2xl font-bold text-surface-900 dark:text-surface-100">
                  {statsLoading ? '-' : displayStats.documents}
                </div>
                <div className="text-xs text-surface-500 dark:text-surface-400">学习文档</div>
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="p-4 flex items-center gap-4">
              <div className="w-10 h-10 rounded-lg bg-red-100 dark:bg-red-900/30 flex items-center justify-center">
                <Heart className="h-5 w-5 text-red-600 dark:text-red-400" />
              </div>
              <div>
                <div className="text-2xl font-bold text-surface-900 dark:text-surface-100">
                  {statsLoading ? '-' : displayStats.favorites}
                </div>
                <div className="text-xs text-surface-500 dark:text-surface-400">我的收藏</div>
              </div>
            </CardContent>
          </Card>
        </div>

        {/* 搜索和筛选 */}
        <Card className="mb-6">
          <CardContent className="p-4">
            <div className="flex flex-col md:flex-row gap-4">
              <div className="flex-1 relative">
                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-surface-400" />
                <Input
                  placeholder="搜索资源..."
                  aria-label={activeTab === 'knowledge' ? '知识搜索' : '搜索资源'}
                  maxLength={activeTab === 'knowledge' ? 2000 : undefined}
                  value={searchTerm}
                  onChange={(e) => setSearchTerm(e.target.value)}
                  className="pl-10"
                />
              </div>
              <div className="flex min-w-0 flex-col gap-2 min-[400px]:flex-row">
                <Select
                  options={typeOptions}
                  value={selectedType}
                  onChange={setSelectedType}
                  className="min-w-0 flex-1 md:w-28 md:flex-none"
                />
                <Select
                  options={chapterOptions}
                  value={selectedChapter}
                  onChange={setSelectedChapter}
                  className="min-w-0 flex-1 md:w-40 md:flex-none"
                />
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 资源列表 */}
        <Card>
          <CardHeader>
            <Tabs defaultValue="all" onValueChange={setActiveTab}>
              <TabsList className="max-w-full">
                <TabsTrigger value="all" className="px-2 text-xs sm:px-3 sm:text-sm">全部资源</TabsTrigger>
                <TabsTrigger value="favorites" className="px-2 text-xs sm:px-3 sm:text-sm">我的收藏</TabsTrigger>
                <TabsTrigger value="knowledge" className="px-2 text-xs sm:px-3 sm:text-sm">知识搜索</TabsTrigger>
              </TabsList>
            </Tabs>
          </CardHeader>
          <CardContent>
            {activeTab === 'knowledge' ? (
              <KnowledgeSearch key={`${searchTerm}:${selectedType}:${selectedChapter}`} query={searchTerm} type={selectedType} chapter={selectedChapter} />
            ) : loadError ? (
              <RequestErrorNotice
                error={loadError}
                onRetry={retryResources}
                onRefresh={retryResources}
              />
            ) : loading ? (
              <div className="flex items-center justify-center py-12">
                <Loader2 className="h-8 w-8 animate-spin text-primary-500" />
                <span className="ml-2 text-surface-500">加载中...</span>
              </div>
            ) : (
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
                {resources.map((resource) => (
                  <Card key={resource.id} className="overflow-hidden hover:shadow-lg transition-shadow">
                    {/* 封面/预览 */}
                    <div className="relative h-40 bg-surface-100 dark:bg-surface-800 flex items-center justify-center">
                      <div
                        className={`w-16 h-16 rounded-full flex items-center justify-center ${
                          resource.type === 'video'
                            ? 'bg-primary-100 dark:bg-primary-900/30 text-primary-600 dark:text-primary-400'
                            : 'bg-secondary-100 dark:bg-secondary-900/30 text-secondary-600 dark:text-secondary-400'
                        }`}
                      >
                        {getTypeIcon(resource.type)}
                      </div>
                      {resource.type === 'video' && (
                        <div
                          className="absolute inset-0 flex items-center justify-center bg-black/20 opacity-0 hover:opacity-100 transition-opacity cursor-pointer"
                          onClick={() => handleOpenResource(resource)}
                        >
                          <div className="w-12 h-12 rounded-full bg-white/90 flex items-center justify-center">
                            <Play className="h-6 w-6 text-primary-600 ml-1" />
                          </div>
                        </div>
                      )}
                      <button
                        onClick={() => void handleToggleFavorite(resource.id)}
                        disabled={actionLoading}
                        className="absolute top-3 right-3 w-8 h-8 rounded-full bg-white dark:bg-surface-900 shadow-md flex items-center justify-center hover:scale-110 transition-transform disabled:opacity-50"
                      >
                        <Heart
                          className={`h-4 w-4 ${
                            resource.is_favorite
                              ? 'fill-red-500 text-red-500'
                              : 'text-surface-400'
                          }`}
                        />
                      </button>
                      <div className="absolute bottom-3 left-3">
                        {getTypeBadge(resource.type)}
                      </div>
                      {resource.type === 'video' && resource.duration && (
                        <div className="absolute bottom-3 right-3 px-2 py-1 rounded bg-black/70 text-white text-xs">
                          {resource.duration}
                        </div>
                      )}
                    </div>

                    <CardContent className="p-4">
                      <h3 className="font-semibold text-surface-900 dark:text-surface-100 mb-2 line-clamp-2">
                        {resource.title}
                      </h3>
                      <div className="flex items-center gap-2 text-sm text-surface-500 dark:text-surface-400 mb-3">
                        <span>{resource.source || '未知来源'}</span>
                        {resource.topic && (
                          <>
                            <span>·</span>
                            <Badge variant="outline" className="text-xs">
                              {resource.topic}
                            </Badge>
                          </>
                        )}
                      </div>
                      <div className="flex items-center justify-between text-xs text-surface-500 dark:text-surface-400">
                        <div className="flex items-center gap-3">
                          <div className="flex items-center gap-1">
                            <Eye className="h-3 w-3" />
                            <span>{resource.views >= 1000 ? `${(resource.views / 1000).toFixed(1)}k` : resource.views}</span>
                          </div>
                          <div className="flex items-center gap-1">
                            <Star className="h-3 w-3" />
                            <span>{resource.likes}</span>
                          </div>
                        </div>
                        <div className="flex gap-1">
                          {resource.type === 'video' ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="h-8"
                              onClick={() => handleOpenResource(resource)}
                            >
                              <ExternalLink className="h-3 w-3 mr-1" />
                              观看
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="h-8"
                              onClick={() => handleOpenResource(resource)}
                            >
                              <Download className="h-3 w-3 mr-1" />
                              下载
                            </Button>
                          )}
                        </div>
                      </div>
                    </CardContent>
                  </Card>
                ))}

                {resources.length === 0 && (
                  <div className="col-span-full text-center py-12">
                    <FileText className="h-12 w-12 mx-auto text-surface-400 mb-4" />
                    <h3 className="text-lg font-medium text-surface-900 dark:text-surface-100 mb-2">
                      {activeTab === 'favorites' ? '暂无收藏资源' : '未找到相关资源'}
                    </h3>
                    <p className="text-surface-500 dark:text-surface-400">
                      {activeTab === 'favorites'
                        ? '收藏一些资源后会在这里显示'
                        : '尝试调整筛选条件或搜索关键词'}
                    </p>
                  </div>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </MainLayout>
  );
};
