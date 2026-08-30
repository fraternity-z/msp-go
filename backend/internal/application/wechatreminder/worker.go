package wechatreminder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"mathstudy/backend/internal/platform/identifier"
)

const (
	defaultPollInterval  = 2 * time.Second
	defaultLeaseDuration = 90 * time.Second
	defaultSendTimeout   = 10 * time.Second
	defaultBatchSize     = 5
	defaultMaxAttempts   = 5
	finishedJobRetention = 30 * 24 * time.Hour
	cleanupInterval      = time.Hour
	cleanupBatchSize     = 1000
	maxCleanupBatches    = 10
	messagePreviewRunes  = 40
	templateTimeLayout   = "2006-01-02 15:04"
	maxTemplateIDBytes   = 256
)

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

var retrySchedule = [...]time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
}

// Config controls durable reminder processing. Enabled defaults to false at the platform layer.
type Config struct {
	Enabled                  bool
	AppID                    string
	PrivateMessageTemplateID string
	NoticeTemplateID         string
	QAMessageTemplateID      string
	PollInterval             time.Duration
	LeaseDuration            time.Duration
	SendTimeout              time.Duration
	BatchSize                int
	MaxAttempts              int
}

// Worker claims reminder jobs and performs WeChat calls outside business transactions.
type Worker struct {
	repository Repository
	sender     Sender
	classify   ErrorClassifier
	logger     *slog.Logger
	config     Config
	now        func() time.Time
}

// NewWorker creates a reminder worker. Disabled workers do not require dependencies.
func NewWorker(repository Repository, sender Sender, classify ErrorClassifier, logger *slog.Logger, cfg Config) (*Worker, error) {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = defaultSendTimeout
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	minimumLease := time.Duration(cfg.BatchSize)*cfg.SendTimeout + 5*time.Second
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = max(defaultLeaseDuration, minimumLease)
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Enabled {
		if repository == nil {
			return nil, errors.New("wechat reminder repository is nil")
		}
		if sender == nil {
			return nil, errors.New("wechat reminder sender is nil")
		}
		if cfg.AppID == "" {
			return nil, errors.New("wechat reminder app ID is empty")
		}
		if !validTemplateID(cfg.PrivateMessageTemplateID) ||
			!validTemplateID(cfg.NoticeTemplateID) ||
			!validTemplateID(cfg.QAMessageTemplateID) {
			return nil, errors.New("invalid wechat reminder template ID")
		}
		if cfg.LeaseDuration < minimumLease {
			return nil, fmt.Errorf("wechat reminder lease duration must be at least %s", minimumLease)
		}
	}
	if classify == nil {
		classify = ClassifySendError
	}
	return &Worker{
		repository: repository,
		sender:     sender,
		classify:   classify,
		logger:     logger,
		config:     cfg,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// Run processes ready jobs until ctx is canceled.
func (w *Worker) Run(ctx context.Context) error {
	if !w.config.Enabled {
		return nil
	}
	owner, err := identifier.NewUUID()
	if err != nil {
		return fmt.Errorf("generate wechat reminder worker owner: %w", err)
	}
	var nextCleanupAt time.Time
	for {
		now := w.now()
		if nextCleanupAt.IsZero() || !now.Before(nextCleanupAt) {
			w.cleanupFinished(ctx, now.Add(-finishedJobRetention))
			nextCleanupAt = now.Add(cleanupInterval)
		}
		claimed, runErr := w.processBatch(ctx, owner)
		if ctx.Err() != nil {
			return nil
		}
		if runErr != nil {
			w.logger.Error("wechat reminder batch failed", "error_code", "repository_error")
		}
		if runErr == nil && claimed == w.config.BatchSize {
			continue
		}
		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) processBatch(ctx context.Context, owner string) (int, error) {
	now := w.now()
	jobs, err := w.repository.Claim(
		ctx,
		w.config.AppID,
		owner,
		now,
		now.Add(w.config.LeaseDuration),
		w.config.BatchSize,
	)
	if err != nil {
		return 0, err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return len(jobs), nil
		}
		w.processJob(ctx, owner, job)
	}
	return len(jobs), nil
}

func (w *Worker) processJob(ctx context.Context, owner string, job Job) {
	templateID, ok := w.templateID(job.EventType)
	if !ok {
		w.finishDead(ctx, owner, job, SendFailure{Disposition: FailureDead, Code: "unsupported_event"})
		return
	}
	_, eligible, skipCode, err := w.repository.ResolveDelivery(ctx, w.config.AppID, job)
	if err != nil {
		w.transitionFailure(ctx, owner, job, SendFailure{Disposition: FailureRetry, Code: "resolve_failed"})
		return
	}
	if !eligible {
		w.finishSkipped(ctx, owner, job, skipCode)
		return
	}
	leaseNow := w.now()
	renewed, err := w.repository.RenewLease(
		ctx,
		job.ID,
		owner,
		leaseNow,
		leaseNow.Add(w.config.LeaseDuration),
	)
	if err != nil || !renewed {
		w.logTransition("renewed", job, renewed, err, "")
		return
	}
	delivery, eligible, skipCode, err := w.repository.ResolveDelivery(ctx, w.config.AppID, job)
	if err != nil {
		w.transitionFailure(ctx, owner, job, SendFailure{Disposition: FailureRetry, Code: "resolve_failed"})
		return
	}
	if !eligible {
		w.finishSkipped(ctx, owner, job, skipCode)
		return
	}
	fields := templateFields(job.EventType, delivery)
	sendCtx, cancel := context.WithTimeout(ctx, w.config.SendTimeout)
	err = w.sender.SendTemplate(sendCtx, delivery.OpenID, templateID, fields)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.transitionFailure(ctx, owner, job, w.classify(err))
		return
	}
	w.finishSent(ctx, owner, job)
}

func (w *Worker) cleanupFinished(ctx context.Context, before time.Time) {
	for batch := 0; batch < maxCleanupBatches && ctx.Err() == nil; batch++ {
		deleted, err := w.repository.DeleteFinishedBefore(
			ctx,
			w.config.AppID,
			before,
			cleanupBatchSize,
		)
		if err != nil {
			w.logger.Error("wechat reminder cleanup failed", "error_code", "repository_error")
			return
		}
		if deleted < cleanupBatchSize {
			return
		}
	}
}

func (w *Worker) transitionFailure(ctx context.Context, owner string, job Job, failure SendFailure) {
	if failure.Code == "" {
		failure.Code = "send_failed"
	}
	switch failure.Disposition {
	case FailureSkip:
		w.finishSkippedWithProvider(ctx, owner, job, failure)
	case FailureDead:
		w.finishDead(ctx, owner, job, failure)
	default:
		if job.AttemptCount >= w.config.MaxAttempts {
			failure.Code = "attempts_exhausted"
			w.finishDead(ctx, owner, job, failure)
			return
		}
		nextAttempt := w.now().Add(retryDelay(job.ID, job.AttemptCount))
		updated, err := w.repository.Reschedule(
			ctx,
			job.ID,
			owner,
			failure.Code,
			failure.ProviderCode,
			nextAttempt,
		)
		w.logTransition("retry", job, updated, err, failure.Code)
	}
}

func (w *Worker) finishSent(ctx context.Context, owner string, job Job) {
	updated, err := w.repository.MarkSent(ctx, job.ID, owner, w.now())
	w.logTransition("sent", job, updated, err, "")
}

func (w *Worker) finishSkipped(ctx context.Context, owner string, job Job, code string) {
	if code == "" {
		code = "recipient_unavailable"
	}
	updated, err := w.repository.MarkSkipped(ctx, job.ID, owner, code, nil, w.now())
	w.logTransition("skipped", job, updated, err, code)
}

func (w *Worker) finishSkippedWithProvider(ctx context.Context, owner string, job Job, failure SendFailure) {
	updated, err := w.repository.MarkSkipped(ctx, job.ID, owner, failure.Code, failure.ProviderCode, w.now())
	w.logTransition("skipped", job, updated, err, failure.Code)
}

func (w *Worker) finishDead(ctx context.Context, owner string, job Job, failure SendFailure) {
	updated, err := w.repository.MarkDead(
		ctx,
		job.ID,
		owner,
		failure.Code,
		failure.ProviderCode,
		w.now(),
	)
	w.logTransition("dead", job, updated, err, failure.Code)
}

func (w *Worker) logTransition(status string, job Job, updated bool, err error, code string) {
	if err != nil {
		w.logger.Error(
			"wechat reminder transition failed",
			"job_id", job.ID,
			"event_type", job.EventType,
			"target_status", status,
			"error_code", "repository_error",
		)
		return
	}
	if !updated {
		w.logger.Warn(
			"wechat reminder lease lost",
			"job_id", job.ID,
			"event_type", job.EventType,
			"target_status", status,
		)
		return
	}
	if status == "dead" {
		w.logger.Error(
			"wechat reminder permanently failed",
			"job_id", job.ID,
			"event_type", job.EventType,
			"error_code", code,
		)
	}
}

// ClassifySendError maps provider and transport failures to safe queue transitions.
func ClassifySendError(err error) SendFailure {
	if err == nil {
		return SendFailure{}
	}
	var provider ProviderError
	if errors.As(err, &provider) {
		code := provider.WechatProviderCode()
		var providerCode *int
		if code != 0 {
			providerCode = &code
		}
		switch code {
		case 45015:
			return SendFailure{Disposition: FailureSkip, Code: "interaction_window_closed", ProviderCode: providerCode}
		case 40003, 43004, 46004:
			return SendFailure{Disposition: FailureSkip, Code: "recipient_rejected", ProviderCode: providerCode}
		case 40001, 40014, 42001:
			return SendFailure{Disposition: FailureRetry, Code: "access_token_invalid", ProviderCode: providerCode}
		case 40013, 40125, 40164, 48001:
			return SendFailure{Disposition: FailureDead, Code: "provider_configuration", ProviderCode: providerCode}
		case 40037:
			return SendFailure{Disposition: FailureDead, Code: "template_configuration", ProviderCode: providerCode}
		case 47003:
			return SendFailure{Disposition: FailureDead, Code: "template_schema", ProviderCode: providerCode}
		}
		if provider.WechatRetryable() || provider.WechatHTTPStatus() == 429 || provider.WechatHTTPStatus() >= 500 {
			return SendFailure{Disposition: FailureRetry, Code: "provider_retryable", ProviderCode: providerCode}
		}
		return SendFailure{Disposition: FailureDead, Code: "provider_rejected", ProviderCode: providerCode}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return SendFailure{Disposition: FailureRetry, Code: "send_timeout"}
	}
	return SendFailure{Disposition: FailureRetry, Code: "send_failed"}
}

func (w *Worker) templateID(eventType EventType) (string, bool) {
	switch eventType {
	case EventPrivateMessage:
		return w.config.PrivateMessageTemplateID, true
	case EventNotice:
		return w.config.NoticeTemplateID, true
	case EventQAMessage:
		return w.config.QAMessageTemplateID, true
	case EventDailyQuestion:
		return w.config.NoticeTemplateID, true
	default:
		return "", false
	}
}

func templateFields(eventType EventType, delivery Delivery) map[string]string {
	content := normalizeWhitespace(delivery.Content)
	if eventType == EventPrivateMessage || eventType == EventQAMessage {
		content = truncateRunes(content, messagePreviewRunes)
	}
	if content == "" {
		if eventType == EventNotice || eventType == EventDailyQuestion {
			content = "新通知"
		} else {
			content = "新消息"
		}
	}
	actorName := normalizeWhitespace(delivery.ActorName)
	if actorName == "" {
		actorName = "用户"
	}
	return map[string]string{
		"keyword1": actorName,
		"keyword2": content,
		"keyword3": delivery.OccurredAt.In(shanghaiLocation).Format(templateTimeLayout),
	}
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func validTemplateID(value string) bool {
	return value != "" && len(value) <= maxTemplateIDBytes && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\t\r\n")
}

func retryDelay(jobID int64, attempt int) time.Duration {
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(retrySchedule) {
		index = len(retrySchedule) - 1
	}
	base := retrySchedule[index]
	// Stable per-job jitter spreads retries without global mutable random state.
	jitterPercent := int64(90) + (jobID*31+int64(attempt)*17)%21
	return time.Duration(int64(base) * jitterPercent / 100)
}
