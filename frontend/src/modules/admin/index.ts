/**
 * Admin 模块 - 管理员功能
 */

// Hooks
export { useAIConfig, useProviders, useModels, useAgentConfigs, useTestConnection } from './hooks/useAIConfig';

// Components
export { ChannelFormModal } from './components/ChannelFormModal';
export { ChannelCard } from './components/ChannelCard';
export { AgentConfigPanel } from './components/AgentConfigPanel';
export { EmbeddingConfigPanel } from './components/EmbeddingConfigPanel';
export { AdminLayout } from './components/AdminLayout';

// Constants
export { PROVIDER_PRESETS, getProviderPreset, getAllPresetModels, getRelatedModels } from './constants/providerPresets';

// Services
export { default as adminStatsService } from './services/adminStatsService';
export { default as adminUserService } from './services/adminUserService';
export { default as securityLogService } from './services/securityLogService';
export { default as systemSettingService } from './services/systemSettingService';
export { default as knowledgeAdminService } from './services/knowledgeAdminService';

// Store
export { default as adminStatsReducer } from './store/adminStatsSlice';
export { default as securityLogReducer } from './store/securityLogSlice';
export { default as knowledgeAdminReducer } from './store/knowledgeAdminSlice';
