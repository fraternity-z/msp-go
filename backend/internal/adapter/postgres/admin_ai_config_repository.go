package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	adminaiconfigapp "mathstudy/backend/internal/application/adminaiconfig"
)

// AdminAIConfigRepository persists LLM provider, model, and agent configuration.
type AdminAIConfigRepository struct {
	Repository
}

// NewAdminAIConfigRepository creates a PostgreSQL-backed admin AI config repository.
func NewAdminAIConfigRepository(db Querier) (AdminAIConfigRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return AdminAIConfigRepository{}, err
	}
	return AdminAIConfigRepository{Repository: base}, nil
}

// ListProviders returns LLM providers without API keys.
func (r AdminAIConfigRepository) ListProviders(ctx context.Context, includeInactive bool) ([]adminaiconfigapp.LLMProvider, error) {
	where := ""
	if !includeInactive {
		where = "WHERE is_active"
	}
	rows, err := r.DB().Query(ctx, `
		SELECT id, name, code, base_url, priority, weight, is_active, description, created_at, updated_at
		FROM public.llm_providers
		`+where+`
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	providers := []adminaiconfigapp.LLMProvider{}
	for rows.Next() {
		provider, err := scanLLMProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

// GetProvider returns one provider including its encrypted API key.
func (r AdminAIConfigRepository) GetProvider(ctx context.Context, providerID string) (adminaiconfigapp.StoredProvider, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT id, name, code, base_url, encrypted_api_key, priority, weight, is_active, description, created_at, updated_at
		FROM public.llm_providers
		WHERE id = $1`,
		providerID,
	)
	provider, err := scanStoredProvider(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return adminaiconfigapp.StoredProvider{}, false, nil
		}
		return adminaiconfigapp.StoredProvider{}, false, err
	}
	return provider, true, nil
}

// CreateProvider inserts one LLM provider.
func (r AdminAIConfigRepository) CreateProvider(ctx context.Context, input adminaiconfigapp.ProviderInput, now time.Time) (adminaiconfigapp.LLMProvider, error) {
	row := r.DB().QueryRow(ctx, `
		INSERT INTO public.llm_providers (
			id, name, code, base_url, encrypted_api_key, priority, weight, is_active, description, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		RETURNING id, name, code, base_url, priority, weight, is_active, description, created_at, updated_at`,
		input.ID,
		input.Name,
		input.Code,
		input.BaseURL,
		input.EncryptedAPIKey,
		input.Priority,
		input.Weight,
		input.IsActive,
		input.Description,
		now,
	)
	provider, err := scanLLMProvider(row)
	if err != nil {
		return adminaiconfigapp.LLMProvider{}, normalizeAIConfigPGError(err)
	}
	return provider, nil
}

// UpdateProvider updates one LLM provider.
func (r AdminAIConfigRepository) UpdateProvider(ctx context.Context, providerID string, update adminaiconfigapp.ProviderUpdate, now time.Time) (adminaiconfigapp.LLMProvider, bool, error) {
	var provider adminaiconfigapp.LLMProvider
	found := false
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		if err := tx.lockEmbeddingActivation(ctx); err != nil {
			return err
		}
		current, ok, err := tx.GetProvider(ctx, providerID)
		if err != nil || !ok {
			return err
		}
		found = true
		name, baseURL, encryptedAPIKey := current.Name, current.BaseURL, current.EncryptedAPIKey
		priority, weight, isActive, description := current.Priority, current.Weight, current.IsActive, current.Description
		if update.Name != nil {
			name = *update.Name
		}
		if update.BaseURL != nil {
			baseURL = *update.BaseURL
		}
		if update.EncryptedAPIKey != nil {
			encryptedAPIKey = *update.EncryptedAPIKey
		}
		if update.Priority != nil {
			priority = *update.Priority
		}
		if update.Weight != nil {
			weight = *update.Weight
		}
		if update.IsActive != nil {
			isActive = *update.IsActive
		}
		if update.DescriptionSet {
			description = update.Description
		}
		row := tx.DB().QueryRow(ctx, `
		UPDATE public.llm_providers
		SET name = $2,
			base_url = $3,
			encrypted_api_key = $4,
			priority = $5,
			weight = $6,
			is_active = $7,
			description = $8,
			updated_at = $9
		WHERE id = $1
		RETURNING id, name, code, base_url, priority, weight, is_active, description, created_at, updated_at`,
			providerID, name, baseURL, encryptedAPIKey, priority, weight, isActive, description, now,
		)
		var scanErr error
		provider, scanErr = scanLLMProvider(row)
		return normalizeAIConfigPGError(scanErr)
	})
	if err != nil {
		return adminaiconfigapp.LLMProvider{}, false, normalizeAIConfigPGError(err)
	}
	return provider, found, nil
}

// DeleteProvider deletes one provider and cascades its models.
func (r AdminAIConfigRepository) DeleteProvider(ctx context.Context, providerID string) (bool, error) {
	deleted := false
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		if err := tx.lockEmbeddingActivation(ctx); err != nil {
			return err
		}
		inUse, err := tx.providerHasActiveEmbedding(ctx, providerID)
		if err != nil {
			return err
		}
		if inUse {
			return activeEmbeddingSourceConflict()
		}
		tag, err := tx.DB().Exec(ctx, `DELETE FROM public.llm_providers WHERE id = $1`, providerID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// ListModels returns provider models with provider display data.
func (r AdminAIConfigRepository) ListModels(ctx context.Context, filter adminaiconfigapp.ModelFilter) ([]adminaiconfigapp.LLMModel, error) {
	conditions := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(filter.ProviderID) != "" {
		args = append(args, strings.TrimSpace(filter.ProviderID))
		conditions = append(conditions, fmt.Sprintf("m.provider_id = $%d", len(args)))
	}
	if !filter.IncludeInactive {
		conditions = append(conditions, "m.is_active")
		conditions = append(conditions, "p.is_active")
	}
	rows, err := r.DB().Query(ctx, `
		SELECT `+llmModelSelectColumns+`
		FROM public.llm_models m
		JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY p.created_at DESC, m.is_default DESC, m.created_at DESC, m.id DESC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := []adminaiconfigapp.LLMModel{}
	for rows.Next() {
		model, err := scanLLMModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

// GetModel returns one model with provider display data.
func (r AdminAIConfigRepository) GetModel(ctx context.Context, modelID string) (adminaiconfigapp.LLMModel, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT `+llmModelSelectColumns+`
		FROM public.llm_models m
		JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE m.id = $1`,
		modelID,
	)
	model, err := scanLLMModel(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return adminaiconfigapp.LLMModel{}, false, nil
		}
		return adminaiconfigapp.LLMModel{}, false, err
	}
	return model, true, nil
}

// ListRuntimeCandidates returns active channel implementations for one logical model name.
func (r AdminAIConfigRepository) ListRuntimeCandidates(ctx context.Context, modelKey string) ([]adminaiconfigapp.RuntimeCandidate, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT `+runtimeCandidateSelectColumns+`
		FROM public.llm_models m
		JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE m.name = $1 AND m.is_active AND p.is_active
		ORDER BY p.priority DESC, p.id ASC, m.is_default DESC, m.updated_at DESC, m.id ASC`,
		strings.TrimSpace(modelKey),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := []adminaiconfigapp.RuntimeCandidate{}
	for rows.Next() {
		candidate, err := scanRuntimeCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// CreateModel inserts one model.
func (r AdminAIConfigRepository) CreateModel(ctx context.Context, input adminaiconfigapp.ModelInput, now time.Time) (adminaiconfigapp.LLMModel, error) {
	var modelID string
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		isDefault := input.IsDefault
		if !isDefault {
			var existingDefaults int
			if err := tx.DB().QueryRow(ctx, `SELECT count(*)::int FROM public.llm_models WHERE provider_id = $1`, input.ProviderID).Scan(&existingDefaults); err != nil {
				return err
			}
			isDefault = existingDefaults == 0
		}
		if isDefault {
			if _, err := tx.DB().Exec(ctx, `UPDATE public.llm_models SET is_default = false, updated_at = $2 WHERE provider_id = $1`, input.ProviderID, now); err != nil {
				return err
			}
		}
		modelID = input.ID
		_, err := tx.DB().Exec(ctx, `
			INSERT INTO public.llm_models (
				id, provider_id, name, model_id, default_temperature, default_max_tokens,
				default_top_p, default_timeout, default_max_retries, is_active, is_default,
				capabilities, description, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::json, $13, $14, $14)`,
			input.ID,
			input.ProviderID,
			input.Name,
			input.ModelID,
			input.DefaultTemperature,
			input.DefaultMaxTokens,
			input.DefaultTopP,
			input.DefaultTimeout,
			input.DefaultMaxRetries,
			input.IsActive,
			isDefault,
			jsonObject(input.Capabilities),
			input.Description,
			now,
		)
		return normalizeAIConfigPGError(err)
	})
	if err != nil {
		return adminaiconfigapp.LLMModel{}, err
	}
	model, ok, err := r.GetModel(ctx, modelID)
	if err != nil {
		return adminaiconfigapp.LLMModel{}, err
	}
	if !ok {
		return adminaiconfigapp.LLMModel{}, pgx.ErrNoRows
	}
	return model, nil
}

// UpdateModel updates one model.
func (r AdminAIConfigRepository) UpdateModel(ctx context.Context, modelID string, update adminaiconfigapp.ModelUpdate, now time.Time) (adminaiconfigapp.LLMModel, bool, error) {
	var model adminaiconfigapp.LLMModel
	found := false
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		if err := tx.lockEmbeddingActivation(ctx); err != nil {
			return err
		}
		current, ok, err := tx.GetModel(ctx, modelID)
		if err != nil || !ok {
			return err
		}
		found = true
		if update.ModelID != nil && *update.ModelID != current.ModelID {
			inUse, err := tx.embeddingModelIsActive(ctx, modelID)
			if err != nil {
				return err
			}
			if inUse {
				return activeEmbeddingSourceConflict()
			}
		}
		name := current.Name
		providerModelID := current.ModelID
		temperature := current.DefaultTemperature
		maxTokens := current.DefaultMaxTokens
		topP := current.DefaultTopP
		timeout := current.DefaultTimeout
		retries := current.DefaultMaxRetries
		isActive := current.IsActive
		capabilities := current.Capabilities
		description := current.Description
		if update.Name != nil {
			name = *update.Name
		}
		if update.ModelID != nil {
			providerModelID = *update.ModelID
		}
		if update.DefaultTemperature != nil {
			temperature = *update.DefaultTemperature
		}
		if update.DefaultMaxTokensSet {
			maxTokens = update.DefaultMaxTokens
		}
		if update.DefaultTopPSet {
			topP = update.DefaultTopP
		}
		if update.DefaultTimeout != nil {
			timeout = *update.DefaultTimeout
		}
		if update.DefaultMaxRetries != nil {
			retries = *update.DefaultMaxRetries
		}
		if update.IsActive != nil {
			isActive = *update.IsActive
		}
		if update.CapabilitiesSet {
			capabilities = update.Capabilities
		}
		if update.DescriptionSet {
			description = update.Description
		}
		_, err = tx.DB().Exec(ctx, `
		UPDATE public.llm_models
		SET name = $2,
			model_id = $3,
			default_temperature = $4,
			default_max_tokens = $5,
			default_top_p = $6,
			default_timeout = $7,
			default_max_retries = $8,
			is_active = $9,
			capabilities = $10::json,
			description = $11,
			updated_at = $12
		WHERE id = $1`,
			modelID,
			name,
			providerModelID,
			temperature,
			maxTokens,
			topP,
			timeout,
			retries,
			isActive,
			jsonObject(capabilities),
			description,
			now,
		)
		if err != nil {
			return normalizeAIConfigPGError(err)
		}
		model, found, err = tx.GetModel(ctx, modelID)
		return err
	})
	if err != nil {
		return adminaiconfigapp.LLMModel{}, false, normalizeAIConfigPGError(err)
	}
	return model, found, nil
}

// DeleteModel deletes one model.
func (r AdminAIConfigRepository) DeleteModel(ctx context.Context, modelID string) (bool, error) {
	deleted := false
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		if err := tx.lockEmbeddingActivation(ctx); err != nil {
			return err
		}
		inUse, err := tx.embeddingModelIsActive(ctx, modelID)
		if err != nil {
			return err
		}
		if inUse {
			return activeEmbeddingSourceConflict()
		}
		var providerID string
		var wasDefault bool
		err = tx.DB().QueryRow(ctx, `SELECT provider_id, is_default FROM public.llm_models WHERE id = $1`, modelID).Scan(&providerID, &wasDefault)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		tag, err := tx.DB().Exec(ctx, `DELETE FROM public.llm_models WHERE id = $1`, modelID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		deleted = true
		if !wasDefault {
			return nil
		}
		_, err = tx.DB().Exec(ctx, `
			UPDATE public.llm_models
			SET is_default = true, updated_at = now()
			WHERE id = (
				SELECT id FROM public.llm_models
				WHERE provider_id = $1 AND is_active
				ORDER BY created_at DESC, id DESC
				LIMIT 1
			)`,
			providerID,
		)
		return err
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// SetDefaultModel marks one model as the provider default.
func (r AdminAIConfigRepository) SetDefaultModel(ctx context.Context, modelID string, now time.Time) (bool, error) {
	var providerID string
	if err := r.DB().QueryRow(ctx, `SELECT provider_id FROM public.llm_models WHERE id = $1`, modelID).Scan(&providerID); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if _, err := r.DB().Exec(ctx, `UPDATE public.llm_models SET is_default = false, updated_at = $2 WHERE provider_id = $1`, providerID, now); err != nil {
		return false, err
	}
	tag, err := r.DB().Exec(ctx, `UPDATE public.llm_models SET is_default = true, is_active = true, updated_at = $2 WHERE id = $1`, modelID, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ReplaceProviderModels applies an ordered provider model list while preserving unchanged IDs.
func (r AdminAIConfigRepository) ReplaceProviderModels(ctx context.Context, providerID string, inputs []adminaiconfigapp.ModelInput, now time.Time) (adminaiconfigapp.ModelsUpdateResult, error) {
	result := adminaiconfigapp.ModelsUpdateResult{}
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		if err := tx.lockEmbeddingActivation(ctx); err != nil {
			return err
		}
		current, err := tx.listProviderModelsForReplace(ctx, providerID)
		if err != nil {
			return err
		}
		desired := map[string]adminaiconfigapp.ModelInput{}
		order := make([]string, 0, len(inputs))
		for _, input := range inputs {
			if _, ok := desired[input.ModelID]; ok {
				continue
			}
			desired[input.ModelID] = input
			order = append(order, input.ModelID)
		}
		defaultModelID := ""
		for _, model := range current {
			if model.IsDefault {
				if _, ok := desired[model.ModelID]; ok {
					defaultModelID = model.ModelID
					break
				}
			}
		}
		if defaultModelID == "" && len(order) > 0 {
			defaultModelID = order[0]
		}
		for _, model := range current {
			if _, ok := desired[model.ModelID]; ok {
				continue
			}
			inUse, err := tx.embeddingModelIsActive(ctx, model.ID)
			if err != nil {
				return err
			}
			if inUse {
				return adminaiconfigapp.Error{Kind: adminaiconfigapp.ErrConflict, Message: "模型列表包含当前向量模型，请先激活其他向量模型"}
			}
			if _, err := tx.DB().Exec(ctx, `DELETE FROM public.llm_models WHERE id = $1`, model.ID); err != nil {
				return err
			}
			result.Removed++
		}
		for _, modelID := range order {
			input := desired[modelID]
			isDefault := modelID == defaultModelID
			if existing, ok := findModelByProviderModelID(current, modelID); ok {
				_, err := tx.DB().Exec(ctx, `
					UPDATE public.llm_models
					SET name = $2,
						is_active = true,
						is_default = $3,
						updated_at = $4
					WHERE id = $1`,
					existing.ID,
					input.Name,
					isDefault,
					now,
				)
				if err != nil {
					return err
				}
				result.Unchanged++
				continue
			}
			_, err := tx.DB().Exec(ctx, `
				INSERT INTO public.llm_models (
					id, provider_id, name, model_id, default_temperature, default_max_tokens,
					default_top_p, default_timeout, default_max_retries, is_active, is_default,
					capabilities, description, created_at, updated_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $11::json, $12, $13, $13)`,
				input.ID,
				providerID,
				input.Name,
				input.ModelID,
				input.DefaultTemperature,
				input.DefaultMaxTokens,
				input.DefaultTopP,
				input.DefaultTimeout,
				input.DefaultMaxRetries,
				isDefault,
				jsonObject(input.Capabilities),
				input.Description,
				now,
			)
			if err != nil {
				return normalizeAIConfigPGError(err)
			}
			result.Added++
		}
		return nil
	})
	if err != nil {
		return adminaiconfigapp.ModelsUpdateResult{}, err
	}
	models, err := r.ListModels(ctx, adminaiconfigapp.ModelFilter{ProviderID: providerID, IncludeInactive: true})
	if err != nil {
		return adminaiconfigapp.ModelsUpdateResult{}, err
	}
	result.Models = models
	return result, nil
}

// ListAgentConfigs returns all configured agent model mappings.
func (r AdminAIConfigRepository) ListAgentConfigs(ctx context.Context) ([]adminaiconfigapp.AgentModelConfig, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT `+agentConfigSelectColumns+`
		FROM public.agent_model_configs a
		LEFT JOIN public.llm_models m ON m.id = a.model_id
		LEFT JOIN public.llm_providers p ON p.id = m.provider_id
		ORDER BY a.agent_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := []adminaiconfigapp.AgentModelConfig{}
	for rows.Next() {
		config, err := scanAgentModelConfig(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

// GetAgentConfig returns one configured agent model mapping.
func (r AdminAIConfigRepository) GetAgentConfig(ctx context.Context, agentType string) (adminaiconfigapp.AgentModelConfig, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT `+agentConfigSelectColumns+`
		FROM public.agent_model_configs a
		LEFT JOIN public.llm_models m ON m.id = a.model_id
		LEFT JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE a.agent_type = $1`,
		agentType,
	)
	config, err := scanAgentModelConfig(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return adminaiconfigapp.AgentModelConfig{}, false, nil
		}
		return adminaiconfigapp.AgentModelConfig{}, false, err
	}
	return config, true, nil
}

// UpsertAgentConfig inserts or updates one agent mapping.
func (r AdminAIConfigRepository) UpsertAgentConfig(ctx context.Context, input adminaiconfigapp.AgentConfigInput, now time.Time) (adminaiconfigapp.AgentModelConfig, error) {
	_, err := r.DB().Exec(ctx, `
		INSERT INTO public.agent_model_configs (
			id, agent_type, model_id, model_key, temperature_override, max_tokens_override,
			top_p_override, timeout_override, max_retries_override, extra_config,
			is_active, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::json, $11, $12, $12)
		ON CONFLICT (agent_type) DO UPDATE
		SET model_id = EXCLUDED.model_id,
			model_key = EXCLUDED.model_key,
			temperature_override = EXCLUDED.temperature_override,
			max_tokens_override = EXCLUDED.max_tokens_override,
			top_p_override = EXCLUDED.top_p_override,
			timeout_override = EXCLUDED.timeout_override,
			max_retries_override = EXCLUDED.max_retries_override,
			extra_config = EXCLUDED.extra_config,
			is_active = EXCLUDED.is_active,
			updated_at = EXCLUDED.updated_at`,
		input.ID,
		input.AgentType,
		input.ModelID,
		input.ModelKey,
		input.TemperatureOverride,
		input.MaxTokensOverride,
		input.TopPOverride,
		input.TimeoutOverride,
		input.MaxRetriesOverride,
		jsonObject(input.ExtraConfig),
		input.IsActive,
		now,
	)
	if err != nil {
		return adminaiconfigapp.AgentModelConfig{}, normalizeAIConfigPGError(err)
	}
	config, ok, err := r.GetAgentConfig(ctx, input.AgentType)
	if err != nil {
		return adminaiconfigapp.AgentModelConfig{}, err
	}
	if !ok {
		return adminaiconfigapp.AgentModelConfig{}, pgx.ErrNoRows
	}
	return config, nil
}

// DeleteAgentConfig removes one agent mapping.
func (r AdminAIConfigRepository) DeleteAgentConfig(ctx context.Context, agentType string) (bool, error) {
	tag, err := r.DB().Exec(ctx, `DELETE FROM public.agent_model_configs WHERE agent_type = $1`, agentType)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r AdminAIConfigRepository) listProviderModelsForReplace(ctx context.Context, providerID string) ([]adminaiconfigapp.LLMModel, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT `+llmModelSelectColumns+`
		FROM public.llm_models m
		JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE m.provider_id = $1
		ORDER BY m.created_at ASC, m.id ASC`,
		providerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := []adminaiconfigapp.LLMModel{}
	for rows.Next() {
		model, err := scanLLMModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func (r AdminAIConfigRepository) withTx(ctx context.Context, fn func(AdminAIConfigRepository) error) error {
	return withRepositoryTx(ctx, "admin ai config", r.Repository, func(base Repository) AdminAIConfigRepository {
		return AdminAIConfigRepository{Repository: base}
	}, fn)
}

func (r AdminAIConfigRepository) embeddingModelIsActive(ctx context.Context, modelID string) (bool, error) {
	return r.Exists(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM public.embedding_model_versions
			WHERE llm_model_id = $1 AND status = 'active'
		)`,
		modelID,
	)
}

func (r AdminAIConfigRepository) providerHasActiveEmbedding(ctx context.Context, providerID string) (bool, error) {
	return r.Exists(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM public.embedding_model_versions v
			JOIN public.llm_models m ON m.id = v.llm_model_id
			WHERE m.provider_id = $1 AND v.status = 'active'
		)`, providerID)
}

func (r AdminAIConfigRepository) lockEmbeddingActivation(ctx context.Context) error {
	var ignored any
	return r.DB().QueryRow(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, adminaiconfigapp.ResourceEmbeddingLogicalName).Scan(&ignored)
}

func activeEmbeddingSourceConflict() error {
	return adminaiconfigapp.Error{Kind: adminaiconfigapp.ErrConflict, Message: "当前渠道或模型正被向量模型使用，请先激活其他向量模型"}
}

const llmModelSelectColumns = `
	m.id,
	m.provider_id,
	m.name,
	m.model_id,
	m.default_temperature,
	m.default_max_tokens,
	m.default_top_p,
	m.default_timeout,
	m.default_max_retries,
	m.is_active,
	m.is_default,
	m.capabilities,
	m.description,
	m.created_at,
	m.updated_at,
	p.name AS provider_name,
	p.code AS provider_code`

const agentConfigSelectColumns = `
	a.id,
	a.agent_type,
	a.model_id,
	a.model_key,
	a.temperature_override,
	a.max_tokens_override,
	a.top_p_override,
	a.timeout_override,
	a.max_retries_override,
	a.extra_config,
	a.is_active,
	a.created_at,
	a.updated_at,
	m.name AS model_name,
	m.model_id AS model_model_id,
	p.name AS provider_name`

const runtimeCandidateSelectColumns = `
	p.id,
	p.name,
	p.code,
	p.base_url,
	p.encrypted_api_key,
	p.priority,
	p.weight,
	p.is_active,
	p.description,
	p.created_at,
	p.updated_at,
	m.id,
	m.provider_id,
	m.name,
	m.model_id,
	m.default_temperature,
	m.default_max_tokens,
	m.default_top_p,
	m.default_timeout,
	m.default_max_retries,
	m.is_active,
	m.is_default,
	m.capabilities,
	m.description,
	m.created_at,
	m.updated_at`

func scanLLMProvider(scanner rowScanner) (adminaiconfigapp.LLMProvider, error) {
	var provider adminaiconfigapp.LLMProvider
	var description pgtype.Text
	if err := scanner.Scan(
		&provider.ID,
		&provider.Name,
		&provider.Code,
		&provider.BaseURL,
		&provider.Priority,
		&provider.Weight,
		&provider.IsActive,
		&description,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	); err != nil {
		return adminaiconfigapp.LLMProvider{}, err
	}
	provider.Description = textPtr(description)
	return provider, nil
}

func scanStoredProvider(scanner rowScanner) (adminaiconfigapp.StoredProvider, error) {
	var provider adminaiconfigapp.StoredProvider
	var description pgtype.Text
	if err := scanner.Scan(
		&provider.ID,
		&provider.Name,
		&provider.Code,
		&provider.BaseURL,
		&provider.EncryptedAPIKey,
		&provider.Priority,
		&provider.Weight,
		&provider.IsActive,
		&description,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	); err != nil {
		return adminaiconfigapp.StoredProvider{}, err
	}
	provider.Description = textPtr(description)
	return provider, nil
}

func scanLLMModel(scanner rowScanner) (adminaiconfigapp.LLMModel, error) {
	var model adminaiconfigapp.LLMModel
	var maxTokens pgtype.Int4
	var topP pgtype.Float8
	var capabilitiesRaw []byte
	var description pgtype.Text
	var providerName pgtype.Text
	var providerCode pgtype.Text
	if err := scanner.Scan(
		&model.ID,
		&model.ProviderID,
		&model.Name,
		&model.ModelID,
		&model.DefaultTemperature,
		&maxTokens,
		&topP,
		&model.DefaultTimeout,
		&model.DefaultMaxRetries,
		&model.IsActive,
		&model.IsDefault,
		&capabilitiesRaw,
		&description,
		&model.CreatedAt,
		&model.UpdatedAt,
		&providerName,
		&providerCode,
	); err != nil {
		return adminaiconfigapp.LLMModel{}, err
	}
	capabilities, err := decodeObjectMap(capabilitiesRaw)
	if err != nil {
		return adminaiconfigapp.LLMModel{}, fmt.Errorf("decode llm model capabilities: %w", err)
	}
	model.DefaultMaxTokens = intPtr(maxTokens)
	model.DefaultTopP = floatPtr(topP)
	model.Capabilities = capabilities
	model.Description = textPtr(description)
	model.ProviderName = textPtr(providerName)
	model.ProviderCode = textPtr(providerCode)
	return model, nil
}

func scanAgentModelConfig(scanner rowScanner) (adminaiconfigapp.AgentModelConfig, error) {
	var config adminaiconfigapp.AgentModelConfig
	var modelID pgtype.Text
	var modelKey pgtype.Text
	var temperature pgtype.Float8
	var maxTokens pgtype.Int4
	var topP pgtype.Float8
	var timeout pgtype.Int4
	var retries pgtype.Int4
	var extraConfigRaw []byte
	var modelName pgtype.Text
	var modelModelID pgtype.Text
	var providerName pgtype.Text
	if err := scanner.Scan(
		&config.ID,
		&config.AgentType,
		&modelID,
		&modelKey,
		&temperature,
		&maxTokens,
		&topP,
		&timeout,
		&retries,
		&extraConfigRaw,
		&config.IsActive,
		&config.CreatedAt,
		&config.UpdatedAt,
		&modelName,
		&modelModelID,
		&providerName,
	); err != nil {
		return adminaiconfigapp.AgentModelConfig{}, err
	}
	extraConfig, err := decodeObjectMap(extraConfigRaw)
	if err != nil {
		return adminaiconfigapp.AgentModelConfig{}, fmt.Errorf("decode agent extra config: %w", err)
	}
	config.ModelID = textPtr(modelID)
	config.ModelKey = textPtr(modelKey)
	config.TemperatureOverride = floatPtr(temperature)
	config.MaxTokensOverride = intPtr(maxTokens)
	config.TopPOverride = floatPtr(topP)
	config.TimeoutOverride = intPtr(timeout)
	config.MaxRetriesOverride = intPtr(retries)
	config.ExtraConfig = extraConfig
	config.ModelName = textPtr(modelName)
	config.ModelModelID = textPtr(modelModelID)
	config.ProviderName = textPtr(providerName)
	return config, nil
}

func scanRuntimeCandidate(scanner rowScanner) (adminaiconfigapp.RuntimeCandidate, error) {
	var candidate adminaiconfigapp.RuntimeCandidate
	var providerDescription pgtype.Text
	var maxTokens pgtype.Int4
	var topP pgtype.Float8
	var capabilitiesRaw []byte
	var modelDescription pgtype.Text
	if err := scanner.Scan(
		&candidate.Provider.ID,
		&candidate.Provider.Name,
		&candidate.Provider.Code,
		&candidate.Provider.BaseURL,
		&candidate.Provider.EncryptedAPIKey,
		&candidate.Provider.Priority,
		&candidate.Provider.Weight,
		&candidate.Provider.IsActive,
		&providerDescription,
		&candidate.Provider.CreatedAt,
		&candidate.Provider.UpdatedAt,
		&candidate.Model.ID,
		&candidate.Model.ProviderID,
		&candidate.Model.Name,
		&candidate.Model.ModelID,
		&candidate.Model.DefaultTemperature,
		&maxTokens,
		&topP,
		&candidate.Model.DefaultTimeout,
		&candidate.Model.DefaultMaxRetries,
		&candidate.Model.IsActive,
		&candidate.Model.IsDefault,
		&capabilitiesRaw,
		&modelDescription,
		&candidate.Model.CreatedAt,
		&candidate.Model.UpdatedAt,
	); err != nil {
		return adminaiconfigapp.RuntimeCandidate{}, err
	}
	capabilities, err := decodeObjectMap(capabilitiesRaw)
	if err != nil {
		return adminaiconfigapp.RuntimeCandidate{}, fmt.Errorf("decode runtime model capabilities: %w", err)
	}
	candidate.Provider.Description = textPtr(providerDescription)
	candidate.Model.DefaultMaxTokens = intPtr(maxTokens)
	candidate.Model.DefaultTopP = floatPtr(topP)
	candidate.Model.Capabilities = capabilities
	candidate.Model.Description = textPtr(modelDescription)
	candidate.Model.ProviderName = &candidate.Provider.Name
	candidate.Model.ProviderCode = &candidate.Provider.Code
	return candidate, nil
}

func findModelByProviderModelID(models []adminaiconfigapp.LLMModel, providerModelID string) (adminaiconfigapp.LLMModel, bool) {
	for _, model := range models {
		if model.ModelID == providerModelID {
			return model, true
		}
	}
	return adminaiconfigapp.LLMModel{}, false
}

func jsonObject(value map[string]any) string {
	if value == nil {
		return "{}"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func normalizeAIConfigPGError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return adminaiconfigapp.Error{Kind: adminaiconfigapp.ErrConflict, Message: "AI 配置名称或模型已存在"}
		case "23503":
			return adminaiconfigapp.Error{Kind: adminaiconfigapp.ErrBadRequest, Message: "关联的渠道或模型不存在"}
		}
	}
	return err
}
