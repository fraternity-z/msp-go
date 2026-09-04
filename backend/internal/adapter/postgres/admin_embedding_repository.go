package postgres

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	adminaiconfigapp "mathstudy/backend/internal/application/adminaiconfig"
)

// ListEmbeddingModelVersions returns administrator-managed embedding snapshots.
func (r AdminAIConfigRepository) ListEmbeddingModelVersions(ctx context.Context, logicalName string) ([]adminaiconfigapp.EmbeddingModelVersion, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT `+embeddingVersionSelectColumns+`
		FROM public.embedding_model_versions v
		LEFT JOIN public.llm_models m ON m.id = v.llm_model_id
		LEFT JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE v.logical_name = $1
		ORDER BY (v.status = 'active') DESC, v.created_at DESC, v.id DESC`,
		strings.TrimSpace(logicalName),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []adminaiconfigapp.EmbeddingModelVersion{}
	for rows.Next() {
		version, err := scanEmbeddingModelVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// ActivateEmbeddingModelVersion atomically retires the prior active snapshot and activates input.
func (r AdminAIConfigRepository) ActivateEmbeddingModelVersion(ctx context.Context, input adminaiconfigapp.EmbeddingModelVersionInput, now time.Time) (adminaiconfigapp.EmbeddingModelVersion, error) {
	var activatedID string
	err := r.withTx(ctx, func(tx AdminAIConfigRepository) error {
		if err := tx.lockEmbeddingActivation(ctx); err != nil {
			return err
		}
		model, found, err := tx.GetModel(ctx, input.LLMModelID)
		if err != nil {
			return err
		}
		if !found {
			return embeddingActivationSourceConflict()
		}
		provider, found, err := tx.GetProvider(ctx, model.ProviderID)
		if err != nil {
			return err
		}
		if !found || !model.IsActive || !provider.IsActive ||
			model.ModelID != input.ProviderModel || provider.Code != input.Provider ||
			provider.BaseURL != input.ProviderSnapshot.BaseURL ||
			provider.EncryptedAPIKey != input.ProviderSnapshot.EncryptedCredential ||
			!adminaiconfigapp.EmbeddingSourceVersionMatches(
				adminaiconfigapp.EmbeddingModelVersion{Metadata: input.Metadata}, model, provider,
			) {
			return embeddingActivationSourceConflict()
		}
		existing, found, err := tx.getEmbeddingModelVersionByIdentity(ctx, input.Provider, input.ProviderModel, input.Revision)
		if err != nil {
			return err
		}
		if found && !embeddingVersionMatchesInput(existing, input) {
			return adminaiconfigapp.Error{
				Kind:    adminaiconfigapp.ErrConflict,
				Message: "相同 provider、model 和 revision 已存在不同向量契约或来源，请更新 revision 后重试",
			}
		}
		if _, err := tx.DB().Exec(ctx, `
			UPDATE public.embedding_model_versions
			SET status = 'retired', retired_at = $2, updated_at = $2
			WHERE logical_name = $1 AND status = 'active'`,
			input.LogicalName,
			now,
		); err != nil {
			return err
		}
		if found {
			activatedID = existing.ID
			_, err = tx.DB().Exec(ctx, `
				UPDATE public.embedding_model_versions
				SET status = 'active', verified_at = $2, activated_at = $2,
					retired_at = NULL, metadata = $3::json, updated_at = $2
				WHERE id = $1`,
				existing.ID,
				now,
				jsonObject(input.Metadata),
			)
			return err
		}
		activatedID = input.ID
		_, err = tx.DB().Exec(ctx, `
			INSERT INTO public.embedding_model_versions (
				id, logical_name, llm_model_id, provider, provider_model, revision,
				dimension, metric, tokenizer, normalization, max_tokens,
				send_dimensions, batch_size, timeout_seconds, max_retries,
				status, metadata, verified_at, activated_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8::public.distancemetric, $9, $10,
				$11, $12, $13, $14, $15, 'active', $16::json, $17, $17, $17, $17
			)`,
			input.ID,
			input.LogicalName,
			input.LLMModelID,
			input.Provider,
			input.ProviderModel,
			input.Revision,
			input.Dimension,
			embeddingMetricToDB(input.Metric),
			input.Tokenizer,
			input.Normalization,
			input.MaxTokens,
			input.SendDimensions,
			input.BatchSize,
			input.TimeoutSeconds,
			input.MaxRetries,
			jsonObject(input.Metadata),
			input.VerifiedAt,
		)
		return normalizeAIConfigPGError(err)
	})
	if err != nil {
		return adminaiconfigapp.EmbeddingModelVersion{}, err
	}
	version, found, err := r.getEmbeddingModelVersionByID(ctx, activatedID)
	if err != nil {
		return adminaiconfigapp.EmbeddingModelVersion{}, err
	}
	if !found {
		return adminaiconfigapp.EmbeddingModelVersion{}, pgx.ErrNoRows
	}
	return version, nil
}

func embeddingActivationSourceConflict() error {
	return adminaiconfigapp.Error{Kind: adminaiconfigapp.ErrConflict, Message: "向量模型来源已变更或停用，请重新测试后激活"}
}

// GetActiveEmbeddingRuntimeCandidate resolves one active snapshot and its live channel credentials.
func (r AdminAIConfigRepository) GetActiveEmbeddingRuntimeCandidate(ctx context.Context, logicalName string) (adminaiconfigapp.EmbeddingRuntimeCandidate, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT `+embeddingVersionSelectColumns+`
		FROM public.embedding_model_versions v
		LEFT JOIN public.llm_models m ON m.id = v.llm_model_id
		LEFT JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE v.logical_name = $1 AND v.status = 'active'`,
		strings.TrimSpace(logicalName),
	)
	version, err := scanEmbeddingModelVersion(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return adminaiconfigapp.EmbeddingRuntimeCandidate{}, false, nil
		}
		return adminaiconfigapp.EmbeddingRuntimeCandidate{}, false, err
	}
	if version.LLMModelID == nil {
		return adminaiconfigapp.EmbeddingRuntimeCandidate{}, false, nil
	}
	model, found, err := r.GetModel(ctx, *version.LLMModelID)
	if err != nil || !found {
		return adminaiconfigapp.EmbeddingRuntimeCandidate{}, false, err
	}
	provider, found, err := r.GetProvider(ctx, model.ProviderID)
	if err != nil || !found {
		return adminaiconfigapp.EmbeddingRuntimeCandidate{}, false, err
	}
	if !model.IsActive || !provider.IsActive || model.ModelID != version.ProviderModel ||
		provider.Code != version.Provider || !adminaiconfigapp.EmbeddingSourceVersionMatches(version, model, provider) {
		return adminaiconfigapp.EmbeddingRuntimeCandidate{}, false, nil
	}
	return adminaiconfigapp.EmbeddingRuntimeCandidate{Version: version, Model: model, Provider: provider}, true, nil
}

func (r AdminAIConfigRepository) getEmbeddingModelVersionByIdentity(ctx context.Context, provider string, providerModel string, revision string) (adminaiconfigapp.EmbeddingModelVersion, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT `+embeddingVersionSelectColumns+`
		FROM public.embedding_model_versions v
		LEFT JOIN public.llm_models m ON m.id = v.llm_model_id
		LEFT JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE v.provider = $1 AND v.provider_model = $2 AND v.revision = $3`,
		provider,
		providerModel,
		revision,
	)
	version, err := scanEmbeddingModelVersion(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return adminaiconfigapp.EmbeddingModelVersion{}, false, nil
		}
		return adminaiconfigapp.EmbeddingModelVersion{}, false, err
	}
	return version, true, nil
}

func (r AdminAIConfigRepository) getEmbeddingModelVersionByID(ctx context.Context, id string) (adminaiconfigapp.EmbeddingModelVersion, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT `+embeddingVersionSelectColumns+`
		FROM public.embedding_model_versions v
		LEFT JOIN public.llm_models m ON m.id = v.llm_model_id
		LEFT JOIN public.llm_providers p ON p.id = m.provider_id
		WHERE v.id = $1`,
		id,
	)
	version, err := scanEmbeddingModelVersion(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return adminaiconfigapp.EmbeddingModelVersion{}, false, nil
		}
		return adminaiconfigapp.EmbeddingModelVersion{}, false, err
	}
	return version, true, nil
}

const embeddingVersionSelectColumns = `
		v.id,
		v.logical_name,
		v.llm_model_id,
		v.provider,
		v.provider_model,
		v.revision,
		v.dimension,
		v.metric::text,
		v.tokenizer,
		v.normalization,
		v.max_tokens,
		v.send_dimensions,
		v.batch_size,
		v.timeout_seconds,
		v.max_retries,
		v.status,
		v.metadata,
		v.verified_at,
		v.activated_at,
		v.retired_at,
		v.created_at,
		v.updated_at,
		p.name AS provider_name,
		m.name AS model_name`

func scanEmbeddingModelVersion(scanner rowScanner) (adminaiconfigapp.EmbeddingModelVersion, error) {
	var version adminaiconfigapp.EmbeddingModelVersion
	var llmModelID, tokenizer, normalization, providerName, modelName pgtype.Text
	var metric string
	var metadataRaw []byte
	var verifiedAt, activatedAt, retiredAt pgtype.Timestamp
	if err := scanner.Scan(
		&version.ID,
		&version.LogicalName,
		&llmModelID,
		&version.Provider,
		&version.ProviderModel,
		&version.Revision,
		&version.Dimension,
		&metric,
		&tokenizer,
		&normalization,
		&version.MaxTokens,
		&version.SendDimensions,
		&version.BatchSize,
		&version.TimeoutSeconds,
		&version.MaxRetries,
		&version.Status,
		&metadataRaw,
		&verifiedAt,
		&activatedAt,
		&retiredAt,
		&version.CreatedAt,
		&version.UpdatedAt,
		&providerName,
		&modelName,
	); err != nil {
		return adminaiconfigapp.EmbeddingModelVersion{}, err
	}
	metadata, err := decodeObjectMap(metadataRaw)
	if err != nil {
		return adminaiconfigapp.EmbeddingModelVersion{}, fmt.Errorf("decode embedding model metadata: %w", err)
	}
	version.LLMModelID = textPtr(llmModelID)
	version.Metric = embeddingMetricFromDB(metric)
	version.Tokenizer = textPtr(tokenizer)
	version.Normalization = textPtr(normalization)
	version.Metadata = metadata
	version.VerifiedAt = timestampPtr(verifiedAt)
	version.ActivatedAt = timestampPtr(activatedAt)
	version.RetiredAt = timestampPtr(retiredAt)
	version.ProviderName = textPtr(providerName)
	version.ModelName = textPtr(modelName)
	return version, nil
}

func embeddingVersionMatchesInput(version adminaiconfigapp.EmbeddingModelVersion, input adminaiconfigapp.EmbeddingModelVersionInput) bool {
	return version.LogicalName == input.LogicalName &&
		version.LLMModelID != nil && *version.LLMModelID == input.LLMModelID &&
		version.Provider == input.Provider &&
		version.ProviderModel == input.ProviderModel &&
		version.Revision == input.Revision &&
		version.Dimension == input.Dimension &&
		version.Metric == input.Metric &&
		stringPointerEqual(version.Tokenizer, input.Tokenizer) &&
		stringPointerEqual(version.Normalization, input.Normalization) &&
		version.MaxTokens == input.MaxTokens &&
		version.SendDimensions == input.SendDimensions &&
		version.BatchSize == input.BatchSize &&
		version.TimeoutSeconds == input.TimeoutSeconds &&
		version.MaxRetries == input.MaxRetries &&
		reflect.DeepEqual(version.Metadata, input.Metadata)
}

func stringPointerEqual(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func embeddingMetricToDB(metric string) string {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case adminaiconfigapp.EmbeddingMetricDot:
		return "IP"
	case adminaiconfigapp.EmbeddingMetricEuclid:
		return "L2"
	default:
		return "COSINE"
	}
}

func embeddingMetricFromDB(metric string) string {
	switch strings.ToUpper(strings.TrimSpace(metric)) {
	case "IP":
		return adminaiconfigapp.EmbeddingMetricDot
	case "L2":
		return adminaiconfigapp.EmbeddingMetricEuclid
	default:
		return adminaiconfigapp.EmbeddingMetricCosine
	}
}
